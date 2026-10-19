package site

import "testing"

func TestExtractCommonBookTagsSupportsMetaLabelsAndTagContainers(t *testing.T) {
	doc, err := parseHTML(`<html><head>
		<meta property="og:novel:tags" content="奇幻，冒险">
	</head><body>
		<p>分类：<a href="/sort/1/">都市</a></p>
		<p>状态：连载中</p>
		<div class="book-tag-list"><a href="/tag/1"><em>#</em>重生</a><a href="/tag/2">#后宫</a></div>
	</body></html>`)
	if err != nil {
		t.Fatalf("parse markup: %v", err)
	}

	tags := extractCommonBookTags(doc)
	want := []string{"奇幻", "冒险", "都市", "重生", "后宫"}
	if len(tags) != len(want) {
		t.Fatalf("unexpected tags: %v", tags)
	}
	for idx := range want {
		if tags[idx] != want[idx] {
			t.Fatalf("tag %d: got %q, want %q (%v)", idx, tags[idx], want[idx], tags)
		}
	}
}

func TestExtractCommonBookTagsDeduplicatesAndSkipsStatus(t *testing.T) {
	doc, err := parseHTML(`<html><body>
		<div class="tags"><a>#都市</a><a>都市</a><a>已完结</a><a>重生</a></div>
	</body></html>`)
	if err != nil {
		t.Fatalf("parse markup: %v", err)
	}

	tags := extractCommonBookTags(doc)
	if len(tags) != 2 || tags[0] != "都市" || tags[1] != "重生" {
		t.Fatalf("unexpected tags: %v", tags)
	}
}
