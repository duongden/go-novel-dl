package site

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/guohuiyuan/go-novel-dl/internal/config"
	"github.com/guohuiyuan/go-novel-dl/internal/model"
)

func TestFanqieParseDirectoryFlattensAndSorts(t *testing.T) {
	body := []byte(`{
		"data": {
			"volumeNameList": ["第一卷：戏中人", "第二卷"],
			"chapterListWithVolume": [
				[
					{"itemId":"b","title":"第2章","realChapterOrder":"2"},
					{"itemId":"a","title":"第1章","realChapterOrder":"1"}
				],
				[
					{"itemId":"d","title":"第4章","realChapterOrder":"4"},
					{"itemId":"c","title":"第3章","realChapterOrder":"3"}
				]
			]
		},
		"code": 0,
		"message": "success"
	}`)

	site := &FanqieNovelSite{}
	chapters, err := site.parseDirectory(body)
	if err != nil {
		t.Fatalf("parseDirectory returned error: %v", err)
	}
	if len(chapters) != 4 {
		t.Fatalf("expected 4 chapters, got %d", len(chapters))
	}
	if chapters[0].ID != "a" || chapters[0].Order != 1 {
		t.Fatalf("first chapter mismatch: %+v", chapters[0])
	}
	if chapters[3].ID != "d" || chapters[3].Order != 4 {
		t.Fatalf("last chapter mismatch: %+v", chapters[3])
	}
	if chapters[0].Volume != "第一卷：戏中人" {
		t.Fatalf("unexpected volume: %q", chapters[0].Volume)
	}
	if chapters[2].Volume != "第二卷" {
		t.Fatalf("unexpected second volume: %q", chapters[2].Volume)
	}
}

func TestFanqieParseDirectoryRejectsBadCode(t *testing.T) {
	body := []byte(`{"data":{"chapterListWithVolume":[]},"code":7,"message":"boom"}`)
	site := &FanqieNovelSite{}
	if _, err := site.parseDirectory(body); err == nil {
		t.Fatal("expected error for non-zero code")
	}
}

func TestFanqieParseDirectorySupportsAlternateAndRecursiveShapes(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{
			name: "nested flat list",
			body: `{
				"data":{"data":{"items":[
					{"catalog_id":"12","item_id":"item-12","chapter_order":"2","chapter_title":"Second"},
					{"catalog_id":"11","chapterId":"item-11","real_chapter_order":"1","chapterName":"First"},
					{"catalog_id":"12","item_id":"item-12","chapter_order":"2","chapter_title":"Second duplicate"}
				]}},
				"code":200
			}`,
		},
		{
			name: "recursive chapter array",
			body: `{
				"data":{"payload":{"catalog_data":{"list":[
					{"chapter_id":"21","chapter_order":"2","chapter_name":"Second"},
					{"chapter_id":"20","chapter_order":"1","chapter_name":"First"},
					{"chapter_id":"21","chapter_order":"2","chapter_name":"Duplicate"}
				]}}},
				"code":0
			}`,
		},
	}

	site := &FanqieNovelSite{}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			chapters, err := site.parseDirectory([]byte(tt.body))
			if err != nil {
				t.Fatalf("parseDirectory: %v", err)
			}
			if len(chapters) != 2 {
				t.Fatalf("expected two deduplicated chapters, got %+v", chapters)
			}
			if chapters[0].Title != "First" || chapters[1].Title != "Second" {
				t.Fatalf("unexpected chapter order: %+v", chapters)
			}
		})
	}
}

func TestFanqieExtractBookMetadataSupportsJSONLDArrayAndImages(t *testing.T) {
	markup := `<html><head>
		<script type="application/ld+json">
		{
			"@type":["Book","Product"],
			"name":"JSON-LD Book",
			"author":{"name":"JSON-LD Author"},
			"description":"JSON-LD description",
			"genre":["奇幻","冒险"],
			"images":["https://img.example/cover.jpg"]
		}
		</script>
	</head></html>`

	page, err := extractFanqieBookMetadata(markup)
	if err != nil {
		t.Fatalf("extractFanqieBookMetadata: %v", err)
	}
	if got := stringValue(page["name"]); got != "JSON-LD Book" {
		t.Fatalf("unexpected title: %q", got)
	}
	if got := fanqiePageCover(page); got != "https://img.example/cover.jpg" {
		t.Fatalf("unexpected cover: %q", got)
	}
	if tags := fanqiePageTags(page); strings.Join(tags, ",") != "奇幻,冒险" {
		t.Fatalf("unexpected tags: %v", tags)
	}
}

func TestFanqieDownloadPlanUsesNextDataAndCompleteDirectory(t *testing.T) {
	var pageRequests, directoryRequests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/page/123":
			pageRequests++
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = fmt.Fprint(w, `<html><body><script id="__NEXT_DATA__" type="application/json">
				{"props":{"pageProps":{"book":{
					"bookName":"Next Book","authorName":"Next Author",
					"abstract":"Next description","thumbUri":"novel-pic/cover.jpg",
					"tagNames":["奇幻","冒险"]
				}}}}
			</script></body></html>`)
		case "/directory":
			directoryRequests++
			if got := r.URL.Query().Get("bookId"); got != "123" {
				t.Errorf("unexpected bookId query: %q", got)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprint(w, `{"code":0,"data":{"chapterList":[
				{"itemId":"chapter-2","title":"第二章","realChapterOrder":"2"},
				{"itemId":"chapter-1","title":"第一章","realChapterOrder":"1"}
			]}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	cfg := config.DefaultConfig()
	cfg.General.CacheDir = t.TempDir()
	site := NewFanqieNovelSite(cfg.ResolveSiteConfig("fanqienovel"))
	site.client = server.Client()
	site.html = NewHTMLSite(site.client)
	site.directoryURL = server.URL + "/directory"
	site.directoryInterval = time.Nanosecond
	site.client.Transport = rewriteFanqieTestURLs(server.URL, site.client.Transport)

	book, err := site.DownloadPlan(context.Background(), model.BookRef{BookID: "123"})
	if err != nil {
		t.Fatalf("DownloadPlan: %v", err)
	}
	if book.Title != "Next Book" || book.Author != "Next Author" || book.Description != "Next description" {
		t.Fatalf("unexpected metadata: %+v", book)
	}
	if book.CoverURL != "https://p3-reading-sign.fqnovelpic.com/novel-pic/cover.jpg" {
		t.Fatalf("unexpected cover: %q", book.CoverURL)
	}
	if strings.Join(book.Tags, ",") != "奇幻,冒险" {
		t.Fatalf("unexpected tags: %v", book.Tags)
	}
	if len(book.Chapters) != 2 || book.Chapters[0].ID != "chapter-1" || book.Chapters[1].ID != "chapter-2" {
		t.Fatalf("unexpected chapters: %+v", book.Chapters)
	}
	if pageRequests != 1 || directoryRequests != 1 {
		t.Fatalf("unexpected request counts: page=%d directory=%d", pageRequests, directoryRequests)
	}
}

func TestFanqieDirectoryWarmsUpAfterForbiddenAndFallsBackToCache(t *testing.T) {
	var directoryCalls, warmupCalls int
	failDirectory := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/page/321":
			warmupCalls++
			w.Header().Set("Content-Type", "text/html")
			_, _ = fmt.Fprint(w, `<html><body>warm</body></html>`)
		case "/directory":
			directoryCalls++
			if failDirectory {
				http.NotFound(w, r)
				return
			}
			if directoryCalls == 1 {
				http.Error(w, "forbidden", http.StatusForbidden)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprint(w, `{"code":0,"data":{"chapterList":[
				{"itemId":"chapter-1","title":"第一章","realChapterOrder":"1"}
			]}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	cfg := config.DefaultConfig()
	cfg.General.CacheDir = t.TempDir()
	site := NewFanqieNovelSite(cfg.ResolveSiteConfig("fanqienovel"))
	site.client = server.Client()
	site.client.Transport = rewriteFanqieTestURLs(server.URL, site.client.Transport)
	site.html = NewHTMLSite(site.client)
	site.directoryURL = server.URL + "/directory"
	site.directoryInterval = time.Nanosecond

	chapters, err := site.fetchDirectory(context.Background(), "321")
	if err != nil {
		t.Fatalf("first fetchDirectory: %v", err)
	}
	if len(chapters) != 1 || warmupCalls == 0 {
		t.Fatalf("expected warmup and one chapter, chapters=%+v warmups=%d", chapters, warmupCalls)
	}
	if directoryCalls != 2 {
		t.Fatalf("expected forbidden request followed by retry, got %d calls", directoryCalls)
	}

	failDirectory = true
	before := directoryCalls
	chapters, err = site.fetchDirectory(context.Background(), "321")
	if err != nil {
		t.Fatalf("cached fetchDirectory: %v", err)
	}
	if len(chapters) != 1 || directoryCalls != before+1 {
		t.Fatalf("expected cached fallback after one failed request, chapters=%+v calls=%d", chapters, directoryCalls-before)
	}
}

func TestFanqieDirectoryDisableCacheDoesNotWrite(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"code":0,"data":{"chapterList":[
			{"itemId":"chapter-1","title":"第一章","realChapterOrder":"1"}
		]}}`)
	}))
	defer server.Close()

	cfg := config.DefaultConfig()
	cfg.General.CacheDir = t.TempDir()
	cfg.General.DisableCache = true
	site := NewFanqieNovelSite(cfg.ResolveSiteConfig("fanqienovel"))
	site.client = server.Client()
	site.html = NewHTMLSite(site.client)
	site.directoryURL = server.URL
	site.directoryInterval = time.Nanosecond

	if _, err := site.fetchDirectory(context.Background(), "654"); err != nil {
		t.Fatalf("fetchDirectory: %v", err)
	}
	if _, err := os.Stat(site.directoryCachePath("654")); !os.IsNotExist(err) {
		t.Fatalf("cache should not be written when disabled, got %v", err)
	}
}

func TestFanqieAtomicCacheReplacesExistingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "directory.json")
	if err := writeAtomicSiteCache(path, []byte(`{"version":1}`)); err != nil {
		t.Fatalf("first cache write: %v", err)
	}
	if err := writeAtomicSiteCache(path, []byte(`{"version":2}`)); err != nil {
		t.Fatalf("second cache write: %v", err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read cache: %v", err)
	}
	if string(body) != `{"version":2}` {
		t.Fatalf("cache was not replaced: %q", body)
	}
}

func TestFanqieDiscoveryRoutesRankingsAndDecodesBooks(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/rank":
			w.Header().Set("Content-Type", "text/html")
			_, _ = fmt.Fprint(w, `<script>window.__INITIAL_STATE__ = {
				"rank":{"book_list":[{"bookId":"rank-1","bookName":"Rank Book","author":"Rank Author"}]}
			};</script>`)
		case "/recommend":
			if r.URL.Query().Get("type") != "1" || r.URL.Query().Get("limit") != "30" {
				t.Errorf("unexpected recommendation query: %s", r.URL.RawQuery)
			}
			_, _ = fmt.Fprint(w, `{"code":0,"data":{"list":[
				{"bookId":"rec-1","bookName":"Recommend Book","author":"Rec Author"},
				{"bookId":"rec-1","bookName":"Duplicate"}
			]}}`)
		case "/category":
			for key, want := range map[string]string{
				"app_id": "2503", "rank_list_type": "3", "category_id": "1141",
				"gender": "1", "rankMold": "2", "limit": "30",
			} {
				if got := r.URL.Query().Get(key); got != want {
					t.Errorf("query %s = %q, want %q", key, got, want)
				}
			}
			_, _ = fmt.Fprint(w, `{"code":0,"data":{"book_list":[
				{"bookId":"cat-1","bookName":"Category Book","author":"Cat Author","thumbUri":"covers/cat.jpg"}
			]}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	site := &FanqieNovelSite{
		client:           server.Client(),
		html:             NewHTMLSite(server.Client()),
		rankPageURL:      server.URL + "/rank",
		rankRecommendURL: server.URL + "/recommend",
		rankCategoryURL:  server.URL + "/category",
	}

	lists, err := site.DiscoveryLists(context.Background())
	if err != nil {
		t.Fatalf("DiscoveryLists: %v", err)
	}
	if len(lists) != len(fanqieDiscoverySpecs) || len(lists) != 7 {
		t.Fatalf("unexpected discovery lists: %+v", lists)
	}

	tests := []struct {
		listID string
		wantID string
	}{
		{listID: "all", wantID: "rank-1"},
		{listID: "recommend_1", wantID: "rec-1"},
		{listID: "male_new_1141", wantID: "cat-1"},
	}
	for _, tt := range tests {
		t.Run(tt.listID, func(t *testing.T) {
			books, err := site.DiscoveryBooks(context.Background(), tt.listID, 30)
			if err != nil {
				t.Fatalf("DiscoveryBooks(%s): %v", tt.listID, err)
			}
			if len(books) != 1 || books[0].BookID != tt.wantID {
				t.Fatalf("unexpected books for %s: %+v", tt.listID, books)
			}
		})
	}
}

func rewriteFanqieTestURLs(serverURL string, base http.RoundTripper) http.RoundTripper {
	if base == nil {
		base = http.DefaultTransport
	}
	return roundTripFunc(func(req *http.Request) (*http.Response, error) {
		clone := req.Clone(req.Context())
		clone.URL.Scheme = "http"
		clone.URL.Host = strings.TrimPrefix(serverURL, "http://")
		return base.RoundTrip(clone)
	})
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}

func TestFanqieChapterNumber(t *testing.T) {
	if got := fanqieChapterNumber("1928"); got != 1928 {
		t.Fatalf("expected 1928, got %d", got)
	}
	if got := fanqieChapterNumber(""); got != 0 {
		t.Fatalf("expected 0 for empty, got %d", got)
	}
	if got := fanqieChapterNumber("abc"); got != 0 {
		t.Fatalf("expected 0 for non-numeric, got %d", got)
	}
}

func TestFanqieSearchParsesResults(t *testing.T) {
	var gotQuery, gotAID, gotOffset string
	var gotAccept, gotReferer, gotFetchSite, gotUserAgent string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/novel/channel/homepage/search/search/v1/" {
			t.Errorf("unexpected search path: %s", r.URL.Path)
		}
		gotQuery = r.URL.Query().Get("q")
		gotAID = r.URL.Query().Get("aid")
		gotOffset = r.URL.Query().Get("offset")
		gotAccept = r.Header.Get("Accept")
		gotReferer = r.Header.Get("Referer")
		gotFetchSite = r.Header.Get("Sec-Fetch-Site")
		gotUserAgent = r.Header.Get("User-Agent")
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{
			"code": 0,
			"data": {
				"has_more": false,
				"offset": 10,
				"ret_data": [
					{"book_id":"123","title":"Test Book","author":"Auth","abstract":"Desc","thumb_url":"http://img"},
					{"book_id":"456","title":"Second","author":"B","thumb_url":"covers/second.jpg"}
				]
			}
		}`)
	}))
	defer server.Close()

	site := &FanqieNovelSite{
		cfg:       config.DefaultConfig().ResolveSiteConfig("fanqienovel"),
		client:    server.Client(),
		searchURL: server.URL + "/api/novel/channel/homepage/search/search/v1/",
	}
	results, err := site.Search(context.Background(), "hello", 5)
	if err != nil {
		t.Fatalf("Search returned error: %v", err)
	}
	if gotQuery != "hello" {
		t.Fatalf("unexpected q: %q", gotQuery)
	}
	if gotAID != "1967" || gotOffset != "0" {
		t.Fatalf("unexpected official search params: aid=%q offset=%q", gotAID, gotOffset)
	}
	if gotAccept == "" || gotUserAgent != fanqieSearchUserAgent || gotReferer != "" || gotFetchSite != "" {
		t.Fatalf("unexpected official search headers: accept=%q user-agent=%q referer=%q fetch-site=%q", gotAccept, gotUserAgent, gotReferer, gotFetchSite)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	if results[0].BookID != "123" || results[0].Title != "Test Book" || results[0].Author != "Auth" {
		t.Fatalf("unexpected first result: %+v", results[0])
	}
	if results[0].URL != "https://fanqienovel.com/page/123" {
		t.Fatalf("unexpected URL: %q", results[0].URL)
	}
	if results[0].Description != "Desc" {
		t.Fatalf("unexpected description: %q", results[0].Description)
	}
	if results[1].CoverURL != "https://p3-reading-sign.fqnovelpic.com/covers/second.jpg" {
		t.Fatalf("unexpected cover: %q", results[1].CoverURL)
	}
}

func TestFanqieSearchHonorsLimit(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{
			"code": 0,
			"data": {
				"has_more": false,
				"offset": 10,
				"ret_data": [
					{"book_id":"1","title":"A"},
					{"book_id":"2","title":"B"},
					{"book_id":"3","title":"C"}
				]
			}
		}`)
	}))
	defer server.Close()

	site := &FanqieNovelSite{client: server.Client(), searchURL: server.URL}
	results, err := site.Search(context.Background(), "x", 2)
	if err != nil {
		t.Fatalf("Search returned error: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
}

func TestFanqieSearchUsesResponseOffsetForPagination(t *testing.T) {
	var offsets []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		offset := r.URL.Query().Get("offset")
		offsets = append(offsets, offset)
		start, count, hasMore, nextOffset := 0, 10, true, 10
		if offset == "10" {
			start, count, hasMore, nextOffset = 10, 11, false, 20
		}
		books := make([]string, 0, count-start)
		for i := start; i < count; i++ {
			books = append(books, fmt.Sprintf(`{"book_id":"%d","title":"Book %d"}`, i+1, i+1))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"code":0,"data":{"has_more":%t,"offset":%d,"ret_data":[%s]}}`,
			hasMore, nextOffset, strings.Join(books, ","))
	}))
	defer server.Close()

	site := &FanqieNovelSite{client: server.Client(), searchURL: server.URL}
	results, err := site.Search(context.Background(), "page", 11)
	if err != nil {
		t.Fatalf("Search returned error: %v", err)
	}
	if got := strings.Join(offsets, ","); got != "0,10" {
		t.Fatalf("unexpected search offsets: %q", got)
	}
	if len(results) != 11 {
		t.Fatalf("expected 11 results, got %d", len(results))
	}
}

func TestFanqieSearchFallsBackToAlternateEndpoint(t *testing.T) {
	primaryCalls := 0
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		primaryCalls++
		w.WriteHeader(http.StatusOK)
	}))
	defer primary.Close()

	fallbackCalls := 0
	fallback := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fallbackCalls++
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"code":0,"data":{"has_more":false,"offset":10,"ret_data":[{"book_id":"fallback-book","title":"Fallback"}]}}`)
	}))
	defer fallback.Close()

	site := &FanqieNovelSite{
		client:            primary.Client(),
		searchURL:         primary.URL,
		searchFallbackURL: fallback.URL,
	}
	results, err := site.Search(context.Background(), "fallback", 1)
	if err != nil {
		t.Fatalf("Search returned error: %v", err)
	}
	if primaryCalls != 1 || fallbackCalls != 1 {
		t.Fatalf("unexpected endpoint calls: primary=%d fallback=%d", primaryCalls, fallbackCalls)
	}
	if len(results) != 1 || results[0].BookID != "fallback-book" {
		t.Fatalf("unexpected fallback results: %+v", results)
	}
}

func TestFanqieSearchReturnsEmptyWhenNoMoreResults(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"code":0,"data":{"has_more":false,"offset":0}}`)
	}))
	defer server.Close()

	site := &FanqieNovelSite{client: server.Client(), searchURL: server.URL}
	results, err := site.Search(context.Background(), "missing", 10)
	if err != nil {
		t.Fatalf("Search returned error: %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("expected no results, got %+v", results)
	}
}

func TestFanqieSearchReportsUnavailableWithoutLeakingCookie(t *testing.T) {
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer primary.Close()
	fallback := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer fallback.Close()

	cfg := config.DefaultConfig().ResolveSiteConfig("fanqienovel")
	cfg.Cookie = "sessionid=top-secret-cookie"
	site := &FanqieNovelSite{
		cfg:               cfg,
		client:            primary.Client(),
		searchURL:         primary.URL,
		searchFallbackURL: fallback.URL,
	}
	_, err := site.Search(context.Background(), "hello", 5)
	if err == nil {
		t.Fatal("expected unavailable error")
	}
	if !strings.Contains(err.Error(), "暂时不可用") || !strings.Contains(err.Error(), "空响应") {
		t.Fatalf("error should explain that the official search is unavailable: %v", err)
	}
	if strings.Contains(strings.ToLower(err.Error()), "cookie") || strings.Contains(err.Error(), "top-secret-cookie") {
		t.Fatalf("error leaked configured Cookie: %v", err)
	}
}

func TestFanqieFetchChapterUsesOfficialReaderState(t *testing.T) {
	var gotReferer string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/reader/123" {
			http.NotFound(w, r)
			return
		}
		gotReferer = r.Header.Get("Referer")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = fmt.Fprint(w, `<html><body><script>
			window.__INITIAL_STATE__={"reader":{"chapterData":{
				"bookId":"book-1",
				"title":"Official Chapter",
				"isChapterLock":false,
				"needPay":0,
				"content":"\u003Cp\u003EHello\u003C\u002Fp\u003E\u003Cp\u003EWorld\u003C\u002Fp\u003E"
			}}};
		</script></body></html>`)
	}))
	defer server.Close()

	client := server.Client()
	client.Transport = rewriteFanqieTestURLs(server.URL, client.Transport)
	site := &FanqieNovelSite{client: client, html: NewHTMLSite(client)}
	chapter, err := site.FetchChapter(context.Background(), "book-1", model.Chapter{ID: "123"})
	if err != nil {
		t.Fatalf("FetchChapter: %v", err)
	}
	if chapter.Content != "Hello\nWorld" {
		t.Fatalf("unexpected content: %q", chapter.Content)
	}
	if chapter.Title != "Official Chapter" {
		t.Fatalf("unexpected title: %q", chapter.Title)
	}
	if gotReferer != "https://fanqienovel.com/page/book-1" {
		t.Fatalf("unexpected reader referer: %q", gotReferer)
	}
	if !chapter.Downloaded || chapter.URL != "https://fanqienovel.com/reader/123" {
		t.Fatalf("unexpected downloaded chapter: %+v", chapter)
	}
}

func TestFanqieFetchChapterRejectsLockedContent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = fmt.Fprint(w, `<html><body><script>
			window.__INITIAL_STATE__={"reader":{"chapterData":{
				"bookId":"book-1",
				"isChapterLock":true,
				"needPay":1,
				"content":"\u003Cp\u003EPreview only\u003C\u002Fp\u003E"
			}}};
		</script></body></html>`)
	}))
	defer server.Close()

	client := server.Client()
	client.Transport = rewriteFanqieTestURLs(server.URL, client.Transport)
	site := &FanqieNovelSite{client: client, html: NewHTMLSite(client)}
	chapter, err := site.FetchChapter(context.Background(), "book-1", model.Chapter{ID: "456"})
	if err == nil {
		t.Fatal("expected locked chapter error")
	}
	if chapter.Content != "" || chapter.Downloaded {
		t.Fatalf("locked chapter must not be treated as downloaded: %+v", chapter)
	}
	if !strings.Contains(err.Error(), "已锁定") || !strings.Contains(err.Error(), "Cookie") {
		t.Fatalf("unexpected locked chapter error: %v", err)
	}
}

func TestFanqieDownloadUsesOfficialPlanAndReader(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/page/1001":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = fmt.Fprint(w, `<html><body><script>
				window.__INITIAL_STATE__={"page":{"bookName":"Official Download","authorName":"Author","abstract":"Description"}};
			</script></body></html>`)
		case "/api/reader/directory/detail":
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprint(w, `{"code":0,"data":{"chapterList":[
				{"itemId":"2","title":"Second","realChapterOrder":"2"},
				{"itemId":"1","title":"First","realChapterOrder":"1"}
			]}}`)
		case "/reader/1", "/reader/2":
			itemID := strings.TrimPrefix(r.URL.Path, "/reader/")
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = fmt.Fprintf(w, `<html><body><script>
				window.__INITIAL_STATE__={"reader":{"chapterData":{
					"bookId":"1001","isChapterLock":false,"needPay":0,
					"content":"\u003Cp\u003EContent %s\u003C\u002Fp\u003E"
				}}};
			</script></body></html>`, itemID)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	cfg := config.DefaultConfig()
	cfg.General.CacheDir = t.TempDir()
	site := NewFanqieNovelSite(cfg.ResolveSiteConfig("fanqienovel"))
	site.client.Transport = rewriteFanqieTestURLs(server.URL, site.client.Transport)
	site.html = NewHTMLSite(site.client)
	site.directoryInterval = time.Nanosecond

	book, err := site.Download(context.Background(), model.BookRef{BookID: "1001"})
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	if book.Title != "Official Download" || len(book.Chapters) != 2 {
		t.Fatalf("unexpected book: %+v", book)
	}
	if book.Chapters[0].Content != "Content 1" || book.Chapters[1].Content != "Content 2" {
		t.Fatalf("unexpected downloaded content: %+v", book.Chapters)
	}
	for _, chapter := range book.Chapters {
		if !chapter.Downloaded || !strings.HasPrefix(chapter.URL, "https://fanqienovel.com/reader/") {
			t.Fatalf("chapter was not loaded from the official reader: %+v", chapter)
		}
	}
}

func TestFanqieCookieIsScopedToOfficialDomain(t *testing.T) {
	cfg := config.DefaultConfig().ResolveSiteConfig("fanqienovel")
	cfg.Cookie = "Cookie: sessionid=abc; ttwid=def"
	site := NewFanqieNovelSite(cfg)

	official, err := url.Parse("https://fanqienovel.com/reader/123")
	if err != nil {
		t.Fatal(err)
	}
	other, err := url.Parse("https://example.com/")
	if err != nil {
		t.Fatal(err)
	}
	search, err := url.Parse(fanqieSearchAPI)
	if err != nil {
		t.Fatal(err)
	}
	fallback, err := url.Parse(fanqieSearchFallbackAPI)
	if err != nil {
		t.Fatal(err)
	}
	if cookies := site.client.Jar.Cookies(official); len(cookies) != 2 {
		t.Fatalf("expected two official-domain cookies, got %v", cookies)
	}
	if cookies := site.client.Jar.Cookies(other); len(cookies) != 0 {
		t.Fatalf("Cookie leaked outside fanqienovel.com: %v", cookies)
	}
	if cookies := site.client.Jar.Cookies(search); len(cookies) != 0 {
		t.Fatalf("Cookie leaked to the search endpoint: %v", cookies)
	}
	if cookies := site.client.Jar.Cookies(fallback); len(cookies) != 0 {
		t.Fatalf("Cookie leaked to the fallback endpoint: %v", cookies)
	}
}
