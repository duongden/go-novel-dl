package site

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/guohuiyuan/go-novel-dl/internal/config"
	"github.com/guohuiyuan/go-novel-dl/internal/model"
)

type wereadRoundTripFunc func(*http.Request) (*http.Response, error)

func (fn wereadRoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}

func TestWeReadSearchPaginatesAndDeduplicates(t *testing.T) {
	requestedPages := make([]string, 0, 2)
	site := newWeReadTestSite(t, func(req *http.Request) (*http.Response, error) {
		if req.URL.Path != "/web/search/global" {
			return wereadTestResponse(req, http.StatusNotFound, `{}`), nil
		}
		if req.URL.Query().Get("keyword") != "小说" {
			return wereadTestResponse(req, http.StatusBadRequest, `{}`), nil
		}
		maxIdx := req.URL.Query().Get("maxIdx")
		requestedPages = append(requestedPages, maxIdx+":"+req.URL.Query().Get("sid"))
		switch maxIdx {
		case "0":
			return wereadTestResponse(req, http.StatusOK, `{
				"books":[
					{"bookInfo":{"bookId":"101","title":"Book 101","author":"Author A","intro":"Intro A","readerLink":"https://weread.qq.com/web/reader/reader-101","cover":"https://cdn.example/101.jpg"}},
					{"bookInfo":{"bookId":"102","title":"Book 102","author":"Author B","intro":"Intro B","readerLink":"https://weread.qq.com/web/reader/reader-102"}}
				],
				"hasMore":1,"sid":"session-1","totalCount":3
			}`), nil
		case "2":
			if req.URL.Query().Get("sid") != "session-1" {
				return wereadTestResponse(req, http.StatusBadRequest, `{}`), nil
			}
			return wereadTestResponse(req, http.StatusOK, `{
				"books":[
					{"bookInfo":{"bookId":"102","title":"Book 102 duplicate","author":"Author B","intro":"Intro B"}},
					{"bookInfo":{"bookId":"103","title":"Book 103","author":"Author C","intro":"Intro C","deepLink":"https://weread.qq.com/book-detail?type=1&v=reader-103"}}
				],
				"hasMore":0,"sid":"session-1","totalCount":3
			}`), nil
		default:
			return wereadTestResponse(req, http.StatusBadRequest, `{}`), nil
		}
	})

	results, err := site.Search(context.Background(), "小说", 3)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 3 || results[0].BookID != "101" || results[2].BookID != "103" {
		t.Fatalf("unexpected deduplicated search results: %+v", results)
	}
	if results[2].URL != "https://weread.qq.com/book-detail?type=1&v=reader-103" {
		t.Fatalf("expected deep-link fallback, got %q", results[2].URL)
	}
	if strings.Join(requestedPages, ",") != "0:,2:session-1" {
		t.Fatalf("unexpected search pagination: %v", requestedPages)
	}
}

func TestWeReadDiscoveryListsAndRankingBooks(t *testing.T) {
	site := newWeReadTestSite(t, func(req *http.Request) (*http.Response, error) {
		if req.URL.Path != "/web/bookListInCategory/rising" ||
			req.URL.Query().Get("rank") != "1" ||
			req.URL.Query().Get("count") != "20" {
			return wereadTestResponse(req, http.StatusNotFound, `{}`), nil
		}
		return wereadTestResponse(req, http.StatusOK, `{
			"books":[
				{"bookInfo":{"bookId":"201","title":"Rising 201","author":"Author 201","intro":"Intro 201","readerLink":"https://weread.qq.com/web/reader/reader-201"}},
				{"bookInfo":{"bookId":"201","title":"Rising duplicate","author":"Author 201"}},
				{"bookInfo":{"bookId":"202","title":"Rising 202","author":"Author 202"}}
			],
			"hasMore":1,"totalCount":40,"synckey":123
		}`), nil
	})

	lists, err := site.DiscoveryLists(context.Background())
	if err != nil {
		t.Fatalf("DiscoveryLists: %v", err)
	}
	if len(lists) != len(wereadDiscoveryLists) {
		t.Fatalf("unexpected list count: %d", len(lists))
	}

	books, err := site.DiscoveryBooks(context.Background(), "rising", 30)
	if err != nil {
		t.Fatalf("DiscoveryBooks: %v", err)
	}
	if len(books) != 2 || books[0].BookID != "201" || books[1].BookID != "202" {
		t.Fatalf("unexpected ranking books: %+v", books)
	}
}

func TestWeReadIsSearchOnly(t *testing.T) {
	site := newWeReadTestSite(t, func(req *http.Request) (*http.Response, error) {
		return wereadTestResponse(req, http.StatusInternalServerError, `{}`), nil
	})
	if capabilities := site.Capabilities(); !capabilities.Search || capabilities.Download || capabilities.Login {
		t.Fatalf("unexpected capabilities: %+v", capabilities)
	}
	if _, err := site.DownloadPlan(context.Background(), model.BookRef{BookID: "201"}); err == nil ||
		!strings.Contains(err.Error(), "未启用下载") || !strings.Contains(err.Error(), "未配置 Cookie") {
		t.Fatalf("expected an explicit download limitation, got %v", err)
	}
}

func TestWeReadConfiguredCookieIsScopedAndKeptOutOfErrors(t *testing.T) {
	const (
		sessionCookie = "session-test-value"
		readerCookie  = "reader-test-value"
	)
	cfg := config.DefaultConfig()
	cfg.Sites["weread"] = config.SiteConfig{
		Cookie: "Cookie: wr_skey=" + sessionCookie + "; wr_vid=" + readerCookie,
	}
	site := NewWeReadSite(cfg.ResolveSiteConfig("weread"))
	if capabilities := site.Capabilities(); !capabilities.Login {
		t.Fatalf("configured cookie should advertise login capability: %+v", capabilities)
	}

	base, err := url.Parse(wereadBaseURL)
	if err != nil {
		t.Fatalf("parse base URL: %v", err)
	}
	other, err := url.Parse("https://example.com/")
	if err != nil {
		t.Fatalf("parse other URL: %v", err)
	}
	if cookies := site.client.Jar.Cookies(base); len(cookies) != 2 {
		t.Fatalf("expected two scoped cookies, got %d", len(cookies))
	}
	if cookies := site.client.Jar.Cookies(other); len(cookies) != 0 {
		t.Fatalf("cookies leaked outside weread domain: %v", cookies)
	}

	var gotCookie string
	site.client.Transport = wereadRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		gotCookie = req.Header.Get("Cookie")
		return wereadTestResponse(req, http.StatusOK, `{"books":[],"hasMore":0}`), nil
	})
	if _, err := site.Search(context.Background(), "小说", 5); err != nil {
		t.Fatalf("Search: %v", err)
	}
	if !strings.Contains(gotCookie, "wr_skey="+sessionCookie) ||
		!strings.Contains(gotCookie, "wr_vid="+readerCookie) {
		t.Fatalf("configured cookies were not sent to weread: %q", gotCookie)
	}

	_, err = site.FetchChapter(context.Background(), "book", model.Chapter{ID: "chapter"})
	if err == nil {
		t.Fatal("expected protected chapter download to remain disabled")
	}
	message := err.Error()
	if !strings.Contains(message, "服务端签名保护") || !strings.Contains(message, "未启用下载") {
		t.Fatalf("unexpected configured-cookie limitation: %v", err)
	}
	if strings.Contains(message, sessionCookie) || strings.Contains(message, readerCookie) {
		t.Fatalf("download error leaked cookie data: %v", err)
	}
}

func newWeReadTestSite(
	t *testing.T,
	respond func(*http.Request) (*http.Response, error),
) *WeReadSite {
	t.Helper()
	site := NewWeReadSite(config.DefaultConfig().ResolveSiteConfig("weread"))
	site.client.Transport = wereadRoundTripFunc(respond)
	return site
}

func wereadTestResponse(req *http.Request, status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"application/json; charset=utf-8"}},
		Body:       io.NopCloser(strings.NewReader(body)),
		Request:    req,
	}
}
