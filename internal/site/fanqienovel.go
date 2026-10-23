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
	fanqieSearchAPI              = "http://101.35.133.34:5000/api/search"
	fanqieChapterAPI             = "http://101.35.133.34:5000/api/raw_full"
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
	return &FanqieNovelSite{
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
}

func (s *FanqieNovelSite) Key() string         { return "fanqienovel" }
func (s *FanqieNovelSite) DisplayName() string { return "FanqieNovel" }
func (s *FanqieNovelSite) Capabilities() Capabilities {
	return Capabilities{Download: true, Search: true, Login: false}
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
	_ = bookID
	body, err := s.getJSON(ctx, fmt.Sprintf("%s?item_id=%s", fanqieChapterAPI, chapter.ID), "")
	if err != nil {
		return chapter, err
	}
	var payload struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Data    struct {
			Content string `json:"content"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return chapter, err
	}
	if payload.Code != 200 {
		return chapter, fmt.Errorf("fanqienovel chapter api: %s", payload.Message)
	}
	rawContent := payload.Data.Content
	if strings.TrimSpace(rawContent) == "" {
		return chapter, fmt.Errorf("fanqienovel chapter content not found")
	}
	doc, err := parseHTML(rawContent)
	if err != nil {
		return chapter, err
	}
	paragraphs := make([]string, 0)
	for _, p := range findAll(doc, func(n *html.Node) bool { return n.Type == html.ElementNode && n.Data == "p" }) {
		text := cleanText(nodeText(p))
		text = fanqieStripVoiceMarkers(text)
		if text != "" {
			paragraphs = append(paragraphs, text)
		}
	}
	if len(paragraphs) == 0 {
		return chapter, fmt.Errorf("fanqienovel parsed paragraph content is empty")
	}
	chapter.Content = strings.Join(paragraphs, "\n")
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

	// The official web search endpoint is protected by a slide captcha that
	// cannot be solved server-side. This mirrors the public helper service used
	// by the reference fanqienovel-downloader project. The helper treats its own
	// result as a fixed-size page, so walk the offsets until we either satisfy
	// the requested limit or the helper reports there are no more books.
	results := make([]model.SearchResult, 0, limit)
	seen := make(map[string]bool)
	const helperPageSize = 10
	const maxPages = 12
	for page := 1; page <= maxPages && len(results) < limit; page++ {
		if ctx.Err() != nil {
			break
		}
		offset := (page - 1) * helperPageSize
		endpoint := s.searchURL + "?key=" + url.QueryEscape(keyword) + "&offset=" + strconv.Itoa(offset)
		body, err := s.getJSON(ctx, endpoint, "")
		if err != nil {
			if len(results) > 0 {
				break
			}
			return nil, err
		}
		var payload map[string]any
		if err := json.Unmarshal(body, &payload); err != nil {
			if len(results) > 0 {
				break
			}
			return nil, err
		}
		if int64Value(payload["code"]) != 200 {
			if len(results) > 0 {
				break
			}
			return nil, fmt.Errorf("fanqienovel search api: %s", stringValue(payload["message"]))
		}
		data := mapValue(payload, "data")
		if data == nil {
			break
		}

		before := len(results)
		hasMore := false
		for _, tabRaw := range sliceValue(data["search_tabs"]) {
			tab := mapValue(tabRaw)
			tabData := sliceValue(tab["data"])
			if len(tabData) == 0 {
				continue
			}
			if more, ok := tab["has_more"].(bool); ok {
				hasMore = more
			}
			for _, itemRaw := range tabData {
				item := mapValue(itemRaw)
				books := sliceValue(item["book_data"])
				if len(books) == 0 {
					continue
				}
				book := mapValue(books[0])
				bookID := stringValue(book["book_id"])
				title := stringValue(book["book_name"])
				if title == "" {
					title = stringValue(book["raw_book_name"])
				}
				if bookID == "" || title == "" || seen[bookID] {
					continue
				}
				seen[bookID] = true
				author := stringValue(book["author"])
				description := stringValue(book["abstract"])
				if description == "" {
					description = stringValue(book["book_abstract_v2"])
				}
				cover := stringValue(book["thumb_url"])
				if cover == "" {
					cover = stringValue(book["horiz_thumb_url"])
				}
				results = append(results, model.SearchResult{
					Site:          s.Key(),
					BookID:        bookID,
					Title:         applySubstMap(title, fanqieMap),
					Author:        applySubstMap(author, fanqieMap),
					Description:   applySubstMap(description, fanqieMap),
					URL:           fmt.Sprintf("https://fanqienovel.com/page/%s", bookID),
					LatestChapter: applySubstMap(stringValue(book["last_chapter_title"]), fanqieMap),
					CoverURL:      cover,
				})
				if len(results) >= limit {
					return results, nil
				}
			}
			// The first tab that carries results is the primary book list; the
			// remaining tabs are media/community views without novels.
			break
		}
		if len(results) == before || !hasMore {
			break
		}
	}
	return results, nil
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
	var lastErr error
	backoff := 400 * time.Millisecond
	for attempt := 0; attempt < 4; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("User-Agent", defaultBrowserUserAgent)
		req.Header.Set("Accept", "application/json, text/plain, */*")
		req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9")
		if referer != "" {
			req.Header.Set("Referer", referer)
		}
		resp, err := s.client.Do(req)
		if err != nil {
			lastErr = err
			if err := sleepContext(ctx, backoff); err != nil {
				return nil, err
			}
			backoff *= 2
			continue
		}
		status := resp.StatusCode
		body, readErr := io.ReadAll(resp.Body)
		resp.Body.Close()
		if readErr != nil {
			lastErr = readErr
			if err := sleepContext(ctx, backoff); err != nil {
				return nil, err
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
				return nil, err
			}
			backoff *= 2
			continue
		}
		if status == http.StatusTooManyRequests || status == http.StatusRequestTimeout || status >= 500 {
			lastErr = fmt.Errorf("http %d for %s", status, rawURL)
			if err := sleepContext(ctx, backoff); err != nil {
				return nil, err
			}
			backoff *= 2
			continue
		}
		if status < 200 || status >= 300 {
			return nil, fmt.Errorf("http %d for %s", status, rawURL)
		}
		return body, nil
	}
	return nil, lastErr
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
