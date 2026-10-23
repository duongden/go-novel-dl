package site

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/html"
	charsetpkg "golang.org/x/net/html/charset"

	"github.com/guohuiyuan/go-novel-dl/internal/config"
	"github.com/guohuiyuan/go-novel-dl/internal/model"
)

var (
	fanqieBookRe         = regexp.MustCompile(`^/page/(\d+)/?$`)
	fanqieChapterRe      = regexp.MustCompile(`^/reader/(\d+)/?$`)
	fanqieInitialStateRe = regexp.MustCompile(`(?s)window\.__INITIAL_STATE__\s*=\s*({.*?})\s*;`)
	fanqieNextDataRe     = regexp.MustCompile(`(?s)<script[^>]*id=["']__NEXT_DATA__["'][^>]*>(.*?)</script>`)
	fanqieJSONLDRe       = regexp.MustCompile(`(?s)<script[^>]*type=["']application/ld\+json["'][^>]*>(.*?)</script>`)
	fanqieBookIDRe       = regexp.MustCompile(`^\d+$`)
)

const (
	fanqieSearchAPI              = "https://fanqienovel.com/api/author/search/search_book/v1"
	fanqieReaderURL              = "https://fanqienovel.com/reader/"
	fanqieDirectoryAPI           = "https://fanqienovel.com/api/reader/directory/detail"
	fanqieDirectoryMinInterval   = 800 * time.Millisecond
	fanqieDirectoryCacheFileName = "directory.json"
)

//go:embed resources/fanqienovel.json
var fanqieMapRaw string

var fanqieMap = mustLoadSubstMap(fanqieMapRaw)

type FanqieNovelSite struct {
	cfg               config.ResolvedSiteConfig
	html              HTMLSite
	client            *http.Client
	searchURL         string
	directoryURL      string
	rankPageURL       string
	rankRecommendURL  string
	rankCategoryURL   string
	directoryMu       sync.Mutex
	lastDirectoryCall time.Time
	directoryInterval time.Duration
}

func NewFanqieNovelSite(cfg config.ResolvedSiteConfig) *FanqieNovelSite {
	timeout := 15 * time.Second
	if cfg.General.Timeout > 0 {
		timeout = time.Duration(cfg.General.Timeout * float64(time.Second))
	}
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Timeout: timeout, Jar: jar}
	site := &FanqieNovelSite{
		cfg:       cfg,
		html:      NewHTMLSite(client),
		client:    client,
		searchURL: fanqieSearchAPI,

		directoryURL:      fanqieDirectoryAPI,
		rankPageURL:       "https://fanqienovel.com/rank",
		rankRecommendURL:  "https://fanqienovel.com/api/rank/recommend/list",
		rankCategoryURL:   "https://fanqienovel.com/api/rank/category/list",
		directoryInterval: fanqieDirectoryMinInterval,
	}
	site.injectCookieString(cfg.Cookie)
	return site
}

func (s *FanqieNovelSite) Key() string         { return "fanqienovel" }
func (s *FanqieNovelSite) DisplayName() string { return "FanqieNovel" }
func (s *FanqieNovelSite) Capabilities() Capabilities {
	return Capabilities{Download: true, Search: true, Login: s != nil && strings.TrimSpace(s.cfg.Cookie) != ""}
}

func (s *FanqieNovelSite) injectCookieString(raw string) {
	if s == nil || s.client == nil {
		return
	}
	raw = strings.TrimSpace(raw)
	if len(raw) >= len("Cookie:") && strings.EqualFold(raw[:len("Cookie:")], "Cookie:") {
		raw = strings.TrimSpace(raw[len("Cookie:"):])
	}
	if raw == "" {
		return
	}
	if s.client.Jar == nil {
		jar, _ := cookiejar.New(nil)
		s.client.Jar = jar
	}
	base, err := url.Parse("https://fanqienovel.com/")
	if err != nil {
		return
	}
	cookies := make([]*http.Cookie, 0)
	for _, part := range strings.Split(raw, ";") {
		parts := strings.SplitN(strings.TrimSpace(part), "=", 2)
		if len(parts) != 2 || strings.TrimSpace(parts[0]) == "" {
			continue
		}
		cookies = append(cookies, &http.Cookie{
			Name:   strings.TrimSpace(parts[0]),
			Value:  strings.TrimSpace(parts[1]),
			Path:   "/",
			Domain: ".fanqienovel.com",
		})
	}
	if len(cookies) > 0 {
		s.client.Jar.SetCookies(base, cookies)
	}
}

func (s *FanqieNovelSite) ResolveURL(rawURL string) (*ResolvedURL, bool) {
	parsed, err := normalizeURL(rawURL)
	if err != nil {
		return nil, false
	}
	host := strings.ToLower(strings.TrimPrefix(parsed.Host, "www."))
	if host != "fanqienovel.com" {
		return nil, false
	}
	if m := fanqieChapterRe.FindStringSubmatch(parsed.Path); len(m) == 2 {
		return &ResolvedURL{SiteKey: s.Key(), ChapterID: m[1], Canonical: "https://fanqienovel.com" + parsed.Path}, true
	}
	if m := fanqieBookRe.FindStringSubmatch(parsed.Path); len(m) == 2 {
		return &ResolvedURL{SiteKey: s.Key(), BookID: m[1], Canonical: "https://fanqienovel.com" + parsed.Path}, true
	}
	return nil, false
}

func (s *FanqieNovelSite) Download(ctx context.Context, ref model.BookRef) (*model.Book, error) {
	book, err := s.DownloadPlan(ctx, ref)
	if err != nil {
		return nil, err
	}
	for idx, chapter := range book.Chapters {
		loaded, err := s.FetchChapter(ctx, ref.BookID, chapter)
		if err != nil {
			return nil, err
		}
		loaded.Order = idx + 1
		book.Chapters[idx] = loaded
	}
	return book, nil
}

func (s *FanqieNovelSite) DownloadPlan(ctx context.Context, ref model.BookRef) (*model.Book, error) {
	book, err := s.fetchBook(ctx, ref.BookID)
	if err != nil {
		return nil, err
	}

	// The book page only renders the first visible volume. The directory API
	// returns the complete list (every volume), so it is the source of truth for
	// the download plan. Chapter content continues to use the adapter's existing
	// helper endpoint and remains subject to the source service's availability.
	chapters, err := s.fetchDirectory(ctx, ref.BookID)
	if err != nil {
		return nil, err
	}
	book.Chapters = applyChapterRange(chapters, ref)
	return book, nil
}

func (s *FanqieNovelSite) fetchBook(ctx context.Context, bookID string) (*model.Book, error) {
	pageURL := fmt.Sprintf("https://fanqienovel.com/page/%s", bookID)
	markup, err := s.getHTML(ctx, pageURL, "")
	if err != nil {
		return nil, err
	}
	page, err := extractFanqieBookMetadata(markup)
	if err != nil {
		return nil, err
	}

	title := applySubstMap(fallback(
		stringValue(page["bookName"]),
		fallback(
			stringValue(page["book_name"]),
			fallback(stringValue(page["title"]), stringValue(page["name"])),
		),
	), fanqieMap)
	if title == "" {
		return nil, fmt.Errorf("fanqienovel book title not found")
	}
	author := applySubstMap(fallback(
		stringValue(page["authorName"]),
		fallback(
			stringValue(page["author_name"]),
			fanqieAuthorName(page["author"]),
		),
	), fanqieMap)
	description := applySubstMap(fallback(
		stringValue(page["abstract"]),
		fallback(
			stringValue(page["description"]),
			fallback(stringValue(page["intro"]), stringValue(page["introduce"])),
		),
	), fanqieMap)
	cover := fanqiePageCover(page)
	if cover == "" {
		cover = fanqieJSONLDCover(markup)
	}
	tags := fanqiePageTags(page)
	if len(tags) == 0 {
		tags = fanqieInfoLabelTags(markup)
	}

	return &model.Book{
		Site:         s.Key(),
		ID:           bookID,
		Title:        title,
		Author:       author,
		Description:  description,
		SourceURL:    pageURL,
		CoverURL:     cover,
		Tags:         tags,
		DownloadedAt: time.Now().UTC(),
		UpdatedAt:    time.Now().UTC(),
	}, nil
}

func extractFanqieBookMetadata(markup string) (map[string]any, error) {
	if state, err := extractFanqieInitialState(markup); err == nil {
		if page := mapValue(state, "page"); page != nil {
			return page, nil
		}
		if page := findFanqieBookObject(state); page != nil {
			return page, nil
		}
	}

	if match := fanqieNextDataRe.FindStringSubmatch(markup); len(match) == 2 {
		var data map[string]any
		if err := decodeFanqieJSONObject(match[1], &data); err == nil {
			if page := findFanqieBookObject(data); page != nil {
				return page, nil
			}
		}
	}

	for _, match := range fanqieJSONLDRe.FindAllStringSubmatch(markup, -1) {
		if len(match) != 2 {
			continue
		}
		var data map[string]any
		if err := decodeFanqieJSONObject(match[1], &data); err == nil {
			if page := findFanqieBookObject(data); page != nil {
				return page, nil
			}
		}
	}
	return nil, fmt.Errorf("fanqienovel book metadata not found")
}

func decodeFanqieJSONObject(raw string, target any) error {
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.UseNumber()
	return decoder.Decode(target)
}

func findFanqieBookObject(value any) map[string]any {
	current := mapValue(value)
	if current != nil {
		if fanqieJSONLDTypeIsBook(current["@type"]) || fanqieBookObjectHasTitle(current) {
			for _, contentKey := range []string{
				"@type", "abstract", "description", "intro", "introduce", "author", "authorName",
				"author_name", "cover", "cover_url", "image", "images", "thumbUri", "thumb_uri",
				"thumb_url", "detail_page_thumb_url", "chapterCount", "chapter_count", "tags",
				"tagNames", "tag_names", "genre", "category", "categoryV2",
			} {
				if _, ok := current[contentKey]; ok {
					return current
				}
			}
		}
		preferred := []string{"page", "book", "bookInfo", "book_info", "props", "data", "@graph"}
		visited := make(map[string]struct{}, len(preferred))
		for _, key := range preferred {
			visited[key] = struct{}{}
			if nested, ok := current[key]; ok {
				if page := findFanqieBookObject(nested); page != nil {
					return page
				}
			}
		}
		keys := make([]string, 0, len(current))
		for key := range current {
			if _, ok := visited[key]; !ok {
				keys = append(keys, key)
			}
		}
		sort.Strings(keys)
		for _, key := range keys {
			if page := findFanqieBookObject(current[key]); page != nil {
				return page
			}
		}
		return nil
	}
	for _, nested := range sliceValue(value) {
		if page := findFanqieBookObject(nested); page != nil {
			return page
		}
	}
	return nil
}

func fanqieBookObjectHasTitle(book map[string]any) bool {
	for _, key := range []string{"bookName", "book_name", "name", "title"} {
		if strings.TrimSpace(stringValue(book[key])) != "" {
			return true
		}
	}
	return false
}

func fanqieJSONLDTypeIsBook(value any) bool {
	switch typed := value.(type) {
	case string:
		return strings.EqualFold(strings.TrimSpace(typed), "Book")
	case []any:
		for _, item := range typed {
			if fanqieJSONLDTypeIsBook(item) {
				return true
			}
		}
	}
	return false
}

func fanqieAuthorName(value any) string {
	if author := mapValue(value); author != nil {
		return firstNonEmptyString(stringValue(author["name"]), stringValue(author["authorName"]))
	}
	for _, item := range sliceValue(value) {
		if author := fanqieAuthorName(item); author != "" {
			return author
		}
	}
	return stringValue(value)
}

func fanqiePageTags(page map[string]any) []string {
	if tags := fanqieCategoryTags(stringValue(page["categoryV2"])); len(tags) > 0 {
		return tags
	}
	for _, key := range []string{"tags", "tagNames", "tag_names"} {
		if tags := stringSliceValue(page[key]); len(tags) > 0 {
			return normalizeBookTags(tags)
		}
	}
	for _, key := range []string{"genre", "category"} {
		if tags := stringSliceValue(page[key]); len(tags) > 0 {
			return normalizeBookTags(tags)
		}
		if tag := stringValue(page[key]); tag != "" {
			return normalizeBookTags(strings.Split(tag, ","))
		}
	}
	return nil
}

func fanqieCoverURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if strings.HasPrefix(raw, "//") {
		return "https:" + raw
	}
	if strings.HasPrefix(raw, "/") {
		return "https://fanqienovel.com" + raw
	}
	if strings.HasPrefix(raw, "http://") || strings.HasPrefix(raw, "https://") {
		return raw
	}
	return "https://p3-reading-sign.fqnovelpic.com/" + strings.TrimLeft(raw, "/")
}

func fanqiePageCover(page map[string]any) string {
	for _, key := range []string{
		"thumbUrl", "thumb_url", "detailPageThumbUrl", "detail_page_thumb_url",
		"expandThumbUrl", "expand_thumb_url", "thumbUri", "thumb_uri", "cover",
		"cover_url", "image", "images",
	} {
		if cover := firstStringValue(page[key]); cover != "" {
			return fanqieCoverURL(cover)
		}
	}
	return ""
}

func firstStringValue(value any) string {
	if values := sliceValue(value); len(values) > 0 {
		for _, item := range values {
			if text := stringValue(item); text != "" {
				return text
			}
		}
		return ""
	}
	if text := stringValue(value); text != "" {
		return text
	}
	return ""
}

func fanqieJSONLDCover(markup string) string {
	for _, match := range fanqieJSONLDRe.FindAllStringSubmatch(markup, -1) {
		if len(match) != 2 {
			continue
		}
		var data map[string]any
		if err := decodeFanqieJSONObject(match[1], &data); err != nil {
			continue
		}
		if cover := fanqiePageCover(data); cover != "" {
			return cover
		}
	}
	return ""
}

func fanqieInfoLabelTags(markup string) []string {
	doc, err := parseHTML(markup)
	if err != nil {
		return nil
	}
	tags := make([]string, 0, 8)
	for _, node := range findAll(doc, func(node *html.Node) bool {
		return node.Type == html.ElementNode && node.Data == "span" && hasClassContains(node, "info-label-grey")
	}) {
		tags = append(tags, cleanText(nodeText(node)))
	}
	return normalizeBookTags(tags)
}

func (s *FanqieNovelSite) fetchDirectory(ctx context.Context, bookID string) ([]model.Chapter, error) {
	bookID = strings.TrimSpace(bookID)
	if !fanqieBookIDRe.MatchString(bookID) {
		return nil, fmt.Errorf("fanqienovel invalid book id %q", bookID)
	}

	cachePath := s.directoryCachePath(bookID)
	if err := s.waitDirectoryTurn(ctx); err != nil {
		return nil, err
	}
	body, fetchErr := s.getJSON(
		ctx,
		s.directoryEndpoint(bookID),
		fmt.Sprintf("https://fanqienovel.com/page/%s", bookID),
	)
	if fetchErr == nil {
		if chapters, err := s.parseDirectory(body); err == nil {
			if !s.cfg.General.DisableCache {
				_ = writeAtomicSiteCache(cachePath, body)
			}
			return chapters, nil
		}
	}

	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if !s.cfg.General.DisableCache {
		if cached, err := os.ReadFile(cachePath); err == nil {
			if chapters, parseErr := s.parseDirectory(cached); parseErr == nil {
				return chapters, nil
			}
		}
	}
	if fetchErr != nil {
		return nil, fetchErr
	}
	return nil, fmt.Errorf("fanqienovel directory response could not be parsed")
}

func (s *FanqieNovelSite) directoryEndpoint(bookID string) string {
	base := strings.TrimSpace(s.directoryURL)
	if base == "" {
		base = fanqieDirectoryAPI
	}
	separator := "?"
	if strings.Contains(base, "?") {
		separator = "&"
	}
	return fmt.Sprintf("%s%sbookId=%s", base, separator, url.QueryEscape(bookID))
}

func (s *FanqieNovelSite) directoryCachePath(bookID string) string {
	root := strings.TrimSpace(s.cfg.General.CacheDir)
	if root == "" {
		root = filepath.Join(".", "data", "novel_cache")
	}
	return filepath.Join(root, s.Key(), "directories", bookID, fanqieDirectoryCacheFileName)
}

func (s *FanqieNovelSite) waitDirectoryTurn(ctx context.Context) error {
	s.directoryMu.Lock()
	defer s.directoryMu.Unlock()

	interval := s.directoryInterval
	if interval <= 0 {
		interval = fanqieDirectoryMinInterval
	}
	if !s.lastDirectoryCall.IsZero() {
		wait := interval - time.Since(s.lastDirectoryCall)
		if wait > 0 {
			if err := sleepContext(ctx, wait); err != nil {
				return err
			}
		}
	}
	s.lastDirectoryCall = time.Now()
	return nil
}

func writeAtomicSiteCache(path string, body []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }()
	if _, err := tmp.Write(body); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	renameErr := os.Rename(tmpPath, path)
	if renameErr == nil {
		return nil
	}
	// Windows does not replace an existing destination with os.Rename.
	if !os.IsExist(renameErr) {
		return renameErr
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	return nil
}

func (s *FanqieNovelSite) FetchChapter(ctx context.Context, bookID string, chapter model.Chapter) (model.Chapter, error) {
	chapter.ID = strings.TrimSpace(chapter.ID)
	if chapter.ID == "" {
		return chapter, fmt.Errorf("fanqienovel chapter id is empty")
	}
	readerURL := fanqieReaderURL + url.PathEscape(chapter.ID)
	referer := fmt.Sprintf("https://fanqienovel.com/page/%s", url.PathEscape(strings.TrimSpace(bookID)))
	markup, err := s.getHTML(ctx, readerURL, referer)
	if err != nil {
		return chapter, fmt.Errorf("load fanqienovel chapter from official reader: %w", err)
	}
	state, err := extractFanqieInitialState(markup)
	if err != nil {
		return chapter, fmt.Errorf("parse fanqienovel official reader: %w", err)
	}
	reader := mapValue(state, "reader")
	chapterData := mapValue(reader, "chapterData")
	if chapterData == nil {
		chapterData = mapValue(state, "chapterData")
	}
	if chapterData == nil {
		return chapter, fmt.Errorf("fanqienovel official reader chapter data not found")
	}
	if fanqieChapterIsLocked(chapterData) {
		return chapter, fmt.Errorf("番茄章节已锁定或需要付费，当前账号无权下载；请配置有权访问该书的 fanqienovel Cookie")
	}
	if actualBookID := stringValue(chapterData["bookId"]); actualBookID != "" && strings.TrimSpace(bookID) != "" && actualBookID != strings.TrimSpace(bookID) {
		return chapter, fmt.Errorf("fanqienovel chapter book mismatch: expected %s, got %s", bookID, actualBookID)
	}
	rawContent := stringValue(chapterData["content"])
	if strings.TrimSpace(rawContent) == "" {
		return chapter, fmt.Errorf("番茄官方阅读页未返回正文；如章节需要登录或购买，请配置有权访问该书的 Cookie")
	}
	doc, err := parseHTML(rawContent)
	if err != nil {
		return chapter, err
	}
	paragraphs := make([]string, 0)
	for _, p := range findAll(doc, func(n *html.Node) bool { return n.Type == html.ElementNode && n.Data == "p" }) {
		text := fanqieStripVoiceMarkers(cleanText(nodeText(p)))
		text = applySubstMap(text, fanqieMap)
		if text != "" {
			paragraphs = append(paragraphs, text)
		}
	}
	if len(paragraphs) == 0 {
		return chapter, fmt.Errorf("fanqienovel parsed paragraph content is empty")
	}
	if strings.TrimSpace(chapter.Title) == "" {
		chapter.Title = applySubstMap(fanqieStripVoiceMarkers(stringValue(chapterData["title"])), fanqieMap)
	}
	chapter.Content = strings.Join(paragraphs, "\n")
	chapter.URL = readerURL
	chapter.Downloaded = true
	return chapter, nil
}

func (s *FanqieNovelSite) Search(ctx context.Context, keyword string, limit int) ([]model.SearchResult, error) {
	keyword = strings.TrimSpace(keyword)
	if keyword == "" {
		return nil, fmt.Errorf("fanqienovel search keyword is empty")
	}
	if limit <= 0 {
		limit = 10
	}
	results := make([]model.SearchResult, 0, limit)
	seen := make(map[string]bool)
	const pageSize = 10
	maxPages := (limit + pageSize - 1) / pageSize
	if maxPages > 10 {
		maxPages = 10
	}
	searchURL := strings.TrimSpace(s.searchURL)
	if searchURL == "" {
		searchURL = fanqieSearchAPI
	}
	referer := "https://fanqienovel.com/search/" + url.PathEscape(keyword)
	for pageIndex := 0; pageIndex < maxPages && len(results) < limit; pageIndex++ {
		parsedEndpoint, err := url.Parse(searchURL)
		if err != nil {
			return nil, err
		}
		query := parsedEndpoint.Query()
		query.Set("query_word", keyword)
		query.Set("page_count", strconv.Itoa(pageSize))
		query.Set("page_index", strconv.Itoa(pageIndex))
		query.Set("query_type", "0")
		query.Set("filter", "127,127,127,127")
		parsedEndpoint.RawQuery = query.Encode()

		body, responseHeaders, err := s.getJSONResponse(ctx, parsedEndpoint.String(), referer, map[string]string{
			"Sec-Fetch-Site": "same-origin",
			"Sec-Fetch-Mode": "cors",
			"Sec-Fetch-Dest": "empty",
		})
		if err != nil {
			if len(results) > 0 {
				break
			}
			return nil, err
		}
		if strings.TrimSpace(string(body)) == "" {
			if responseHeaders.Get("bdturing-verify") != "" || responseHeaders.Get("x-vc-bdturing-parameters") != "" {
				if len(results) > 0 {
					break
				}
				return nil, fmt.Errorf("番茄官方搜索触发安全验证，请先在 fanqienovel.com 完成验证，并在 fanqienovel 渠道配置有效 Cookie 后重试")
			}
			if len(results) > 0 {
				break
			}
			return nil, fmt.Errorf("番茄官方搜索返回空响应，请稍后重试；如持续出现，请配置有效的 fanqienovel Cookie")
		}
		var payload map[string]any
		if err := json.Unmarshal(body, &payload); err != nil {
			if len(results) > 0 {
				break
			}
			return nil, err
		}
		code := int64Value(payload["code"])
		if code == -5 {
			return results, nil
		}
		if code != 0 {
			if len(results) > 0 {
				break
			}
			message := firstNonEmptyString(stringValue(payload["message"]), stringValue(payload["msg"]))
			if message == "" {
				message = "unknown error"
			}
			return nil, fmt.Errorf("fanqienovel official search api: %s", message)
		}
		data := mapValue(payload, "data")
		if data == nil {
			break
		}

		before := len(results)
		books := fanqieSearchBookList(data)
		for _, itemRaw := range books {
			result, ok := fanqieSearchResult(itemRaw)
			if !ok || seen[result.BookID] {
				continue
			}
			seen[result.BookID] = true
			results = append(results, result)
			if len(results) >= limit {
				return results, nil
			}
		}
		total := int64Value(data["total_count"])
		if len(results) == before || len(books) < pageSize || (total > 0 && int64((pageIndex+1)*pageSize) >= total) {
			break
		}
	}
	return results, nil
}

func fanqieSearchBookList(data map[string]any) []any {
	for _, key := range []string{
		"search_book_data_list", "searchBookList", "book_data_list", "bookDataList",
		"book_list", "bookList", "books", "list",
	} {
		if books := sliceValue(data[key]); len(books) > 0 {
			return books
		}
	}
	return nil
}

func fanqieSearchResult(value any) (model.SearchResult, bool) {
	book := mapValue(value)
	if book == nil {
		return model.SearchResult{}, false
	}
	if nested := mapValue(book["book_data"]); nested != nil {
		book = nested
	} else if nested := sliceValue(book["book_data"]); len(nested) > 0 {
		if wrapped := mapValue(nested[0]); wrapped != nil {
			book = wrapped
		}
	}
	bookID := firstNonEmptyString(
		stringValue(book["book_id"]),
		stringValue(book["bookId"]),
		stringValue(book["book_id_str"]),
		stringValue(book["id"]),
	)
	title := firstNonEmptyString(
		stringValue(book["book_name"]),
		stringValue(book["bookName"]),
		stringValue(book["raw_book_name"]),
		stringValue(book["title"]),
		stringValue(book["name"]),
	)
	if bookID == "" || title == "" {
		return model.SearchResult{}, false
	}
	author := firstNonEmptyString(
		fanqieAuthorName(book["author"]),
		stringValue(book["author_name"]),
		stringValue(book["authorName"]),
	)
	description := firstNonEmptyString(
		stringValue(book["book_abstract"]),
		stringValue(book["bookAbstract"]),
		stringValue(book["abstract"]),
		stringValue(book["book_abstract_v2"]),
		stringValue(book["description"]),
	)
	latestChapter := firstNonEmptyString(
		stringValue(book["last_chapter_title"]),
		stringValue(book["lastChapterTitle"]),
	)
	return model.SearchResult{
		Site:          "fanqienovel",
		BookID:        bookID,
		Title:         applySubstMap(title, fanqieMap),
		Author:        applySubstMap(author, fanqieMap),
		Description:   applySubstMap(description, fanqieMap),
		URL:           fmt.Sprintf("https://fanqienovel.com/page/%s", bookID),
		LatestChapter: applySubstMap(latestChapter, fanqieMap),
		CoverURL:      fanqiePageCover(book),
	}, true
}

func fanqieChapterIsLocked(chapterData map[string]any) bool {
	for _, key := range []string{"isChapterLock", "is_chapter_lock"} {
		if raw, ok := chapterData[key]; ok && fanqieValueIsTruthy(raw) {
			return true
		}
	}
	if raw, ok := chapterData["needPay"]; ok && fanqieValueIsTruthy(raw) {
		return true
	}
	if raw, ok := chapterData["need_pay"]; ok && fanqieValueIsTruthy(raw) {
		return true
	}
	return false
}

func fanqieValueIsTruthy(value any) bool {
	switch typed := value.(type) {
	case bool:
		return typed
	case string:
		switch strings.ToLower(strings.TrimSpace(typed)) {
		case "true", "1", "yes":
			return true
		default:
			return false
		}
	default:
		return int64Value(value) != 0
	}
}

func (s *FanqieNovelSite) parseDirectory(body []byte) ([]model.Chapter, error) {
	var root map[string]any
	if err := decodeFanqieJSONObject(string(body), &root); err != nil {
		return nil, err
	}
	if code := int64Value(root["code"]); code != 0 && code != 200 {
		message := firstNonEmptyString(stringValue(root["message"]), stringValue(root["errMsg"]))
		if message == "" {
			message = "unknown error"
		}
		return nil, fmt.Errorf("fanqienovel directory api: %s", message)
	}

	data := mapValue(root, "data")
	if data == nil {
		data = root
	}
	if nested := mapValue(data, "data"); nested != nil {
		data = nested
	}
	volumeNames := stringSliceValue(data["volumeNameList"])
	if len(volumeNames) == 0 {
		volumeNames = stringSliceValue(data["volume_name_list"])
	}

	groups := sliceValue(data["chapterListWithVolume"])
	flatKeys := []string{"chapterList", "chapter_list", "chapters", "item_list", "items", "list"}
	if len(groups) == 0 {
		for _, key := range flatKeys {
			if items := sliceValue(data[key]); len(items) > 0 {
				groups = []any{items}
				break
			}
		}
	}
	if len(groups) == 0 {
		if items := findFanqieChapterArray(data); len(items) > 0 {
			groups = []any{items}
		}
	}
	if len(groups) == 0 {
		return nil, fmt.Errorf("fanqienovel directory chapter list not found")
	}

	chapters := make([]model.Chapter, 0)
	seen := make(map[string]struct{})
	sequence := 0
	for volIdx, groupRaw := range groups {
		volumeName := fmt.Sprintf("第%d卷", volIdx+1)
		if volIdx < len(volumeNames) && strings.TrimSpace(volumeNames[volIdx]) != "" {
			volumeName = strings.TrimSpace(volumeNames[volIdx])
		}
		for _, itemRaw := range sliceValue(groupRaw) {
			item := mapValue(itemRaw)
			if item == nil {
				continue
			}
			itemID := firstNonEmptyString(
				stringValue(item["itemId"]),
				stringValue(item["item_id"]),
				stringValue(item["chapterId"]),
				stringValue(item["chapter_id"]),
				stringValue(item["catalogId"]),
				stringValue(item["catalog_id"]),
				stringValue(item["id"]),
			)
			if itemID == "" {
				continue
			}
			if _, exists := seen[itemID]; exists {
				continue
			}
			seen[itemID] = struct{}{}
			sequence++
			order := fanqieChapterNumber(firstNonEmptyString(
				stringValue(item["realChapterOrder"]),
				stringValue(item["real_chapter_order"]),
				stringValue(item["chapterOrder"]),
				stringValue(item["chapter_order"]),
			))
			if order <= 0 {
				order = sequence
			}
			title := firstNonEmptyString(
				stringValue(item["title"]),
				stringValue(item["chapterTitle"]),
				stringValue(item["chapter_title"]),
				stringValue(item["chapterName"]),
				stringValue(item["chapter_name"]),
				stringValue(item["name"]),
			)
			if title == "" {
				title = fmt.Sprintf("第%d章", sequence)
			}
			itemVolume := firstNonEmptyString(stringValue(item["volumeName"]), stringValue(item["volume_name"]))
			if itemVolume == "" {
				itemVolume = volumeName
			}
			chapters = append(chapters, model.Chapter{
				ID:     itemID,
				Title:  applySubstMap(title, fanqieMap),
				URL:    fmt.Sprintf("https://fanqienovel.com/reader/%s", itemID),
				Volume: applySubstMap(itemVolume, fanqieMap),
				Order:  order,
			})
		}
	}
	if len(chapters) == 0 {
		return nil, fmt.Errorf("fanqienovel directory chapter list is empty")
	}
	sort.SliceStable(chapters, func(i, j int) bool {
		return chapters[i].Order < chapters[j].Order
	})
	return chapters, nil
}

func findFanqieChapterArray(value any) []any {
	var best []any
	var walk func(any)
	walk = func(current any) {
		if items := sliceValue(current); len(items) > 0 {
			if len(items) > len(best) && fanqieChapterItems(items) {
				best = items
			}
			for _, item := range items {
				walk(item)
			}
			return
		}
		for _, item := range mapValue(current) {
			walk(item)
		}
	}
	walk(value)
	return best
}

func fanqieChapterItems(items []any) bool {
	for _, item := range items {
		chapter := mapValue(item)
		if chapter == nil {
			continue
		}
		for _, key := range []string{"itemId", "item_id", "chapterId", "chapter_id", "catalogId", "catalog_id", "id"} {
			if stringValue(chapter[key]) != "" {
				return true
			}
		}
	}
	return false
}

func fanqieChapterNumber(raw string) int {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return 0
	}
	return n
}

func (s *FanqieNovelSite) getJSON(ctx context.Context, rawURL, referer string) ([]byte, error) {
	body, _, err := s.getJSONResponse(ctx, rawURL, referer, nil)
	return body, err
}

func (s *FanqieNovelSite) getJSONResponse(ctx context.Context, rawURL, referer string, extraHeaders map[string]string) ([]byte, http.Header, error) {
	var lastErr error
	backoff := 400 * time.Millisecond
	for attempt := 0; attempt < 4; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
		if err != nil {
			return nil, nil, err
		}
		req.Header.Set("User-Agent", defaultBrowserUserAgent)
		req.Header.Set("Accept", "application/json, text/plain, */*")
		req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9")
		if referer != "" {
			req.Header.Set("Referer", referer)
		}
		for key, value := range extraHeaders {
			req.Header.Set(key, value)
		}
		resp, err := s.client.Do(req)
		if err != nil {
			lastErr = err
			if err := sleepContext(ctx, backoff); err != nil {
				return nil, nil, err
			}
			backoff *= 2
			continue
		}
		status := resp.StatusCode
		responseHeaders := resp.Header.Clone()
		body, readErr := io.ReadAll(resp.Body)
		resp.Body.Close()
		if readErr != nil {
			lastErr = readErr
			if err := sleepContext(ctx, backoff); err != nil {
				return nil, nil, err
			}
			backoff *= 2
			continue
		}
		if status == 403 {
			if referer != "" && attempt == 0 && s.html.client != nil {
				_, _ = s.html.Get(ctx, referer)
			}
			lastErr = fmt.Errorf("http %d for %s", status, rawURL)
			if err := sleepContext(ctx, backoff); err != nil {
				return nil, nil, err
			}
			backoff *= 2
			continue
		}
		if status == http.StatusTooManyRequests || status == http.StatusRequestTimeout || status >= 500 {
			lastErr = fmt.Errorf("http %d for %s", status, rawURL)
			if err := sleepContext(ctx, backoff); err != nil {
				return nil, nil, err
			}
			backoff *= 2
			continue
		}
		if status < 200 || status >= 300 {
			return nil, responseHeaders, fmt.Errorf("http %d for %s", status, rawURL)
		}
		return body, responseHeaders, nil
	}
	return nil, nil, lastErr
}

func (s *FanqieNovelSite) getHTML(ctx context.Context, rawURL, referer string) (string, error) {
	var lastErr error
	backoff := 500 * time.Millisecond
	for attempt := 0; attempt < 4; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
		if err != nil {
			return "", err
		}
		req.Header.Set("User-Agent", defaultBrowserUserAgent)
		req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,*/*;q=0.8")
		req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9,en;q=0.8")
		req.Header.Set("Cache-Control", "no-cache")
		req.Header.Set("Upgrade-Insecure-Requests", "1")
		if referer != "" {
			req.Header.Set("Referer", referer)
		}
		resp, err := s.client.Do(req)
		if err != nil {
			lastErr = err
			if err := sleepContext(ctx, backoff); err != nil {
				return "", err
			}
			backoff *= 2
			continue
		}
		status := resp.StatusCode
		contentType := resp.Header.Get("Content-Type")
		body, readErr := io.ReadAll(resp.Body)
		resp.Body.Close()
		if readErr != nil {
			lastErr = readErr
			if err := sleepContext(ctx, backoff); err != nil {
				return "", err
			}
			backoff *= 2
			continue
		}
		if status == 403 && referer != "" {
			// Re-fetch the book page to refresh anti-bot cookies, then retry
			// after backoff. Some 403 responses are transient WAF blocks.
			_, _ = s.html.Get(ctx, referer)
			lastErr = fmt.Errorf("http %d for %s", status, rawURL)
			if err := sleepContext(ctx, backoff); err != nil {
				return "", err
			}
			backoff *= 2
			continue
		}
		if status < 200 || status >= 300 {
			return "", fmt.Errorf("http %d for %s", status, rawURL)
		}
		reader, err := charsetpkg.NewReader(bytes.NewReader(body), contentType)
		if err == nil {
			if decoded, derr := io.ReadAll(reader); derr == nil {
				return string(decoded), nil
			}
		}
		return string(body), nil
	}
	return "", lastErr
}

func extractFanqieInitialState(markup string) (map[string]any, error) {
	match := fanqieInitialStateRe.FindStringSubmatch(markup)
	if len(match) != 2 {
		return nil, fmt.Errorf("fanqienovel initial state not found")
	}
	tokens := tokenizeJSObject(match[1])
	value, next, err := parseJSValue(tokens, 0)
	if err != nil {
		return nil, err
	}
	if next == 0 {
		return nil, fmt.Errorf("fanqienovel initial state parser consumed nothing")
	}
	result, ok := value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("fanqienovel initial state is not an object")
	}
	return result, nil
}

func tokenizeJSObject(src string) []string {
	toks := make([]string, 0)
	for i := 0; i < len(src); {
		ch := src[i]
		if strings.ContainsRune(" \t\r\n", rune(ch)) {
			i++
			continue
		}
		if ch == '\'' || ch == '"' {
			j := i + 1
			esc := false
			for j < len(src) {
				c := src[j]
				if esc {
					esc = false
				} else if c == '\\' {
					esc = true
				} else if c == ch {
					j++
					break
				}
				j++
			}
			toks = append(toks, src[i:j])
			i = j
			continue
		}
		if ch == '/' && i+1 < len(src) && (src[i+1] == '/' || src[i+1] == '*') {
			if src[i+1] == '/' {
				i += 2
				for i < len(src) && src[i] != '\n' && src[i] != '\r' {
					i++
				}
			} else {
				i += 2
				for i+1 < len(src) && !(src[i] == '*' && src[i+1] == '/') {
					i++
				}
				i += 2
			}
			continue
		}
		if strings.ContainsRune("{}[]:,", rune(ch)) {
			toks = append(toks, src[i:i+1])
			i++
			continue
		}
		j := i
		for j < len(src) && !strings.ContainsRune(" \t\r\n{}[]:,", rune(src[j])) {
			j++
		}
		toks = append(toks, src[i:j])
		i = j
	}
	return toks
}

func parseJSValue(tokens []string, idx int) (any, int, error) {
	if idx >= len(tokens) {
		return nil, idx, fmt.Errorf("unexpected end of tokens")
	}
	tok := tokens[idx]
	if tok == "{" {
		obj := map[string]any{}
		idx++
		for idx < len(tokens) && tokens[idx] != "}" {
			key := tokens[idx]
			if strings.HasPrefix(key, "\"") || strings.HasPrefix(key, "'") {
				parsed, err := parseJSString(key)
				if err != nil {
					return nil, idx, err
				}
				key = parsed
			}
			idx++
			if idx >= len(tokens) || tokens[idx] != ":" {
				return nil, idx, fmt.Errorf("expected colon in object")
			}
			idx++
			val, next, err := parseJSValue(tokens, idx)
			if err != nil {
				return nil, next, err
			}
			obj[key] = val
			idx = next
			if idx < len(tokens) && tokens[idx] == "," {
				idx++
			}
		}
		if idx >= len(tokens) || tokens[idx] != "}" {
			return nil, idx, fmt.Errorf("unterminated object")
		}
		return obj, idx + 1, nil
	}
	if tok == "[" {
		arr := make([]any, 0)
		idx++
		for idx < len(tokens) && tokens[idx] != "]" {
			val, next, err := parseJSValue(tokens, idx)
			if err != nil {
				return nil, next, err
			}
			arr = append(arr, val)
			idx = next
			if idx < len(tokens) && tokens[idx] == "," {
				idx++
			}
		}
		if idx >= len(tokens) || tokens[idx] != "]" {
			return nil, idx, fmt.Errorf("unterminated array")
		}
		return arr, idx + 1, nil
	}
	val, err := parseJSToken(tok)
	return val, idx + 1, err
}

func parseJSToken(tok string) (any, error) {
	tok = strings.TrimSpace(tok)
	switch tok {
	case "null", "undefined":
		return nil, nil
	case "true":
		return true, nil
	case "false":
		return false, nil
	}
	if strings.HasPrefix(tok, "\"") || strings.HasPrefix(tok, "'") {
		return parseJSString(tok)
	}
	if i, err := strconv.ParseInt(tok, 10, 64); err == nil {
		return i, nil
	}
	if f, err := strconv.ParseFloat(tok, 64); err == nil {
		return f, nil
	}
	return tok, nil
}

func parseJSString(s string) (string, error) {
	if len(s) < 2 || s[0] != s[len(s)-1] {
		return "", fmt.Errorf("invalid JS string literal")
	}
	body := s[1 : len(s)-1]
	if !strings.Contains(body, "\\") {
		return body, nil
	}
	var b strings.Builder
	for i := 0; i < len(body); i++ {
		if body[i] != '\\' {
			b.WriteByte(body[i])
			continue
		}
		i++
		if i >= len(body) {
			break
		}
		switch body[i] {
		case '\'', '"', '\\':
			b.WriteByte(body[i])
		case 'n':
			b.WriteByte('\n')
		case 'r':
			b.WriteByte('\r')
		case 't':
			b.WriteByte('\t')
		case 'b':
			b.WriteByte('\b')
		case 'f':
			b.WriteByte('\f')
		case 'v':
			b.WriteByte('\v')
		case '0':
			b.WriteByte(0)
		case 'x':
			if i+2 >= len(body) {
				return "", fmt.Errorf("invalid hex escape")
			}
			v, err := strconv.ParseInt(body[i+1:i+3], 16, 32)
			if err != nil {
				return "", err
			}
			b.WriteRune(rune(v))
			i += 2
		case 'u':
			if i+4 >= len(body) {
				return "", fmt.Errorf("invalid unicode escape")
			}
			v, err := strconv.ParseInt(body[i+1:i+5], 16, 32)
			if err != nil {
				return "", err
			}
			b.WriteRune(rune(v))
			i += 4
		default:
			b.WriteByte(body[i])
		}
	}
	return b.String(), nil
}

func fanqieCategoryTags(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	var data []map[string]any
	if err := json.Unmarshal([]byte(raw), &data); err != nil {
		return nil
	}
	tags := make([]string, 0, len(data))
	for _, item := range data {
		if name := stringValue(item["Name"]); name != "" {
			tags = append(tags, name)
		}
	}
	return tags
}

var fanqieVoiceMarkerRe = regexp.MustCompile(`\{!--\s*PGC_VOICE:.*?--\}`)

func fanqieStripVoiceMarkers(text string) string {
	return strings.TrimSpace(fanqieVoiceMarkerRe.ReplaceAllString(text, ""))
}

func mapValue(value any, keys ...string) map[string]any {
	if len(keys) > 0 {
		current, ok := value.(map[string]any)
		if !ok {
			return nil
		}
		for _, key := range keys {
			value = current[key]
			current, ok = value.(map[string]any)
			if !ok {
				return nil
			}
		}
		return current
	}
	current, _ := value.(map[string]any)
	return current
}

func sliceValue(value any) []any {
	slice, _ := value.([]any)
	return slice
}

func stringSliceValue(value any) []string {
	items := sliceValue(value)
	if len(items) == 0 {
		return nil
	}
	result := make([]string, 0, len(items))
	for _, item := range items {
		if text := stringValue(item); text != "" {
			result = append(result, text)
		}
	}
	return result
}

func stringValue(value any) string {
	switch v := value.(type) {
	case string:
		return strings.TrimSpace(v)
	case fmt.Stringer:
		return strings.TrimSpace(v.String())
	case int64:
		return strconv.FormatInt(v, 10)
	case float64:
		return strconv.FormatInt(int64(v), 10)
	case json.Number:
		return v.String()
	default:
		if value == nil {
			return ""
		}
		return strings.TrimSpace(fmt.Sprintf("%v", value))
	}
}

func boolValue(value any) bool {
	switch v := value.(type) {
	case bool:
		return v
	case string:
		return v == "true" || v == "1"
	case float64:
		return v != 0
	case int64:
		return v != 0
	default:
		return false
	}
}

func int64Value(value any) int64 {
	switch v := value.(type) {
	case int:
		return int64(v)
	case int64:
		return v
	case float64:
		return int64(v)
	case json.Number:
		i, _ := v.Int64()
		return i
	case string:
		i, _ := strconv.ParseInt(v, 10, 64)
		return i
	default:
		return 0
	}
}
