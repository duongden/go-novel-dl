package site

import (
	"github.com/guohuiyuan/go-novel-dl/internal/config"
	"strings"
	"testing"
)

// 向量的期望值来自网页端混淆代码的真实输出，改动算法会立刻打破这些断言。
func TestWereadEncodeID(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"3300133181", "6d832b40813ab9a86g01102d"},
		{"44", "f7132c6022cf7177163c01c"},
		{"695233", "ce032b305a9bc1ce0b0dd2a"},
		{"90", "861322a025a8613985ec87a"},
		{"123456789012345", "58b32e60775bcd15g043039133"},
		{"abc", "900427206616263900156d7"},
		{"1000000000", "a4b32db075f5e100g010bcf"},
	}
	for _, tc := range cases {
		if got := wereadEncodeID(tc.input); got != tc.want {
			t.Errorf("wereadEncodeID(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
	if got := wereadEncodeID(""); got != "" {
		t.Errorf("wereadEncodeID(\"\") = %q, want empty", got)
	}
}

func TestWereadSignQuery(t *testing.T) {
	params := map[string]string{
		"b":  "6d832b40813ab9a86g01102d",
		"c":  "f7132c6022cf7177163c01c",
		"ct": "1729059593",
		"pc": "3c8329407a4e55e7g012bed",
		"ps": "9cd327207aaf7dd6g013158",
		"r":  "3448449",
		"sc": "0",
		"st": "0",
	}
	if got := wereadSignQuery(params); got != "acb8bb46" {
		t.Fatalf("wereadSignQuery = %q, want acb8bb46", got)
	}

	// 键顺序不应影响结果。
	reordered := map[string]string{}
	for key, value := range params {
		reordered[key] = value
	}
	if got := wereadSignQuery(reordered); got != "acb8bb46" {
		t.Fatalf("signature must not depend on map order, got %q", got)
	}
}

func TestWereadEscapeComponentMatchesEncodeURIComponent(t *testing.T) {
	cases := map[string]string{
		"abcXYZ0189": "abcXYZ0189",
		"-_.!~*'()":  "-_.!~*'()",
		"a b":        "a%20b",
		"a&b=c":      "a%26b%3Dc",
		"中文":         "%E4%B8%AD%E6%96%87",
	}
	for input, want := range cases {
		if got := wereadEscapeComponent(input); got != want {
			t.Errorf("wereadEscapeComponent(%q) = %q, want %q", input, got, want)
		}
	}
}

// 微信读书的部分章节写成 <head><title/></head>。HTML 词法把自闭合 title 当成
// RCDATA 起点，没有 </title> 就会把整篇正文吞掉，这一页曾经因此报“解码后没有正文”。
func TestWereadHTMLToTextHandlesSelfClosingTitle(t *testing.T) {
	markup := `<html xmlns="http://www.w3.org/1999/xhtml"><head>` +
		`<meta name="wr-page-type" content="copyright"/><title/>` +
		`<link href="../Styles/stylesheets.css" rel="stylesheet" type="text/css"/>` +
		`</head><body><h1 class="copyRightTitle">版权信息</h1>` +
		`<p class="contentCR">书名：测试书名</p><p class="contentCR">作者：测试作者</p>` +
		`</body></html>`
	got := wereadHTMLToText(markup)
	for _, want := range []string{"版权信息", "书名：测试书名", "作者：测试作者"} {
		if !strings.Contains(got, want) {
			t.Fatalf("text missing %q, got %q", want, firstRunes(got, 160))
		}
	}
	if got != "版权信息\n\n书名：测试书名\n\n作者：测试作者" {
		t.Fatalf("expected one paragraph per block, got %q", got)
	}
}

func TestWereadHTMLToTextKeepsBlockElements(t *testing.T) {
	markup := `<body><div class="title">书名</div><div>第一段</div><p>第二段<br/>换行</p><ul><li>条目一</li><li>条目二</li></ul></body>`
	got := wereadHTMLToText(markup)
	want := "书名\n\n第一段\n\n第二段\n\n换行\n\n条目一\n\n条目二"
	if got != want {
		t.Fatalf("wereadHTMLToText = %q, want %q", got, want)
	}
}

func TestWereadDecodeChapterPayloadHandlesEmptyShell(t *testing.T) {
	if _, err := wereadDecodeChapterPayload("{}"); err == nil ||
		!strings.Contains(err.Error(), "psvts") {
		t.Fatalf("expected explicit empty-shell error, got %v", err)
	}
}

func TestWereadResolveReaderID(t *testing.T) {
	got, err := wereadResolveReaderID("3300133181")
	if err != nil || got != "6d832b40813ab9a86g01102d" {
		t.Fatalf("numeric book id should be encoded, got %q err=%v", got, err)
	}
	got, err = wereadResolveReaderID("6d832b40813ab9a86g01102d")
	if err != nil || got != "6d832b40813ab9a86g01102d" {
		t.Fatalf("reader id should pass through, got %q err=%v", got, err)
	}
	if _, err := wereadResolveReaderID(""); err == nil {
		t.Fatal("empty id should fail")
	}
	if _, err := wereadResolveReaderID("has space"); err == nil {
		t.Fatal("invalid id should fail")
	}
}

func TestWereadChapterRequestBodySignsAllFields(t *testing.T) {
	body := wereadChapterRequestBody("3300133181", 44, "psvts-test")
	for _, key := range []string{"b", "c", "ct", "pc", "ps", "r", "sc", "st", "s"} {
		if strings.TrimSpace(body[key]) == "" {
			t.Fatalf("request body missing %q: %+v", key, body)
		}
	}
	if body["b"] != "6d832b40813ab9a86g01102d" {
		t.Fatalf("body b should be encoded book id, got %q", body["b"])
	}
	if body["c"] != "f7132c6022cf7177163c01c" {
		t.Fatalf("body c should be encoded chapter uid, got %q", body["c"])
	}
	unsigned := map[string]string{}
	for key, value := range body {
		if key != "s" {
			unsigned[key] = value
		}
	}
	if body["s"] != wereadSignQuery(unsigned) {
		t.Fatal("s must equal signature over the remaining params")
	}
}

func TestWereadParseInitialState(t *testing.T) {
	page := `<html><script>window.__INITIAL_STATE__={"reader":{"infoId":"rid","bookId":"201",` +
		`"psvts":"ps-1","bookInfo":{"bookId":"201","title":"T"},"chapterInfos":[{"chapterUid":7,"title":"C"}]}};</script></html>`
	state, err := wereadParseInitialState(page)
	if err != nil {
		t.Fatalf("wereadParseInitialState: %v", err)
	}
	if state.PSVTS != "ps-1" || len(state.ChapterInfos) != 1 || state.ChapterInfos[0].ChapterUID != 7 {
		t.Fatalf("unexpected state: %+v", state)
	}

	if _, err := wereadParseInitialState("<html></html>"); err == nil {
		t.Fatal("page without initial state should fail")
	}
}

func firstRunes(value string, n int) string {
	runes := []rune(value)
	if len(runes) <= n {
		return value
	}
	return string(runes[:n]) + "..."
}

func TestWereadResolveURL(t *testing.T) {
	site := NewWeReadSite(config.DefaultConfig().ResolveSiteConfig("weread"))

	resolved, ok := site.ResolveURL("https://weread.qq.com/web/reader/6d832b40813ab9a86g01102d")
	if !ok || resolved.SiteKey != "weread" || resolved.BookID != "6d832b40813ab9a86g01102d" {
		t.Fatalf("reader url should resolve: %+v ok=%v", resolved, ok)
	}

	if _, ok := site.ResolveURL("https://weread.qq.com/web/reader/6d832b40813ab9a86g01102d?foo=1"); !ok {
		t.Fatal("reader url with query should resolve")
	}
	if _, ok := site.ResolveURL("https://weread.qq.com/web/shelf"); ok {
		t.Fatal("non-reader path should not resolve")
	}
	if _, ok := site.ResolveURL("https://example.com/web/reader/6d832b40813ab9a86g01102d"); ok {
		t.Fatal("foreign host should not resolve")
	}
}
