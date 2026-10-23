package site

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
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

func TestWeReadAdvertisesDownloadAndSearch(t *testing.T) {
	site := newWeReadTestSite(t, func(req *http.Request) (*http.Response, error) {
		return wereadTestResponse(req, http.StatusInternalServerError, `{}`), nil
	})
	capabilities := site.Capabilities()
	if !capabilities.Search || !capabilities.Download {
		t.Fatalf("unexpected capabilities: %+v", capabilities)
	}
	if capabilities.Login {
		t.Fatalf("login should stay off without a cookie: %+v", capabilities)
	}
	if _, err := site.DownloadPlan(context.Background(), model.BookRef{BookID: "201"}); err == nil {
		t.Fatal("expected the plan to fail when the reader page is unavailable")
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
		t.Fatal("expected an error for an unrecognizable chapter reference")
	}
	message := err.Error()
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

func wereadReaderPageStub() string {
	return `<html><body><script>window.__INITIAL_STATE__=` +
		`{"reader":{"infoId":"rid-201","bookId":"201","psvts":"ps-test",` +
		`"bookInfo":{"bookId":"201","title":"Book 201","author":"Author 201","intro":"Intro 201",` +
		`"cover":"https://cdn.example/201.jpg","category":"精品小说"},` +
		`"chapterInfos":[{"chapterUid":44,"chapterIdx":1,"title":"版权信息","price":0,"paid":0},` +
		`{"chapterUid":45,"chapterIdx":2,"title":"付费章","price":2.5,"paid":1}]}};` +
		`</script></body></html>`
}

func TestWeReadDownloadPlanListsPaidChaptersAsLocked(t *testing.T) {
	site := newWeReadTestSite(t, func(req *http.Request) (*http.Response, error) {
		if strings.HasPrefix(req.URL.Path, "/web/reader/") {
			return wereadTestResponse(req, http.StatusOK, wereadReaderPageStub()), nil
		}
		return wereadTestResponse(req, http.StatusNotFound, `{}`), nil
	})

	book, err := site.DownloadPlan(context.Background(), model.BookRef{BookID: "201"})
	if err != nil {
		t.Fatalf("DownloadPlan: %v", err)
	}
	if book.Title != "Book 201" || book.Author != "Author 201" {
		t.Fatalf("unexpected book metadata: %+v", book)
	}
	// 目录要列全：付费章节照样返回，只是标记 locked，供详情页展示。
	if len(book.Chapters) != 2 {
		t.Fatalf("expected full catalog with paid chapters, got %+v", book.Chapters)
	}
	if book.Chapters[0].ID != "44" || book.Chapters[0].Locked {
		t.Fatalf("free chapter should not be locked: %+v", book.Chapters[0])
	}
	if book.Chapters[1].ID != "45" || !book.Chapters[1].Locked {
		t.Fatalf("paid chapter should be marked locked: %+v", book.Chapters[1])
	}
	if !strings.HasPrefix(book.SourceURL, "https://weread.qq.com/web/reader/") {
		t.Fatalf("unexpected source url: %q", book.SourceURL)
	}
}

func TestWeReadDownloadPlanKeepsPaidChaptersWhenUnrestricted(t *testing.T) {
	cfg := config.DefaultConfig()
	siteCfg := cfg.ResolveSiteConfig("weread")
	siteCfg.General.FetchInaccessible = true
	site := NewWeReadSite(siteCfg)
	site.client.Transport = wereadRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		return wereadTestResponse(req, http.StatusOK, wereadReaderPageStub()), nil
	})

	book, err := site.DownloadPlan(context.Background(), model.BookRef{BookID: "201"})
	if err != nil {
		t.Fatalf("DownloadPlan: %v", err)
	}
	if len(book.Chapters) != 2 || !book.Chapters[1].Locked {
		t.Fatalf("catalog should stay complete and keep locked flags, got %+v", book.Chapters)
	}
}

func TestWeReadFetchChapterDecodesPayload(t *testing.T) {
	payload, err := os.ReadFile("testdata/weread_copyright_payload.txt")
	if err != nil {
		t.Fatalf("read testdata: %v", err)
	}
	var sentBody map[string]any
	site := newWeReadTestSite(t, func(req *http.Request) (*http.Response, error) {
		switch {
		case strings.HasPrefix(req.URL.Path, "/web/reader/"):
			return wereadTestResponse(req, http.StatusOK, wereadReaderPageStub()), nil
		case req.URL.Path == "/web/book/chapter/e_2":
			defer req.Body.Close()
			_ = json.NewDecoder(req.Body).Decode(&sentBody)
			return wereadTestResponse(req, http.StatusOK, string(payload)), nil
		default:
			return wereadTestResponse(req, http.StatusNotFound, `{}`), nil
		}
	})

	chapter, err := site.FetchChapter(context.Background(), "201", model.Chapter{ID: "44", Title: "版权信息"})
	if err != nil {
		t.Fatalf("FetchChapter: %v", err)
	}
	if !chapter.Downloaded {
		t.Fatal("chapter should be marked downloaded")
	}
	if !strings.Contains(chapter.Content, "版权信息") {
		t.Fatalf("unexpected chapter content: %q", firstRunes(chapter.Content, 80))
	}
	if strings.Contains(chapter.Content, "<") {
		t.Fatalf("content still contains markup: %q", firstRunes(chapter.Content, 80))
	}
	if sentBody["ps"] != "ps-test" {
		t.Fatalf("request body should carry psvts from the reader page, got %v", sentBody["ps"])
	}
	if sentBody["b"] != wereadEncodeID("201") {
		t.Fatalf("request body b should be encoded book id, got %v", sentBody["b"])
	}
	if sentBody["c"] != wereadEncodeID("44") {
		t.Fatalf("request body c should be encoded chapter uid, got %v", sentBody["c"])
	}
}

func TestWeReadFetchChapterReturnsPreviewFragmentAsLocked(t *testing.T) {
	preview := wereadBuildPayloadForTest("<html><body><p>只有一小段试读</p></body>")
	site := newWeReadTestSite(t, func(req *http.Request) (*http.Response, error) {
		if strings.HasPrefix(req.URL.Path, "/web/reader/") {
			return wereadTestResponse(req, http.StatusOK, wereadReaderPageStub()), nil
		}
		return wereadTestResponse(req, http.StatusOK, preview), nil
	})

	chapter, err := site.FetchChapter(context.Background(), "201", model.Chapter{ID: "45", Title: "付费章"})
	if err != nil {
		t.Fatalf("试读片段应照常返回内容以便阅读，实际报错: %v", err)
	}
	if !strings.Contains(chapter.Content, "试读") {
		t.Fatalf("unexpected preview content: %q", chapter.Content)
	}
	if !chapter.Locked {
		t.Fatal("试读片段必须标记 Locked，否则会被当成正文写入下载结果")
	}
	if chapter.Downloaded {
		t.Fatal("试读片段不应标记为已下载")
	}
}

func TestWeReadDownloadEndToEnd(t *testing.T) {
	payload, err := os.ReadFile("testdata/weread_copyright_payload.txt")
	if err != nil {
		t.Fatalf("read testdata: %v", err)
	}
	site := newWeReadTestSite(t, func(req *http.Request) (*http.Response, error) {
		if strings.HasPrefix(req.URL.Path, "/web/reader/") {
			return wereadTestResponse(req, http.StatusOK, wereadReaderPageStub()), nil
		}
		return wereadTestResponse(req, http.StatusOK, string(payload)), nil
	})

	book, err := site.Download(context.Background(), model.BookRef{BookID: "201"})
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	if len(book.Chapters) != 1 || !book.Chapters[0].Downloaded {
		t.Fatalf("unexpected downloaded chapters: %+v", book.Chapters)
	}
	if book.Chapters[0].Order != 1 {
		t.Fatalf("chapter order should be normalized, got %d", book.Chapters[0].Order)
	}
}

// wereadBuildPayloadForTest 造一个服务端格式的响应：md5 头 + 标记位 + 置换后的 base64。
func wereadBuildPayloadForTest(markup string) string {
	encoded := base64.StdEncoding.EncodeToString([]byte(markup))
	shuffled := wereadShuffleForTest(encoded)
	body := "P" + shuffled
	return strings.ToUpper(wereadMD5Hex(body)) + body
}

// wereadShuffleForTest 是 wereadUnshuffle 的逆操作（成对交换按相反顺序执行）。
func wereadShuffleForTest(payload string) string {
	keys := wereadShuffleKeys(payload)
	if len(keys) < 2 {
		return payload
	}
	chars := []byte(payload)
	for k := 1; k < len(keys); k += 2 {
		for j := 0; j <= 1; j++ {
			a := keys[k-1] + j
			b := keys[k] + j
			if a < 0 || b < 0 || a >= len(chars) || b >= len(chars) {
				continue
			}
			chars[a], chars[b] = chars[b], chars[a]
		}
	}
	return string(chars)
}
