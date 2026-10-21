package site

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"testing"
)

func TestParseN17KSearchAPIResponse(t *testing.T) {
	payload := `{
  "status": {"code": 0, "msg": "succ"},
  "data": {
    "hasMore": true,
    "page": "1",
    "totalNum": 2,
    "lists": [
      {
        "id": 3597196,
        "bookName": "Book One",
        "authorPenName": "Author One",
        "introduction": "First introduction",
        "coverImg": "//cdn.example/one.jpg",
        "lastUpdateChapterName": "Chapter 20",
        "bookCategory": {"name": "Fantasy"},
        "bookChannel": {"name": "Male"}
      },
      {
        "id": "3597197",
        "bookName": "Book Two",
        "authorPenName": "Author Two",
        "bookCategory": {"name": "History"},
        "bookChannel": {"name": "Male"}
      }
    ]
  }
}`
	response, err := parseN17KSearchAPIResponse(payload)
	if err != nil {
		t.Fatalf("parse 17k search api: %v", err)
	}
	if response.Status.Code != 0 || !response.Data.HasMore || len(response.Data.Lists) != 2 {
		t.Fatalf("unexpected search response: %+v", response)
	}
	first := response.Data.Lists[0]
	if got := n17kSearchAPIBookID(first.ID); got != "3597196" {
		t.Fatalf("unexpected numeric book id: %q", got)
	}
	if got := n17kSearchAPIBookID(response.Data.Lists[1].ID); got != "3597197" {
		t.Fatalf("unexpected string book id: %q", got)
	}
	if got := n17kSearchDescription(response.Data.Lists[1]); got != "Male · History" {
		t.Fatalf("unexpected category fallback description: %q", got)
	}
}

func TestLoadN17KSearchResultsPaginatesAndDeduplicates(t *testing.T) {
	pageOne := `{
  "status": {"code": 0, "msg": "succ"},
  "data": {
    "hasMore": true,
    "lists": [
      {"id":1,"bookName":"Book One","authorPenName":"Author One","introduction":"One"},
      {"id":2,"bookName":"Book Two","authorPenName":"Author Two","introduction":"Two"},
      {"id":1,"bookName":"Duplicate","authorPenName":"Author One"}
    ]
  }
}`
	pageTwo := `{
  "status": {"code": 0, "msg": "succ"},
  "data": {
    "hasMore": false,
    "lists": [
      {"id":2,"bookName":"Duplicate","authorPenName":"Author Two"},
      {"id":3,"bookName":"Book Three","authorPenName":"Author Three","introduction":"Three"}
    ]
  }
}`
	calls := make([]string, 0, 2)
	fetch := func(_ context.Context, rawURL string) (string, error) {
		calls = append(calls, rawURL)
		parsed, err := url.Parse(rawURL)
		if err != nil {
			t.Fatalf("parse search URL: %v", err)
		}
		switch parsed.Query().Get("page") {
		case "1":
			return pageOne, nil
		case "2":
			return pageTwo, nil
		default:
			t.Fatalf("unexpected search page URL: %s", rawURL)
			return "", nil
		}
	}

	results, err := loadN17KSearchResults(context.Background(), fetch, "Test Book", 3)
	if err != nil {
		t.Fatalf("load 17k search api: %v", err)
	}
	if len(results) != 3 {
		t.Fatalf("expected three deduplicated results, got %d: %+v", len(results), results)
	}
	if results[0].BookID != "1" || results[1].BookID != "2" || results[2].BookID != "3" {
		t.Fatalf("unexpected result order: %+v", results)
	}
	if results[2].Site != "n17k" || results[2].URL != "https://www.17k.com/book/3.html" {
		t.Fatalf("unexpected result metadata: %+v", results[2])
	}
	if len(calls) != 2 {
		t.Fatalf("expected two search pages, got %v", calls)
	}
}

func TestLoadN17KSearchResultsStopsWhenAPIHasNoMatches(t *testing.T) {
	payload := `{"status":{"code":0,"msg":"succ"},"data":{"hasMore":false,"lists":[]}}`
	calls := 0
	fetch := func(context.Context, string) (string, error) {
		calls++
		return payload, nil
	}

	results, err := loadN17KSearchResults(context.Background(), fetch, "missing", 10)
	if err != nil {
		t.Fatalf("empty 17k search should be a successful result: %v", err)
	}
	if len(results) != 0 || calls != 1 {
		t.Fatalf("unexpected empty search result: results=%+v calls=%d", results, calls)
	}
}

func TestParseN17KSearchAPIResponseRejectsFailures(t *testing.T) {
	tests := []struct {
		name    string
		payload string
	}{
		{name: "api status", payload: `{"status":{"code":401,"msg":"not logged in"},"data":{}}`},
		{name: "aliyun challenge", payload: `<html><meta name="aliyun_waf_aa"></html>`},
		{name: "legacy challenge", payload: `<script>var arg1 = '0123456789ABCDEF';</script>`},
		{name: "render challenge", payload: `<html><script>window.renderData = {};</script></html>`},
		{name: "invalid json", payload: `{"status":`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := parseN17KSearchAPIResponse(test.payload); err == nil {
				t.Fatalf("expected payload to be rejected: %q", test.payload)
			}
		})
	}
}

func TestN17KSearchAPIURL(t *testing.T) {
	rawURL := n17kSearchAPIURL("A&B", 2)
	parsed, err := url.Parse(rawURL)
	if err != nil {
		t.Fatalf("parse search URL: %v", err)
	}
	query := parsed.Query()
	for key, expected := range map[string]string{
		"appKey":          "1351550300",
		"page":            "2",
		"fuzzySearchType": "1",
		"keyword":         "A&B",
	} {
		if got := query.Get(key); got != expected {
			t.Fatalf("unexpected %s query: got %q want %q", key, got, expected)
		}
	}
}

func TestLoadN17KSearchResultsReturnsFetchError(t *testing.T) {
	expected := errors.New("network down")
	fetch := func(context.Context, string) (string, error) {
		return "", expected
	}
	_, err := loadN17KSearchResults(context.Background(), fetch, "book", 10)
	if !errors.Is(err, expected) {
		t.Fatalf("expected fetch error, got %v", err)
	}
}

func TestN17KLooksLikeWAFChallenge(t *testing.T) {
	for _, payload := range []string{
		`<meta name="aliyun_waf_aa">`,
		`<script>var arg1 = '0123456789ABCDEF';</script>`,
		`<html><script>renderData = {}</script></html>`,
	} {
		if !n17kLooksLikeWAFChallenge(payload) {
			t.Fatalf("challenge was not detected: %s", strings.TrimSpace(payload))
		}
	}
	if n17kLooksLikeWAFChallenge(`{"status":{"code":0},"data":[]}`) {
		t.Fatal("valid JSON was treated as a challenge")
	}
}
