package site

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/guohuiyuan/go-novel-dl/internal/config"
	"github.com/guohuiyuan/go-novel-dl/internal/model"
)

const wereadBaseURL = "https://weread.qq.com"

type WeReadSite struct {
	client *http.Client
	cfg    config.ResolvedSiteConfig
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
	}
	site.injectCookieString(cfg.Cookie)
	return site
}

func (s *WeReadSite) Key() string         { return "weread" }
func (s *WeReadSite) DisplayName() string { return "微信读书" }

func (s *WeReadSite) Capabilities() Capabilities {
	return Capabilities{
		Search:   true,
		Download: false,
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

func (s *WeReadSite) DownloadPlan(context.Context, model.BookRef) (*model.Book, error) {
	return nil, s.downloadUnsupportedError()
}

func (s *WeReadSite) FetchChapter(context.Context, string, model.Chapter) (model.Chapter, error) {
	return model.Chapter{}, s.downloadUnsupportedError()
}

func (s *WeReadSite) Download(context.Context, model.BookRef) (*model.Book, error) {
	return nil, s.downloadUnsupportedError()
}

func (s *WeReadSite) SearchTag(ctx context.Context, tag string, limit int) ([]model.SearchResult, error) {
	tag = strings.TrimSpace(tag)
	if tag == "" {
		return nil, nil
	}
	return s.Search(ctx, tag, limit)
}

func (s *WeReadSite) ResolveURL(string) (*ResolvedURL, bool) {
	// WeRead links use opaque reader hashes rather than public numeric book IDs.
	return nil, false
}

func (s *WeReadSite) hasCookie() bool {
	return s != nil && strings.TrimSpace(s.cfg.Cookie) != ""
}

func (s *WeReadSite) downloadUnsupportedError() error {
	if s != nil && s.hasCookie() {
		return fmt.Errorf("微信读书未启用下载：已配置 Cookie 并用于登录态请求，但网页端正文由服务端签名保护，不能作为普通文本导出；搜索与排行榜仍可用")
	}
	return fmt.Errorf("微信读书未启用下载：正文需要登录和版权权限；当前未配置 Cookie，搜索与排行榜仍可用")
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
