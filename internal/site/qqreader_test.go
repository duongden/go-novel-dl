package site

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/guohuiyuan/go-novel-dl/internal/config"
	"github.com/guohuiyuan/go-novel-dl/internal/model"
)

type qqReaderRoundTripFunc func(*http.Request) (*http.Response, error)

func (fn qqReaderRoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}

func TestQQReaderResolveURL(t *testing.T) {
	site := NewQQReaderSite(config.DefaultConfig().ResolveSiteConfig("qqreader"))
	tests := []struct {
		name      string
		raw       string
		bookID    string
		chapterID string
	}{
		{
			name:      "chapter",
			raw:       "https://book.qq.com/book-read/621674/12",
			bookID:    "621674",
			chapterID: "12",
		},
		{
			name:   "detail",
			raw:    "https://book.qq.com/book-detail/621674",
			bookID: "621674",
		},
		{
			name:   "book chapter landing",
			raw:    "https://book.qq.com/book-chapter/621674/",
			bookID: "621674",
		},
		{
			name:      "reader frontend",
			raw:       "https://ubook.reader.qq.com/book-read/621674/4?from=share",
			bookID:    "621674",
			chapterID: "4",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			resolved, ok := site.ResolveURL(test.raw)
			if !ok {
				t.Fatalf("ResolveURL(%q) was not recognized", test.raw)
			}
			if resolved.SiteKey != "qqreader" || resolved.BookID != test.bookID || resolved.ChapterID != test.chapterID {
				t.Fatalf("unexpected resolution: %+v", resolved)
			}
		})
	}
	for _, raw := range []string{
		"https://example.com/book-read/621674/12",
		"https://book.qq.com/read/621674/12",
	} {
		if _, ok := site.ResolveURL(raw); ok {
			t.Fatalf("ResolveURL(%q) unexpectedly matched", raw)
		}
	}
}

func TestQQReaderSearchPaginatesAndDeduplicates(t *testing.T) {
	requestedPages := make([]string, 0, 2)
	site := newQQReaderTestSite(t, func(req *http.Request) (*http.Response, error) {
		if req.URL.Path != "/api/booksearch/query" {
			return qqReaderTestResponse(req, http.StatusNotFound, `{}`), nil
		}
		if req.URL.Query().Get("keyWord") != "清醒思考的艺术" || req.URL.Query().Get("isSo") != "1" {
			return qqReaderTestResponse(req, http.StatusBadRequest, `{}`), nil
		}
		pageNo := req.URL.Query().Get("pageNo")
		requestedPages = append(requestedPages, pageNo)
		switch pageNo {
		case "1":
			return qqReaderTestResponse(req, http.StatusOK, `{
				"code":0,"msg":"ok","data":{"pageNo":1,"pageSize":2,"hasNext":true,"bookList":[
					{"bid":621674,"title":"清醒思考的艺术","author":"作者甲","intro":"简介甲","category3Name":"知识读物","finished":1,"totalWords":66956,"free":2},
					{"bid":58750579,"title":"同名书","author":"作者乙","intro":"简介乙","free":0}
				]}
			}`), nil
		case "2":
			return qqReaderTestResponse(req, http.StatusOK, `{
				"code":0,"msg":"ok","data":{"pageNo":2,"pageSize":2,"hasNext":false,"bookList":[
					{"bid":58750579,"title":"同名书","author":"作者乙","intro":"简介乙","free":0},
					{"bid":48157550,"title":"第三本","author":"作者丙","intro":"简介丙","free":0}
				]}
			}`), nil
		default:
			return qqReaderTestResponse(req, http.StatusBadRequest, `{}`), nil
		}
	})

	results, err := site.Search(context.Background(), "清醒思考的艺术", 3)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 3 {
		t.Fatalf("expected 3 deduplicated results, got %d: %+v", len(results), results)
	}
	if results[0].BookID != "621674" || results[2].BookID != "48157550" {
		t.Fatalf("unexpected result order: %+v", results)
	}
	if results[0].URL != "https://book.qq.com/book-detail/621674" {
		t.Fatalf("unexpected result URL: %s", results[0].URL)
	}
	if results[0].CoverURL != "https://wfqqreader-1252317822.image.myqcloud.com/cover/674/621674/t5_621674.jpg" {
		t.Fatalf("unexpected generated cover URL: %s", results[0].CoverURL)
	}
	if strings.Join(requestedPages, ",") != "1,2" {
		t.Fatalf("unexpected requested pages: %v", requestedPages)
	}
}

func TestQQReaderSearchTreatsUnknownKeywordAsEmptyResult(t *testing.T) {
	site := newQQReaderTestSite(t, func(req *http.Request) (*http.Response, error) {
		return qqReaderTestResponse(req, http.StatusOK, `{
			"code":404,"msg":"不在关键词库中","data":{"pageNo":1,"pageSize":20,"hasNext":false,"bookList":[]}
		}`), nil
	})

	results, err := site.Search(context.Background(), "清醒思考", 20)
	if err != nil {
		t.Fatalf("a keyword-index miss must not fail the source: %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("expected no results, got %+v", results)
	}
}

func TestQQReaderSearchKeepsResultsWhenLaterPageFails(t *testing.T) {
	site := newQQReaderTestSite(t, func(req *http.Request) (*http.Response, error) {
		switch req.URL.Query().Get("pageNo") {
		case "1":
			return qqReaderTestResponse(req, http.StatusOK, `{
				"code":0,"msg":"ok","data":{"pageNo":1,"pageSize":20,"hasNext":true,"bookList":[
					{"bid":621674,"title":"Partial result","author":"Author","intro":"Intro"}
				]}
			}`), nil
		case "2":
			return qqReaderTestResponse(req, http.StatusOK, `{
				"code":500,"msg":"temporary upstream error","data":{}
			}`), nil
		default:
			return qqReaderTestResponse(req, http.StatusBadRequest, `{}`), nil
		}
	})

	results, err := site.Search(context.Background(), "book", 20)
	if err != nil {
		t.Fatalf("partial search results should be retained: %v", err)
	}
	if len(results) != 1 || results[0].BookID != "621674" {
		t.Fatalf("unexpected partial results: %+v", results)
	}
}

func TestQQReaderDownloadPlanFiltersLockedChaptersByDefault(t *testing.T) {
	site := newQQReaderTestSite(t, qqReaderPlanResponse)
	book, err := site.DownloadPlan(context.Background(), model.BookRef{BookID: "621674"})
	if err != nil {
		t.Fatalf("DownloadPlan: %v", err)
	}
	if book.Title != "清醒思考的艺术" || book.Author != "(德)罗尔夫·多贝里" {
		t.Fatalf("unexpected book metadata: %+v", book)
	}
	if book.SourceURL != "https://book.qq.com/book-detail/621674" {
		t.Fatalf("unexpected source URL: %s", book.SourceURL)
	}
	if len(book.Chapters) != 2 || book.Chapters[0].ID != "2" || book.Chapters[1].ID != "4" {
		t.Fatalf("expected free and purchased chapters only, got %+v", book.Chapters)
	}
	if !containsString(book.Tags, "哲学") || !containsString(book.Tags, "完结") {
		t.Fatalf("expected source categories in book tags, got %v", book.Tags)
	}

	cfg := config.DefaultConfig().ResolveSiteConfig("qqreader")
	cfg.General.FetchInaccessible = true
	allSite := NewQQReaderSite(cfg)
	allSite.client.Transport = site.client.Transport
	allBook, err := allSite.DownloadPlan(context.Background(), model.BookRef{BookID: "621674"})
	if err != nil {
		t.Fatalf("DownloadPlan with fetch inaccessible: %v", err)
	}
	if len(allBook.Chapters) != 3 || allBook.Chapters[1].ID != "3" {
		t.Fatalf("expected complete catalog, got %+v", allBook.Chapters)
	}

	cookieCfg := config.DefaultConfig().ResolveSiteConfig("qqreader")
	cookieCfg.Cookie = "ywguid=test; ywkey=test"
	cookieSite := NewQQReaderSite(cookieCfg)
	cookieSite.client.Transport = site.client.Transport
	cookieBook, err := cookieSite.DownloadPlan(context.Background(), model.BookRef{BookID: "621674"})
	if err != nil {
		t.Fatalf("DownloadPlan with cookie: %v", err)
	}
	if len(cookieBook.Chapters) != 3 {
		t.Fatalf("expected cookie-enabled catalog to retain locked chapters, got %+v", cookieBook.Chapters)
	}
}

func TestQQReaderFetchChapterExtractsParagraphs(t *testing.T) {
	site := newQQReaderTestSite(t, func(req *http.Request) (*http.Response, error) {
		if req.URL.Path != "/api/book/read" || req.URL.Query().Get("bid") != "621674" || req.URL.Query().Get("cid") != "4" {
			return qqReaderTestResponse(req, http.StatusBadRequest, `{}`), nil
		}
		return qqReaderTestResponse(req, http.StatusOK, `{
			"code":0,"msg":"ok","data":{
				"chapterTitle":"1 幸存偏误",
				"authStatus":1,
				"content":"<h1 class=\"firstTitle\">1 幸存偏误</h1><p class=\"subHead\">为什么你该去逛逛墓地</p><p class=\"content\">第一段。</p><p class=\"bodyPic-c\"><img src=\"cover.jpg\"></p><p class=\"content\">第二段。</p>"
			}
		}`), nil
	})

	chapter, err := site.FetchChapter(context.Background(), "621674", model.Chapter{ID: "4", Title: "旧标题"})
	if err != nil {
		t.Fatalf("FetchChapter: %v", err)
	}
	if !chapter.Downloaded || chapter.Title != "1 幸存偏误" {
		t.Fatalf("unexpected chapter metadata: %+v", chapter)
	}
	if chapter.Content != "为什么你该去逛逛墓地\n第一段。\n第二段。" {
		t.Fatalf("unexpected cleaned content: %q", chapter.Content)
	}
	if strings.Contains(chapter.Content, "幸存偏误") {
		t.Fatalf("chapter title should not be duplicated in content: %q", chapter.Content)
	}
}

func TestQQReaderParagraphsFallbackToLooseHTML(t *testing.T) {
	paragraphs, err := qqReaderParagraphs(
		`<h1>第一章</h1><div>第一段<br><br>第二段<br>第三段</div>`,
		"第一章",
	)
	if err != nil {
		t.Fatalf("qqReaderParagraphs: %v", err)
	}
	if strings.Join(paragraphs, "|") != "第一段|第二段|第三段" {
		t.Fatalf("unexpected fallback paragraphs: %#v", paragraphs)
	}
}

func TestQQReaderLockedChapterReturnsActionableError(t *testing.T) {
	site := newQQReaderTestSite(t, func(req *http.Request) (*http.Response, error) {
		return qqReaderTestResponse(req, http.StatusOK, `{
			"code":0,"msg":"ok","data":{"chapterTitle":"9 权威偏误","authStatus":0,"content":""}
		}`), nil
	})
	chapter, err := site.FetchChapter(context.Background(), "621674", model.Chapter{ID: "12"})
	if err == nil {
		t.Fatal("expected locked chapter error")
	}
	if !strings.Contains(err.Error(), "requires login, membership, or purchase") || !strings.Contains(err.Error(), "QQ cookie") {
		t.Fatalf("expected actionable login/purchase error, got %v", err)
	}
	if chapter.Downloaded || chapter.Content != "" {
		t.Fatalf("locked chapter must not be marked downloaded: %+v", chapter)
	}
}

func TestQQReaderInjectsCookieString(t *testing.T) {
	cfg := config.DefaultConfig().ResolveSiteConfig("qqreader")
	cfg.Cookie = "Cookie: ywguid=test-guid; ywkey=test-key; secToken=\"token==\""
	site := NewQQReaderSite(cfg)

	parsed, err := url.Parse(qqReaderBaseURL + "/")
	if err != nil {
		t.Fatal(err)
	}
	got := make(map[string]string)
	for _, cookie := range site.client.Jar.Cookies(parsed) {
		got[cookie.Name] = cookie.Value
	}
	if got["ywguid"] != "test-guid" || got["ywkey"] != "test-key" || got["secToken"] != `"token=="` {
		t.Fatalf("unexpected injected cookies: %#v", got)
	}
}

func TestQQReaderRegisteredForSearchAndDownload(t *testing.T) {
	registry := NewDefaultRegistry()
	descriptor, ok := registry.SiteDescriptor("qqreader")
	if !ok {
		t.Fatal("qqreader is not registered")
	}
	if descriptor.DisplayName != "QQ阅读" || !descriptor.Capabilities.Search || !descriptor.Capabilities.Download {
		t.Fatalf("unexpected descriptor: %+v", descriptor)
	}
	if !descriptor.DefaultAvailable {
		t.Fatal("qqreader should be available in default web search")
	}
}

func newQQReaderTestSite(t *testing.T, respond func(*http.Request) (*http.Response, error)) *QQReaderSite {
	t.Helper()
	site := NewQQReaderSite(testQQReaderConfig())
	site.client.Transport = qqReaderRoundTripFunc(respond)
	return site
}

func testQQReaderConfig() config.ResolvedSiteConfig {
	return config.DefaultConfig().ResolveSiteConfig("qqreader")
}

func qqReaderTestResponse(req *http.Request, status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"application/json; charset=utf-8"}},
		Body:       io.NopCloser(strings.NewReader(body)),
		Request:    req,
	}
}

func qqReaderPlanResponse(req *http.Request) (*http.Response, error) {
	switch req.URL.Path {
	case "/api/book/detail":
		if req.URL.Query().Get("bid") != "621674" {
			return qqReaderTestResponse(req, http.StatusBadRequest, `{}`), nil
		}
		return qqReaderTestResponse(req, http.StatusOK, `{
			"code":0,"msg":"ok","data":{
				"bid":621674,"title":"清醒思考的艺术","author":"(德)罗尔夫·多贝里",
				"intro":"简介","category2Name":"哲学","category3Name":"哲学知识读物",
				"finished":1,"totalWords":66956,"totalChapters":4,"cover":"",
				"updateTime":"2018-12-27 17:05:54"
			}
		}`), nil
	case "/api/book/detail/chapters":
		if req.URL.Query().Get("bid") != "621674" {
			return qqReaderTestResponse(req, http.StatusBadRequest, `{}`), nil
		}
		return qqReaderTestResponse(req, http.StatusOK, `{
			"code":0,"msg":"ok","data":[
				{"cid":1,"chapterName":"封面","free":1,"purchased":0},
				{"cid":2,"chapterName":"版权信息","free":1,"purchased":0},
				{"cid":3,"chapterName":"会员章","free":0,"purchased":0},
				{"cid":4,"chapterName":"已购章","free":0,"purchased":1}
			]
		}`), nil
	default:
		return qqReaderTestResponse(req, http.StatusBadRequest, fmt.Sprintf(`{"error":%q}`, req.URL.Path)), nil
	}
}

func containsString(items []string, target string) bool {
	for _, item := range items {
		if item == target {
			return true
		}
	}
	return false
}
