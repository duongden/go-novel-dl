package site

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"

	"golang.org/x/net/html"

	"github.com/guohuiyuan/go-novel-dl/internal/config"
	"github.com/guohuiyuan/go-novel-dl/internal/model"
)

const qqReaderBaseURL = "https://book.qq.com"

var (
	qqReaderChapterRe             = regexp.MustCompile(`^/book-read/(\d+)/(\d+)/?$`)
	qqReaderBookRe                = regexp.MustCompile(`^/book-(?:detail|chapter)/(\d+)/?$`)
	errQQReaderSearchNoExactMatch = errors.New("qqreader search keyword is not in the searchable keyword index")
)

type QQReaderSite struct {
	cfg    config.ResolvedSiteConfig
	client *http.Client
}

type qqReaderSearchEnvelope struct {
	Code int                `json:"code"`
	Msg  string             `json:"msg"`
	Data qqReaderSearchData `json:"data"`
}

type qqReaderSearchData struct {
	PageNo   int                      `json:"pageNo"`
	PageSize int                      `json:"pageSize"`
	Totals   int                      `json:"totals"`
	Pages    int                      `json:"pages"`
	HasNext  bool                     `json:"hasNext"`
	BookList []qqReaderSearchBookData `json:"bookList"`
}

type qqReaderSearchBookData struct {
	BookID       int64  `json:"bid"`
	Title        string `json:"title"`
	Author       string `json:"author"`
	Intro        string `json:"intro"`
	Category3    string `json:"category3Name"`
	Finished     int    `json:"finished"`
	TotalWords   int64  `json:"totalWords"`
	Free         int    `json:"free"`
	ReadingCount int64  `json:"readingNum"`
}

type qqReaderDetailEnvelope struct {
	Code int                `json:"code"`
	Msg  string             `json:"msg"`
	Data qqReaderDetailData `json:"data"`
}

type qqReaderDetailData struct {
	BookID         int64  `json:"bid"`
	Title          string `json:"title"`
	Author         string `json:"author"`
	Intro          string `json:"intro"`
	Category2      string `json:"category2Name"`
	Category3      string `json:"category3Name"`
	Finished       int    `json:"finished"`
	TotalWords     int64  `json:"totalWords"`
	TotalChapters  int    `json:"totalChapters"`
	Score          string `json:"score"`
	UpdateTime     string `json:"updateTime"`
	Brand          string `json:"brand"`
	Publisher      string `json:"publisher"`
	Cover          string `json:"cover"`
	FirstChapterID int64  `json:"firstChapterId"`
}

type qqReaderCatalogEnvelope struct {
	Code int                   `json:"code"`
	Msg  string                `json:"msg"`
	Data []qqReaderCatalogItem `json:"data"`
}

type qqReaderCatalogItem struct {
	ChapterID   int64  `json:"cid"`
	ChapterName string `json:"chapterName"`
	Free        int    `json:"free"`
	Purchased   int    `json:"purchased"`
	PublishTime string `json:"publishTime"`
}

type qqReaderReadEnvelope struct {
	Code int              `json:"code"`
	Msg  string           `json:"msg"`
	Data qqReaderReadData `json:"data"`
}

type qqReaderReadData struct {
	LoginStatus   int    `json:"loginStatus"`
	ChapterID     int64  `json:"ccid"`
	ChapterTitle  string `json:"chapterTitle"`
	AuthStatus    int    `json:"authStatus"`
	Content       string `json:"content"`
	ChapterVIP    int    `json:"chapterVipFlag"`
	Encrypt       bool   `json:"encrypt"`
	FontEncrypted bool   `json:"fontEncrypt"`
}

func NewQQReaderSite(cfg config.ResolvedSiteConfig) *QQReaderSite {
	timeout := 15 * time.Second
	if cfg.General.Timeout > 0 {
		timeout = time.Duration(cfg.General.Timeout * float64(time.Second))
	}
	jar, _ := cookiejar.New(nil)
	client := newSiteHTTPClient(timeout, siteHTTPClientOptions{Jar: jar})
	site := &QQReaderSite{cfg: cfg, client: client}
	site.injectCookieString(cfg.Cookie)
	return site
}

func (s *QQReaderSite) Key() string { return "qqreader" }

func (s *QQReaderSite) DisplayName() string { return "QQ阅读" }

func (s *QQReaderSite) Capabilities() Capabilities {
	return Capabilities{Download: true, Search: true, Login: false}
}

func (s *QQReaderSite) ResolveURL(rawURL string) (*ResolvedURL, bool) {
	parsed, err := normalizeURL(rawURL)
	if err != nil || parsed == nil {
		return nil, false
	}
	host := strings.ToLower(strings.TrimPrefix(parsed.Hostname(), "www."))
	if host != "book.qq.com" && host != "ubook.reader.qq.com" {
		return nil, false
	}
	if match := qqReaderChapterRe.FindStringSubmatch(parsed.Path); len(match) == 3 {
		return &ResolvedURL{
			SiteKey:   s.Key(),
			BookID:    match[1],
			ChapterID: match[2],
			Canonical: qqReaderChapterURL(match[1], match[2]),
		}, true
	}
	if match := qqReaderBookRe.FindStringSubmatch(parsed.Path); len(match) == 2 {
		return &ResolvedURL{
			SiteKey:   s.Key(),
			BookID:    match[1],
			Canonical: qqReaderBookURL(match[1]),
		}, true
	}
	return nil, false
}

func (s *QQReaderSite) Search(ctx context.Context, keyword string, limit int) ([]model.SearchResult, error) {
	keyword = strings.TrimSpace(keyword)
	if keyword == "" {
		return nil, nil
	}
	pageSize := 20
	if limit > 0 && limit < pageSize {
		pageSize = limit
	}
	if pageSize < 1 {
		pageSize = 1
	}

	results := make([]model.SearchResult, 0, pageSize)
	seen := make(map[string]struct{}, pageSize)
	for pageNo := 1; ; pageNo++ {
		payload, err := s.fetchSearchPage(ctx, keyword, pageNo, pageSize)
		if err != nil {
			if errors.Is(err, errQQReaderSearchNoExactMatch) {
				return s.searchWithFallback(ctx, keyword, limit)
			}
			// Search can return a valid first page and then fail while advancing.
			// Keep the books already found instead of reporting the whole source
			// as unavailable.
			if len(results) > 0 {
				return results, nil
			}
			return nil, err
		}
		for _, item := range payload.Data.BookList {
			result, ok := qqReaderSearchResult(item)
			if !ok {
				continue
			}
			if _, exists := seen[result.BookID]; exists {
				continue
			}
			seen[result.BookID] = struct{}{}
			results = append(results, result)
			if limit > 0 && len(results) >= limit {
				return results, nil
			}
		}
		if !payload.Data.HasNext || len(payload.Data.BookList) == 0 {
			break
		}
	}
	if len(results) == 0 {
		return s.searchWithFallback(ctx, keyword, limit)
	}
	return results, nil
}

// QQ阅读的关键词库偶尔只收录书名中的部分词，并且不保证词序。
// 仅在原始关键词完全未命中时尝试拆词后的反向组合，避免正常搜索多发请求。
func (s *QQReaderSite) searchWithFallback(ctx context.Context, keyword string, limit int) ([]model.SearchResult, error) {
	candidates := qqReaderFallbackKeywords(keyword)
	for _, candidate := range candidates {
		results, err := s.searchFallbackCandidate(ctx, candidate, keyword, limit)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			continue
		}
		if len(results) > 0 {
			return results, nil
		}
	}
	return []model.SearchResult{}, nil
}

func (s *QQReaderSite) searchFallbackCandidate(ctx context.Context, candidate, keyword string, limit int) ([]model.SearchResult, error) {
	const pageSize = 100
	wantedTitle := qqReaderSearchText(keyword)
	if wantedTitle == "" {
		return nil, nil
	}

	results := make([]model.SearchResult, 0, 1)
	seen := make(map[string]struct{})
	for pageNo := 1; ; pageNo++ {
		payload, err := s.fetchSearchPage(ctx, candidate, pageNo, pageSize)
		if err != nil {
			if errors.Is(err, errQQReaderSearchNoExactMatch) {
				return nil, nil
			}
			return nil, err
		}
		for _, item := range payload.Data.BookList {
			if qqReaderSearchText(item.Title) != wantedTitle {
				continue
			}
			result, ok := qqReaderSearchResult(item)
			if !ok {
				continue
			}
			if _, exists := seen[result.BookID]; exists {
				continue
			}
			seen[result.BookID] = struct{}{}
			results = append(results, result)
			if limit > 0 && len(results) >= limit {
				return results, nil
			}
		}
		if !payload.Data.HasNext || len(payload.Data.BookList) == 0 {
			break
		}
	}
	return results, nil
}

func qqReaderFallbackKeywords(keyword string) []string {
	normalized := qqReaderSearchText(keyword)
	runes := []rune(normalized)
	if len(runes) < 4 {
		return nil
	}

	const separators = "我你他她它被把让给和与及在是的有就都也又还很太更最第们了着过吗呢吧啊呀么"
	fragments := make([]string, 0, 4)
	start := 0
	for idx, current := range runes {
		if !strings.ContainsRune(separators, current) {
			continue
		}
		if idx > start {
			fragments = append(fragments, string(runes[start:idx]))
		}
		start = idx + 1
	}
	if start < len(runes) {
		fragments = append(fragments, string(runes[start:]))
	}
	if len(fragments) < 2 {
		return nil
	}

	candidates := make([]string, 0, len(fragments)+1)
	seen := make(map[string]struct{}, len(fragments)+1)
	appendCandidate := func(raw string) {
		raw = strings.TrimSpace(raw)
		if raw == "" || raw == normalized || len([]rune(raw)) < 2 {
			return
		}
		if _, exists := seen[raw]; exists {
			return
		}
		seen[raw] = struct{}{}
		candidates = append(candidates, raw)
	}

	reversed := make([]string, 0, len(fragments))
	for idx := len(fragments) - 1; idx >= 0; idx-- {
		reversed = append(reversed, fragments[idx])
	}
	appendCandidate(strings.Join(reversed, ""))
	for _, fragment := range fragments {
		appendCandidate(fragment)
	}
	return candidates
}

func qqReaderSearchText(raw string) string {
	return strings.Map(func(current rune) rune {
		if unicode.IsLetter(current) || unicode.IsNumber(current) {
			return unicode.ToLower(current)
		}
		return -1
	}, strings.TrimSpace(raw))
}

func qqReaderSearchResult(item qqReaderSearchBookData) (model.SearchResult, bool) {
	if item.BookID <= 0 {
		return model.SearchResult{}, false
	}
	bookID := strconv.FormatInt(item.BookID, 10)
	return model.SearchResult{
		Site:        "qqreader",
		BookID:      bookID,
		Title:       strings.TrimSpace(item.Title),
		Author:      strings.TrimSpace(item.Author),
		Description: strings.TrimSpace(item.Intro),
		URL:         qqReaderBookURL(bookID),
		CoverURL:    qqReaderCoverURL(bookID),
	}, true
}

func (s *QQReaderSite) DownloadPlan(ctx context.Context, ref model.BookRef) (*model.Book, error) {
	bookID := strings.TrimSpace(ref.BookID)
	if bookID == "" {
		return nil, fmt.Errorf("qqreader book id is required")
	}
	detail, err := s.fetchBookDetail(ctx, bookID)
	if err != nil {
		return nil, err
	}
	catalog, err := s.fetchCatalog(ctx, bookID)
	if err != nil {
		return nil, err
	}

	includeLocked := s.cfg.General.FetchInaccessible || strings.TrimSpace(s.cfg.Cookie) != ""
	chapters := make([]model.Chapter, 0, len(catalog.Data))
	for _, item := range catalog.Data {
		if item.ChapterID <= 0 {
			continue
		}
		if qqReaderImageOnlyChapter(item.ChapterName) {
			continue
		}
		if !includeLocked && item.Free != 1 && item.Purchased != 1 {
			continue
		}
		chapterID := strconv.FormatInt(item.ChapterID, 10)
		chapters = append(chapters, model.Chapter{
			ID:    chapterID,
			Title: strings.TrimSpace(item.ChapterName),
			URL:   qqReaderChapterURL(bookID, chapterID),
			Order: len(chapters) + 1,
		})
	}
	if len(chapters) == 0 {
		if len(catalog.Data) > 0 && !includeLocked {
			return nil, fmt.Errorf("qqreader book %s has no free or purchased chapters; configure a QQ cookie with access or enable fetch_inaccessible", bookID)
		}
		return nil, fmt.Errorf("qqreader chapter catalog is empty for book %s", bookID)
	}

	book := qqReaderBookFromDetail(detail.Data, bookID)
	book.Chapters = applyChapterRange(dedupChapters(chapters), ref)
	return book, nil
}

func (s *QQReaderSite) FetchChapter(ctx context.Context, bookID string, chapter model.Chapter) (model.Chapter, error) {
	bookID = strings.TrimSpace(bookID)
	chapterID := strings.TrimSpace(chapter.ID)
	if bookID == "" || chapterID == "" {
		return chapter, fmt.Errorf("qqreader book id and chapter id are required")
	}

	var envelope qqReaderReadEnvelope
	if err := s.getJSON(ctx, qqReaderReadURL(bookID, chapterID), &envelope); err != nil {
		return chapter, err
	}
	if err := qqReaderEnvelopeError("chapter", chapterID, envelope.Code, envelope.Msg); err != nil {
		return chapter, err
	}
	if envelope.Data.Encrypt || envelope.Data.FontEncrypted {
		return chapter, fmt.Errorf("qqreader chapter %s is encrypted and cannot be decoded without QQ阅读's reader", chapterID)
	}
	if envelope.Data.AuthStatus == 0 {
		return chapter, fmt.Errorf("qqreader chapter %s requires login, membership, or purchase; configure a QQ cookie with access to this chapter", chapterID)
	}
	if strings.TrimSpace(envelope.Data.Content) == "" {
		return chapter, fmt.Errorf("qqreader chapter %s returned empty content; the configured account may not have access", chapterID)
	}

	paragraphs, err := qqReaderParagraphs(envelope.Data.Content, envelope.Data.ChapterTitle)
	if err != nil {
		return chapter, err
	}
	if len(paragraphs) == 0 {
		return chapter, fmt.Errorf("qqreader chapter %s content not found", chapterID)
	}
	if title := strings.TrimSpace(envelope.Data.ChapterTitle); title != "" {
		chapter.Title = title
	}
	chapter.ID = chapterID
	chapter.URL = qqReaderChapterURL(bookID, chapterID)
	chapter.Content = strings.Join(paragraphs, "\n")
	chapter.Downloaded = true
	return chapter, nil
}

func (s *QQReaderSite) Download(ctx context.Context, ref model.BookRef) (*model.Book, error) {
	book, err := s.DownloadPlan(ctx, ref)
	if err != nil {
		return nil, err
	}
	for idx, chapter := range book.Chapters {
		loaded, err := s.FetchChapter(ctx, book.ID, chapter)
		if err != nil {
			return nil, fmt.Errorf("qqreader fetch chapter %s: %w", chapter.ID, err)
		}
		loaded.Order = idx + 1
		book.Chapters[idx] = loaded
	}
	return book, nil
}

func (s *QQReaderSite) fetchSearchPage(ctx context.Context, keyword string, pageNo, pageSize int) (qqReaderSearchEnvelope, error) {
	endpoint, err := url.Parse(qqReaderBaseURL + "/api/booksearch/query")
	if err != nil {
		return qqReaderSearchEnvelope{}, err
	}
	query := endpoint.Query()
	query.Set("keyWord", keyword)
	query.Set("pageNo", strconv.Itoa(pageNo))
	query.Set("pageSize", strconv.Itoa(pageSize))
	query.Set("isSo", "1")
	endpoint.RawQuery = query.Encode()

	var envelope qqReaderSearchEnvelope
	if err := s.getJSON(ctx, endpoint.String(), &envelope); err != nil {
		return envelope, err
	}
	if qqReaderSearchKeywordNotFound(envelope.Msg) {
		return envelope, errQQReaderSearchNoExactMatch
	}
	if err := qqReaderEnvelopeError("search", keyword, envelope.Code, envelope.Msg); err != nil {
		return envelope, err
	}
	return envelope, nil
}

func (s *QQReaderSite) fetchBookDetail(ctx context.Context, bookID string) (qqReaderDetailEnvelope, error) {
	endpoint := qqReaderBaseURL + "/api/book/detail?bid=" + url.QueryEscape(bookID)
	var envelope qqReaderDetailEnvelope
	if err := s.getJSON(ctx, endpoint, &envelope); err != nil {
		return envelope, err
	}
	if err := qqReaderEnvelopeError("detail", bookID, envelope.Code, envelope.Msg); err != nil {
		return envelope, err
	}
	if envelope.Data.BookID <= 0 {
		return envelope, fmt.Errorf("qqreader detail for book %s is empty", bookID)
	}
	return envelope, nil
}

func (s *QQReaderSite) fetchCatalog(ctx context.Context, bookID string) (qqReaderCatalogEnvelope, error) {
	endpoint := qqReaderBaseURL + "/api/book/detail/chapters?bid=" + url.QueryEscape(bookID)
	var envelope qqReaderCatalogEnvelope
	if err := s.getJSON(ctx, endpoint, &envelope); err != nil {
		return envelope, err
	}
	if err := qqReaderEnvelopeError("catalog", bookID, envelope.Code, envelope.Msg); err != nil {
		return envelope, err
	}
	return envelope, nil
}

func (s *QQReaderSite) getJSON(ctx context.Context, rawURL string, target any) error {
	body, err := getWithSiteRetry(ctx, func() (string, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
		if err != nil {
			return "", err
		}
		req.Header.Set("User-Agent", defaultBrowserUserAgent)
		req.Header.Set("Accept", "application/json, text/plain, */*")
		req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9,en;q=0.8")
		req.Header.Set("Referer", qqReaderBaseURL+"/")

		resp, err := s.client.Do(req)
		if err != nil {
			return "", err
		}
		defer resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return "", fmt.Errorf("http %d for %s", resp.StatusCode, rawURL)
		}
		data, err := io.ReadAll(resp.Body)
		if err != nil {
			return "", err
		}
		return string(data), nil
	}, defaultSiteRetryAttempts)
	if err != nil {
		return err
	}
	if err := json.Unmarshal([]byte(body), target); err != nil {
		return fmt.Errorf("decode qqreader response for %s: %w", rawURL, err)
	}
	return nil
}

func (s *QQReaderSite) injectCookieString(raw string) {
	raw = strings.TrimSpace(raw)
	if len(raw) >= len("Cookie:") && strings.EqualFold(raw[:len("Cookie:")], "Cookie:") {
		raw = strings.TrimSpace(raw[len("Cookie:"):])
	}
	if raw == "" || s.client == nil || s.client.Jar == nil {
		return
	}
	cookies := make([]*http.Cookie, 0)
	for _, part := range strings.Split(raw, ";") {
		name, value, ok := strings.Cut(strings.TrimSpace(part), "=")
		if !ok || strings.TrimSpace(name) == "" {
			continue
		}
		cookies = append(cookies, &http.Cookie{
			Name:  strings.TrimSpace(name),
			Value: strings.TrimSpace(value),
			Path:  "/",
		})
	}
	if len(cookies) == 0 {
		return
	}
	for _, rawURL := range []string{qqReaderBaseURL + "/", "https://ubook.reader.qq.com/"} {
		parsed, err := url.Parse(rawURL)
		if err == nil {
			s.client.Jar.SetCookies(parsed, cookies)
		}
	}
}

func qqReaderBookFromDetail(detail qqReaderDetailData, bookID string) *model.Book {
	now := time.Now().UTC()
	updatedAt := qqReaderParseTime(detail.UpdateTime)
	if updatedAt.IsZero() {
		updatedAt = now
	}
	status := "连载中"
	if detail.Finished == 1 {
		status = "完结"
	}
	tags := normalizeBookTags([]string{
		detail.Category2,
		detail.Category3,
		status,
		detail.Brand,
	})
	coverURL := strings.TrimSpace(detail.Cover)
	if coverURL == "" {
		coverURL = qqReaderCoverURL(bookID)
	}
	return &model.Book{
		Site:         "qqreader",
		ID:           bookID,
		Title:        strings.TrimSpace(detail.Title),
		Author:       strings.TrimSpace(detail.Author),
		Description:  strings.TrimSpace(detail.Intro),
		SourceURL:    qqReaderBookURL(bookID),
		CoverURL:     coverURL,
		Tags:         tags,
		DownloadedAt: now,
		UpdatedAt:    updatedAt,
	}
}

func qqReaderParagraphs(markup, chapterTitle string) ([]string, error) {
	doc, err := parseHTML(markup)
	if err != nil {
		return nil, err
	}
	nodes := findAll(doc, func(node *html.Node) bool {
		if node.Type != html.ElementNode {
			return false
		}
		switch node.Data {
		case "p", "h2", "h3", "h4", "h5", "h6":
			return true
		default:
			return false
		}
	})
	rawTitle := strings.TrimSpace(chapterTitle)
	title := strings.Join(strings.Fields(chapterTitle), "")
	paragraphs := make([]string, 0, len(nodes))
	for _, node := range nodes {
		text := cleanText(nodeTextPreserveLineBreaks(node))
		if text == "" {
			continue
		}
		if title != "" && strings.Join(strings.Fields(text), "") == title {
			continue
		}
		paragraphs = append(paragraphs, text)
	}
	if len(paragraphs) == 0 {
		body := findFirst(doc, func(node *html.Node) bool {
			return node.Type == html.ElementNode && node.Data == "body"
		})
		for _, text := range cleanLooseTexts(body) {
			if title != "" && strings.Join(strings.Fields(text), "") == title {
				continue
			}
			if rawTitle != "" && strings.HasPrefix(text, rawTitle) {
				text = strings.TrimSpace(strings.TrimPrefix(text, rawTitle))
			}
			if text == "" {
				continue
			}
			paragraphs = append(paragraphs, text)
		}
	}
	return compactParagraphs(paragraphs), nil
}

func qqReaderImageOnlyChapter(title string) bool {
	normalized := strings.ToLower(strings.Join(strings.Fields(title), ""))
	switch normalized {
	case "封面", "封面页", "cover", "frontcover", "coverpage":
		return true
	default:
		return false
	}
}

func qqReaderParseTime(raw string) time.Time {
	parsed, err := time.ParseInLocation("2006-01-02 15:04:05", strings.TrimSpace(raw), time.Local)
	if err != nil {
		return time.Time{}
	}
	return parsed.UTC()
}

func qqReaderEnvelopeError(kind, identity string, code int, message string) error {
	if code == 0 {
		return nil
	}
	message = strings.TrimSpace(message)
	if message == "" {
		message = fmt.Sprintf("code %d", code)
	}
	return fmt.Errorf("qqreader %s %s failed: %s", kind, identity, message)
}

func qqReaderSearchKeywordNotFound(message string) bool {
	message = strings.TrimSpace(message)
	return strings.Contains(message, "不在关键词库中")
}

func qqReaderBookURL(bookID string) string {
	return qqReaderBaseURL + "/book-detail/" + strings.TrimSpace(bookID)
}

func qqReaderChapterURL(bookID, chapterID string) string {
	return qqReaderBaseURL + "/book-read/" + strings.TrimSpace(bookID) + "/" + strings.TrimSpace(chapterID)
}

func qqReaderReadURL(bookID, chapterID string) string {
	query := url.Values{}
	query.Set("bid", strings.TrimSpace(bookID))
	query.Set("cid", strings.TrimSpace(chapterID))
	return qqReaderBaseURL + "/api/book/read?" + query.Encode()
}

func qqReaderCoverURL(bookID string) string {
	bookID = strings.TrimSpace(bookID)
	if bookID == "" {
		return ""
	}
	prefix := bookID
	if len(prefix) > 3 {
		prefix = prefix[len(prefix)-3:]
	} else {
		prefix = strings.Repeat("0", 3-len(prefix)) + prefix
	}
	return fmt.Sprintf(
		"https://wfqqreader-1252317822.image.myqcloud.com/cover/%s/%s/t5_%s.jpg",
		prefix,
		bookID,
		bookID,
	)
}
