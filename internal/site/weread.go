package site

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/guohuiyuan/go-novel-dl/internal/config"
	"github.com/guohuiyuan/go-novel-dl/internal/model"
)

const wereadBaseURL = "https://weread.qq.com"

// wereadReaderStateTTL 控制阅读页状态（psvts / 章节目录）的复用时长，
// 避免逐章重复拉页面，又不会长时间沿用可能失效的会话参数。
const wereadReaderStateTTL = 10 * time.Minute

type WeReadSite struct {
	client *http.Client
	cfg    config.ResolvedSiteConfig

	mu     sync.Mutex
	states map[string]wereadCachedState
}

type wereadCachedState struct {
	state     wereadReaderState
	fetchedAt time.Time
}

type wereadBookInfo struct {
	BookID     string `json:"bookId"`
	Title      string `json:"title"`
	Author     string `json:"author"`
	Intro      string `json:"intro"`
	Cover      string `json:"cover"`
	DeepLink   string `json:"deepLink"`
	ReaderLink string `json:"readerLink"`
	Category   string `json:"category"`
	Finished   int    `json:"finished"`
}

type wereadBookItem struct {
	BookInfo wereadBookInfo `json:"bookInfo"`
}

type wereadSearchResponse struct {
	Books   []wereadBookItem `json:"books"`
	HasMore int              `json:"hasMore"`
	SID     string           `json:"sid"`
	Total   int              `json:"totalCount"`
}

type wereadCategoryResponse struct {
	Books   []wereadBookItem `json:"books"`
	HasMore int              `json:"hasMore"`
	Total   int              `json:"totalCount"`
	SyncKey int64            `json:"synckey"`
}

// wereadChapterMeta 对应 reader.chapterInfos 里的一项。
type wereadChapterMeta struct {
	ChapterUID int64   `json:"chapterUid"`
	ChapterIdx int     `json:"chapterIdx"`
	Title      string  `json:"title"`
	Paid       int     `json:"paid"`
	Price      float64 `json:"price"`
	Level      int     `json:"level"`
	WordCount  int     `json:"wordCount"`
}

// wereadReaderBook 对应 reader.bookInfo 里下载需要的字段。
type wereadReaderBook struct {
	BookID         string `json:"bookId"`
	Title          string `json:"title"`
	Author         string `json:"author"`
	Intro          string `json:"intro"`
	Cover          string `json:"cover"`
	Category       string `json:"category"`
	Format         string `json:"format"`
	TotalWords     int    `json:"totalWords"`
	MaxFreeChapter int    `json:"maxFreeChapter"`
	PublishTime    string `json:"publishTime"`
	EncodeID       string `json:"encodeId"`
}

// wereadReaderState 是阅读页 __INITIAL_STATE__.reader 中与下载相关的部分。
type wereadReaderState struct {
	InfoID       string              `json:"infoId"`
	BookID       string              `json:"bookId"`
	PSVTS        string              `json:"psvts"`
	BookInfo     wereadReaderBook    `json:"bookInfo"`
	ChapterInfos []wereadChapterMeta `json:"chapterInfos"`
}

type wereadInitialState struct {
	Reader wereadReaderState `json:"reader"`
}

type wereadDiscoverySpec struct {
	Category string
	List     DiscoveryList
}

var wereadDiscoveryLists = []wereadDiscoverySpec{
	{
		Category: "rising",
		List: DiscoveryList{
			ID:              "rising",
			Name:            "飙升榜",
			Description:     "微信读书公开的近期阅读热度飙升作品",
			Kind:            "ranking",
			UpdateFrequency: "实时更新",
			BookCount:       20,
		},
	},
	{
		Category: "newbook",
		List: DiscoveryList{
			ID:              "newbook",
			Name:            "新书榜",
			Description:     "近期上架的公开热门新书",
			Kind:            "ranking",
			UpdateFrequency: "每日更新",
			BookCount:       20,
		},
	},
	{
		Category: "all",
		List: DiscoveryList{
			ID:              "all",
			Name:            "总榜",
			Description:     "微信读书综合阅读热度排行",
			Kind:            "ranking",
			UpdateFrequency: "每日更新",
			BookCount:       20,
		},
	},
	{
		Category: "newrating_publish",
		List: DiscoveryList{
			ID:              "newrating_publish",
			Name:            "新书好评榜",
			Description:     "近期新书中读者评价领先的作品",
			Kind:            "ranking",
			UpdateFrequency: "每日更新",
			BookCount:       20,
		},
	},
	{
		Category: "novel_male",
		List: DiscoveryList{
			ID:              "novel_male",
			Name:            "男频小说榜",
			Description:     "微信读书公开的男频小说热门排行",
			Kind:            "ranking",
			UpdateFrequency: "每日更新",
			BookCount:       20,
		},
	},
	{
		Category: "novel_female",
		List: DiscoveryList{
			ID:              "novel_female",
			Name:            "女频小说榜",
			Description:     "微信读书公开的女频小说热门排行",
			Kind:            "ranking",
			UpdateFrequency: "每日更新",
			BookCount:       20,
		},
	},
}

var _ DiscoveryProvider = (*WeReadSite)(nil)

func NewWeReadSite(cfg config.ResolvedSiteConfig) *WeReadSite {
	timeout := 15 * time.Second
	if cfg.General.Timeout > 0 {
		timeout = time.Duration(cfg.General.Timeout * float64(time.Second))
	}
	jar, _ := cookiejar.New(nil)
	client := newSiteHTTPClient(timeout, siteHTTPClientOptions{Jar: jar})
	site := &WeReadSite{
		client: client,
		cfg:    cfg,
		states: make(map[string]wereadCachedState),
	}
	site.injectCookieString(cfg.Cookie)
	return site
}

func (s *WeReadSite) Key() string         { return "weread" }
func (s *WeReadSite) DisplayName() string { return "微信读书" }

func (s *WeReadSite) Capabilities() Capabilities {
	return Capabilities{
		Search:   true,
		Download: true,
		Login:    s.hasCookie(),
	}
}

func (s *WeReadSite) DiscoveryLists(ctx context.Context) ([]DiscoveryList, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	lists := make([]DiscoveryList, 0, len(wereadDiscoveryLists))
	for _, item := range wereadDiscoveryLists {
		lists = append(lists, item.List)
	}
	return cloneDiscoveryLists(lists), nil
}

func (s *WeReadSite) DiscoveryBooks(ctx context.Context, listID string, limit int) ([]model.SearchResult, error) {
	listID = strings.TrimSpace(listID)
	var selected *wereadDiscoverySpec
	for idx := range wereadDiscoveryLists {
		if wereadDiscoveryLists[idx].List.ID == listID {
			selected = &wereadDiscoveryLists[idx]
			break
		}
	}
	if selected == nil {
		return nil, fmt.Errorf("weread discovery list %q not found", listID)
	}
	limit = normalizeDiscoveryLimit(limit, 20)
	count := limit
	if count > 20 {
		// The public category endpoint exposes one 20-book page.
		count = 20
	}
	query := url.Values{}
	query.Set("rank", "1")
	query.Set("maxIdx", "0")
	query.Set("count", strconv.Itoa(count))
	endpoint := fmt.Sprintf(
		"%s/web/bookListInCategory/%s?%s",
		wereadBaseURL,
		url.PathEscape(selected.Category),
		query.Encode(),
	)
	var response wereadCategoryResponse
	if err := s.getJSON(ctx, endpoint, fmt.Sprintf("%s/web/category/%s", wereadBaseURL, selected.Category), &response); err != nil {
		return nil, err
	}
	results := make([]model.SearchResult, 0, len(response.Books))
	seen := make(map[string]struct{}, len(response.Books))
	for _, item := range response.Books {
		result, ok := wereadSearchResult(item.BookInfo)
		if !ok {
			continue
		}
		if _, exists := seen[result.BookID]; exists {
			continue
		}
		seen[result.BookID] = struct{}{}
		results = append(results, result)
		if len(results) >= limit {
			break
		}
	}
	return results, nil
}

func (s *WeReadSite) Search(ctx context.Context, keyword string, limit int) ([]model.SearchResult, error) {
	keyword = strings.TrimSpace(keyword)
	if keyword == "" {
		return nil, nil
	}
	limit = normalizeDiscoveryLimit(limit, 20)
	results := make([]model.SearchResult, 0, limit)
	seen := make(map[string]struct{}, limit)
	maxIdx := 0
	sid := ""
	for len(results) < limit {
		count := limit - len(results)
		if count > 20 {
			count = 20
		}
		query := url.Values{}
		query.Set("keyword", keyword)
		query.Set("maxIdx", strconv.Itoa(maxIdx))
		query.Set("fragmentSize", "120")
		query.Set("count", strconv.Itoa(count))
		if sid != "" {
			query.Set("sid", sid)
		}
		var response wereadSearchResponse
		err := s.getJSON(ctx, wereadBaseURL+"/web/search/global?"+query.Encode(), wereadBaseURL+"/", &response)
		if err != nil {
			if len(results) > 0 {
				return results, nil
			}
			return nil, err
		}
		if len(response.Books) == 0 {
			break
		}
		added := 0
		for _, item := range response.Books {
			result, ok := wereadSearchResult(item.BookInfo)
			if !ok {
				continue
			}
			if _, exists := seen[result.BookID]; exists {
				continue
			}
			seen[result.BookID] = struct{}{}
			results = append(results, result)
			added++
			if len(results) >= limit {
				break
			}
		}
		if response.HasMore == 0 || added == 0 {
			break
		}
		maxIdx += len(response.Books)
		if nextSID := strings.TrimSpace(response.SID); nextSID != "" {
			sid = nextSID
		}
	}
	return results, nil
}

// DownloadPlan 拉取阅读页的章节目录，默认只保留可读的免费章节。
// 未登录时付费章节只能拿到试读片段，因此默认跳过；配置 Cookie 或
// fetch_inaccessible 后才纳入，由 FetchChapter 判断实际拿到的是不是全文。
func (s *WeReadSite) DownloadPlan(ctx context.Context, ref model.BookRef) (*model.Book, error) {
	readerID, err := wereadResolveReaderID(ref.BookID)
	if err != nil {
		return nil, err
	}
	state, err := s.readerState(ctx, readerID)
	if err != nil {
		return nil, err
	}
	if len(state.ChapterInfos) == 0 {
		return nil, fmt.Errorf("微信读书书籍 %s 的章节目录为空，可能已下架或需要登录", ref.BookID)
	}

	includeLocked := s.cfg.General.FetchInaccessible || s.hasCookie()
	chapters := make([]model.Chapter, 0, len(state.ChapterInfos))
	for _, item := range state.ChapterInfos {
		if item.ChapterUID == 0 {
			continue
		}
		title := cleanText(item.Title)
		if title == "" {
			continue
		}
		// 免费章节 price 恒为 0；付费章节为 -1（整本购买）或正数（按章购买）。
		free := item.Price == 0 && item.Paid == 0
		if !free && !includeLocked {
			continue
		}
		chapterID := strconv.FormatInt(item.ChapterUID, 10)
		chapters = append(chapters, model.Chapter{
			ID:     chapterID,
			Title:  title,
			URL:    fmt.Sprintf("%s/web/reader/%s", wereadBaseURL, readerID),
			Order:  len(chapters) + 1,
			Volume: wereadVolumeLabel(item.Level),
		})
	}
	if len(chapters) == 0 {
		return nil, fmt.Errorf(
			"微信读书《%s》的 %d 章全部为付费内容，未登录只能读试读片段；配置登录 Cookie 或开启 fetch_inaccessible 后重试",
			cleanText(state.BookInfo.Title), len(state.ChapterInfos))
	}

	bookID := strings.TrimSpace(state.BookInfo.BookID)
	if bookID == "" {
		bookID = strings.TrimSpace(state.BookID)
	}
	if bookID == "" {
		bookID = strings.TrimSpace(ref.BookID)
	}

	book := &model.Book{
		Site:         s.Key(),
		ID:           bookID,
		Title:        cleanText(state.BookInfo.Title),
		Author:       cleanText(state.BookInfo.Author),
		Description:  cleanText(state.BookInfo.Intro),
		SourceURL:    fmt.Sprintf("%s/web/reader/%s", wereadBaseURL, readerID),
		CoverURL:     strings.TrimSpace(state.BookInfo.Cover),
		DownloadedAt: time.Now(),
		UpdatedAt:    time.Now(),
	}
	if category := cleanText(state.BookInfo.Category); category != "" {
		book.Tags = []string{category}
	}
	book.Chapters = applyChapterRange(dedupChapters(chapters), ref)
	return book, nil
}

// FetchChapter 请求正文接口 e_2（一次返回整章），解码后转纯文本。
func (s *WeReadSite) FetchChapter(ctx context.Context, bookID string, chapter model.Chapter) (model.Chapter, error) {
	readerID, err := wereadResolveReaderID(bookID)
	if err != nil {
		return chapter, err
	}
	chapterUID, err := strconv.ParseInt(strings.TrimSpace(chapter.ID), 10, 64)
	if err != nil || chapterUID <= 0 {
		return chapter, fmt.Errorf("微信读书章节 ID %q 不是有效的 chapterUid", chapter.ID)
	}
	state, err := s.readerState(ctx, readerID)
	if err != nil {
		return chapter, err
	}

	payload, err := s.postChapterPayload(ctx, state, chapterUID, readerID)
	if err != nil {
		return chapter, err
	}
	// psvts 是页面级会话参数，会随时间失效并表现为空壳响应；此时刷新一次再重试。
	if strings.TrimSpace(payload) == "{}" {
		s.invalidateReaderState(readerID)
		if refreshed, refreshErr := s.readerState(ctx, readerID); refreshErr == nil {
			state = refreshed
			payload, err = s.postChapterPayload(ctx, state, chapterUID, readerID)
			if err != nil {
				return chapter, err
			}
		}
	}
	raw, err := wereadDecodeChapterPayload(payload)
	if err != nil {
		return chapter, err
	}
	markup := string(raw)
	content := wereadHTMLToText(markup)
	if content == "" {
		return chapter, fmt.Errorf("微信读书章节 %s 解码后没有正文", chapter.ID)
	}
	if !wereadLooksLikeFullDocument(markup) {
		return chapter, fmt.Errorf(
			"微信读书章节 %s《%s》只返回试读片段（约 %d 字），需要在微信读书购买该章后才能下载全文",
			chapter.ID, chapter.Title, len([]rune(content)))
	}

	chapter.ID = strconv.FormatInt(chapterUID, 10)
	chapter.Content = content
	chapter.URL = fmt.Sprintf("%s/web/reader/%s", wereadBaseURL, readerID)
	chapter.Downloaded = true
	return chapter, nil
}

func (s *WeReadSite) Download(ctx context.Context, ref model.BookRef) (*model.Book, error) {
	book, err := s.DownloadPlan(ctx, ref)
	if err != nil {
		return nil, err
	}
	for idx, chapter := range book.Chapters {
		loaded, err := s.FetchChapter(ctx, book.ID, chapter)
		if err != nil {
			return nil, fmt.Errorf("微信读书抓取章节 %s: %w", chapter.ID, err)
		}
		loaded.Order = idx + 1
		book.Chapters[idx] = loaded
	}
	return book, nil
}

// readerState 拉取并缓存阅读页状态（psvts、书籍信息、章节目录）。
func (s *WeReadSite) readerState(ctx context.Context, readerID string) (wereadReaderState, error) {
	s.mu.Lock()
	if cached, ok := s.states[readerID]; ok && time.Since(cached.fetchedAt) < wereadReaderStateTTL {
		state := cached.state
		s.mu.Unlock()
		return state, nil
	}
	s.mu.Unlock()

	pageURL := fmt.Sprintf("%s/web/reader/%s", wereadBaseURL, readerID)
	page, err := s.getText(ctx, pageURL, wereadBaseURL+"/")
	if err != nil {
		return wereadReaderState{}, err
	}
	state, err := wereadParseInitialState(page)
	if err != nil {
		return wereadReaderState{}, err
	}
	if strings.TrimSpace(state.PSVTS) == "" {
		return wereadReaderState{}, fmt.Errorf("微信读书阅读页没有下发 psvts，无法请求正文（可能需要先登录）")
	}

	s.mu.Lock()
	s.states[readerID] = wereadCachedState{state: state, fetchedAt: time.Now()}
	s.mu.Unlock()
	return state, nil
}

// invalidateReaderState 丢弃缓存的阅读页状态，用于会话参数失效后重新取。
func (s *WeReadSite) invalidateReaderState(readerID string) {
	s.mu.Lock()
	delete(s.states, readerID)
	s.mu.Unlock()
}

func (s *WeReadSite) postChapterPayload(ctx context.Context, state wereadReaderState, chapterUID int64, readerID string) (string, error) {
	bookID := strings.TrimSpace(state.BookInfo.BookID)
	if bookID == "" {
		bookID = strings.TrimSpace(state.BookID)
	}
	body := wereadChapterRequestBody(bookID, chapterUID, state.PSVTS)
	endpoint := fmt.Sprintf("%s/web/book/chapter/%s", wereadBaseURL, wereadChapterEndpoint)
	referer := fmt.Sprintf("%s/web/reader/%s", wereadBaseURL, readerID)

	data, err := json.Marshal(body)
	if err != nil {
		return "", err
	}
	return getWithSiteRetry(ctx, func() (string, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(data))
		if err != nil {
			return "", err
		}
		req.Header.Set("User-Agent", defaultBrowserUserAgent)
		req.Header.Set("Accept", "application/json, text/plain, */*")
		req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9,en;q=0.8")
		req.Header.Set("Content-Type", "application/json;charset=UTF-8")
		req.Header.Set("Referer", referer)
		resp, err := s.client.Do(req)
		if err != nil {
			return "", err
		}
		defer resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return "", fmt.Errorf("http %d for %s", resp.StatusCode, endpoint)
		}
		raw, err := io.ReadAll(resp.Body)
		if err != nil {
			return "", err
		}
		return string(raw), nil
	}, defaultSiteRetryAttempts)
}

// getText 抓取 HTML 页面（不做 JSON 解析）。
func (s *WeReadSite) getText(ctx context.Context, rawURL, referer string) (string, error) {
	return getWithSiteRetry(ctx, func() (string, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
		if err != nil {
			return "", err
		}
		req.Header.Set("User-Agent", defaultBrowserUserAgent)
		req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
		req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9,en;q=0.8")
		if referer != "" {
			req.Header.Set("Referer", referer)
		}
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
}

func (s *WeReadSite) SearchTag(ctx context.Context, tag string, limit int) ([]model.SearchResult, error) {
	tag = strings.TrimSpace(tag)
	if tag == "" {
		return nil, nil
	}
	return s.Search(ctx, tag, limit)
}

// ResolveURL 支持直接粘贴阅读页链接；链接里的 encodeId 不可逆推数字 bookId，
// 但下载链路本身就能接受 encodeId，因此原样作为 BookID 返回。
func (s *WeReadSite) ResolveURL(rawURL string) (*ResolvedURL, bool) {
	trimmed := strings.TrimSpace(rawURL)
	if trimmed == "" {
		return nil, false
	}
	parsed, err := url.Parse(trimmed)
	if err != nil {
		return nil, false
	}
	if !strings.EqualFold(parsed.Hostname(), "weread.qq.com") {
		return nil, false
	}
	path := strings.Trim(parsed.Path, "/")
	const readerPrefix = "web/reader/"
	if !strings.HasPrefix(path, readerPrefix) {
		return nil, false
	}
	encodeID := strings.TrimPrefix(path, readerPrefix)
	if slash := strings.IndexByte(encodeID, '/'); slash >= 0 {
		encodeID = encodeID[:slash]
	}
	if !wereadReaderIDPattern.MatchString(encodeID) {
		return nil, false
	}
	return &ResolvedURL{
		SiteKey:   s.Key(),
		BookID:    encodeID,
		Canonical: trimmed,
	}, true
}

func (s *WeReadSite) hasCookie() bool {
	return s != nil && strings.TrimSpace(s.cfg.Cookie) != ""
}

// wereadChapterEndpoint 是网页端「一次返回整章」的正文端点。
// e_0/e_1/e_3 是分片端点（每片约 8KB），e_2 直接给全文，下载走 e_2 即可。
const wereadChapterEndpoint = "e_2"

// wereadResolveReaderID 把用户给的书 ID 统一成阅读页路径里的 encodeId。
// 纯数字按网页端规则编码；本身就是 encodeId（24 位左右的 base36 串）则原样使用。
func wereadResolveReaderID(bookID string) (string, error) {
	id := strings.TrimSpace(bookID)
	id = strings.TrimPrefix(id, "weread:")
	if id == "" {
		return "", fmt.Errorf("微信读书 book id 不能为空")
	}
	if wereadDigitsOnlyPattern.MatchString(id) {
		encoded := wereadEncodeID(id)
		if encoded == "" {
			return "", fmt.Errorf("微信读书 book id %q 无法编码", bookID)
		}
		return encoded, nil
	}
	if wereadReaderIDPattern.MatchString(id) {
		return id, nil
	}
	return "", fmt.Errorf("无法识别的微信读书标识 %q：请提供数字 bookId 或阅读页 URL 里的 encodeId", bookID)
}

// wereadParseInitialState 从阅读页 HTML 中取出 window.__INITIAL_STATE__。
func wereadParseInitialState(page string) (wereadReaderState, error) {
	const anchor = "window.__INITIAL_STATE__="
	idx := strings.Index(page, anchor)
	if idx < 0 {
		return wereadReaderState{}, fmt.Errorf("微信读书阅读页缺少 __INITIAL_STATE__，页面结构可能已变更")
	}
	payload := strings.TrimSpace(page[idx+len(anchor):])
	decoder := json.NewDecoder(strings.NewReader(payload))
	var state wereadInitialState
	if err := decoder.Decode(&state); err != nil {
		return wereadReaderState{}, fmt.Errorf("解析微信读书阅读页状态失败: %w", err)
	}
	return state.Reader, nil
}

// wereadChapterRequestBody 复刻网页端构造正文请求体的方式：
// b/c 用 e() 编码，r 是随机数平方，ct 为秒级时间戳，pc 必须是 e(客户端时间戳)，
// ps 是阅读页下发的 psvts，最后对全部参数排序序列化后签名。
func wereadChapterRequestBody(bookID string, chapterUID int64, psvts string) map[string]string {
	now := time.Now().Unix()
	noise := rand.Int63n(10000)
	params := map[string]string{
		"b":  wereadEncodeID(bookID),
		"c":  wereadEncodeID(strconv.FormatInt(chapterUID, 10)),
		"ct": strconv.FormatInt(now, 10),
		"pc": wereadEncodeID(strconv.FormatInt(now-1, 10)),
		"ps": psvts,
		"r":  strconv.FormatInt(noise*noise, 10),
		"sc": "0",
		"st": "0",
	}
	params["s"] = wereadSignQuery(params)
	return params
}

// wereadVolumeLabel 把目录层级映射成卷名，保持与其他站点一致的 Volume 语义。
func wereadVolumeLabel(level int) string {
	if level > 1 {
		return fmt.Sprintf("第 %d 级目录", level)
	}
	return ""
}

func (s *WeReadSite) injectCookieString(raw string) {
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
	base, err := url.Parse(wereadBaseURL)
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
			Domain: ".weread.qq.com",
		})
	}
	if len(cookies) > 0 {
		s.client.Jar.SetCookies(base, cookies)
	}
}

func (s *WeReadSite) getJSON(ctx context.Context, rawURL, referer string, target any) error {
	body, err := getWithSiteRetry(ctx, func() (string, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
		if err != nil {
			return "", err
		}
		req.Header.Set("User-Agent", defaultBrowserUserAgent)
		req.Header.Set("Accept", "application/json, text/plain, */*")
		req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9,en;q=0.8")
		req.Header.Set("Referer", referer)
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
		return fmt.Errorf("decode weread response for %s: %w", rawURL, err)
	}
	return nil
}

func wereadSearchResult(book wereadBookInfo) (model.SearchResult, bool) {
	bookID := strings.TrimSpace(book.BookID)
	title := cleanText(book.Title)
	if bookID == "" || title == "" {
		return model.SearchResult{}, false
	}
	sourceURL := strings.TrimSpace(book.ReaderLink)
	if sourceURL == "" {
		sourceURL = strings.TrimSpace(book.DeepLink)
	}
	return model.SearchResult{
		Site:        "weread",
		BookID:      bookID,
		Title:       title,
		Author:      cleanText(book.Author),
		Description: cleanText(book.Intro),
		URL:         sourceURL,
		CoverURL:    strings.TrimSpace(book.Cover),
	}, true
}
