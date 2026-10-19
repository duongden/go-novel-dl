package site

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"golang.org/x/text/encoding/simplifiedchinese"

	"github.com/guohuiyuan/go-novel-dl/internal/config"
	"github.com/guohuiyuan/go-novel-dl/internal/model"
)

func TestMobile17ResolveURL(t *testing.T) {
	cfg := config.DefaultConfig()
	tests := []struct {
		name      string
		site      Site
		rawURL    string
		bookID    string
		chapterID string
	}{
		{"ltxswu book", NewLtxswuSite(cfg.ResolveSiteConfig("ltxswu")), "http://m.ltxswu.net/book/1/", "1", ""},
		{"ltxswu chapter", NewLtxswuSite(cfg.ResolveSiteConfig("ltxswu")), "http://m.ltxswu.me/book/1/135930.html", "1", "135930"},
		{"ltxswu chapter paginated", NewLtxswuSite(cfg.ResolveSiteConfig("ltxswu")), "http://m.ltxswu.net/book/1/1_2.html", "1", "1"},
		{"banzhu book", NewBanzhu66666Site(cfg.ResolveSiteConfig("banzhu66666")), "https://www.banzhu66666.com/book/1/", "1", ""},
		{"banzhu chapter", NewBanzhu66666Site(cfg.ResolveSiteConfig("banzhu66666")), "https://banzhu66666.com/book/1/2.html", "1", "2"},
		{"unrelated host", NewLtxswuSite(cfg.ResolveSiteConfig("ltxswu")), "https://example.com/book/1/", "", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			resolved, ok := tc.site.ResolveURL(tc.rawURL)
			if tc.bookID == "" {
				if ok {
					t.Fatalf("expected URL not to resolve, got %+v", resolved)
				}
				return
			}
			if !ok {
				t.Fatalf("expected URL to resolve")
			}
			if resolved.BookID != tc.bookID || resolved.ChapterID != tc.chapterID {
				t.Fatalf("expected %s/%s, got %s/%s", tc.bookID, tc.chapterID, resolved.BookID, resolved.ChapterID)
			}
		})
	}
}

func TestMobile17DownloadPlanAndFetchChapter(t *testing.T) {
	var sawMobileUA bool
	mux := http.NewServeMux()
	mux.HandleFunc("/book/123/", func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.UserAgent(), "Android") {
			sawMobileUA = true
		}
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<html><head>
<meta property="og:novel:book_name" content="测试书名">
<meta property="og:novel:author" content="测试作者">
<meta property="og:description" content="简介内容">
<meta property="og:image" content="/cover.jpg">
<meta property="og:novel:category" content="辣文合集">
</head><body>
<form name="searchform" action="/s.php"><select id="searchType" name="type"><option value="articlename"></option></select></form>
<p>分类：<a href="/sort/9_1/">辣文合集</a></p>
<p>状态：连载中</p>
<div class="intro">最新章节预览</div>
<ul class="chapter"><li><a href="/book/123/4.html">第4章<span></span></a></li></ul>
<div class="intro">正文</div>
<ul class="chapter">
<li><a href="/book/123/1.html">第1章</a></li>
<li><a href="/book/123/2.html">第2章</a></li>
</ul>
<div class="listpage"><select name="pageselect"><option value="/book/123_2/">21 - 40章</option></select></div>
</body></html>`))
	})
	mux.HandleFunc("/book/123_2/", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<html><body>
<div class="intro">正文</div>
<ul class="chapter">
<li><a href="/book/123/3.html">第3章</a></li>
<li><a href="/book/123/4.html">第4章</a></li>
</ul>
</body></html>`))
	})
	mux.HandleFunc("/book/123/1.html", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<html><body>
<div class="nr_title" id="nr_title">第1章(第1/2页)</div>
<div id="nr" class="nr_nr"><p style="color: red;">怕找不到回家的路！请截图保存本站发布地址</p>
<div id="nr1">&nbsp;第一行内容。<br/><br/>&nbsp;第二行内容。<img src="/zi/a.png" /><br/><br/>&nbsp;地址发布邮箱：xxx@gmail.com<br/><br/>&nbsp;本章未完，请点击下一页继续阅读》》</div></div>
<div class="nr_page"><a href="/book/123/1_2.html">下一页</a></div>
</body></html>`))
	})
	mux.HandleFunc("/book/123/1_2.html", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<html><body>
<div class="nr_title" id="nr_title">第1章(第2/2页)</div>
<div id="nr" class="nr_nr"><div id="nr1">&nbsp;第三行内容。</div></div>
</body></html>`))
	})
	mux.HandleFunc("/book/123/2.html", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<html><body><div id="nr1">第2章内容</div></body></html>`))
	})
	mux.HandleFunc("/s.php", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("unexpected search method: %s", r.Method)
		}
		if err := r.ParseForm(); err != nil {
			t.Fatalf("parse search form: %v", err)
		}
		gbkKeyword, _ := simplifiedchinese.GBK.NewEncoder().String("测试书")
		if r.Form.Get("s") != gbkKeyword || r.Form.Get("type") != "articlename" {
			t.Fatalf("unexpected search form: %v", r.Form)
		}
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<html><body><div class="cover"><div>搜索“<font color=red>测试书</font>”结果</div>
<p class="line"><a href="/sort/3_1/">[都市]</a><a href="/book/123/" class="blue" target="_blank">测试书名</a>/<a href="/author/测试作者" target="_blank">测试作者</a></p>
<p class="line"><a href="/sort/8_1/">[其他]</a><a href="/book/999/" class="blue" target="_blank">其它书</a>/<a href="/author/某人" target="_blank">某人</a></p>
</div></body></html>`))
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	s := NewLtxswuSite(config.DefaultConfig().ResolveSiteConfig("ltxswu"))
	s.baseURL = server.URL
	s.client = server.Client()
	s.html = NewHTMLSite(server.Client())

	book, err := s.DownloadPlan(context.Background(), model.BookRef{BookID: "123"})
	if err != nil {
		t.Fatalf("download plan: %v", err)
	}
	if book.Title != "测试书名" || book.Author != "测试作者" {
		t.Fatalf("unexpected book metadata: %+v", book)
	}
	if len(book.Tags) != 1 || book.Tags[0] != "辣文合集" {
		t.Fatalf("unexpected book tags: %v", book.Tags)
	}
	if len(book.Chapters) != 4 {
		t.Fatalf("expected 4 chapters, got %d", len(book.Chapters))
	}
	// 最新章节预览（第4章）不应重复计入，正文顺序应为 1,2,3,4
	if book.Chapters[3].ID != "4" || book.Chapters[0].ID != "1" {
		t.Fatalf("unexpected chapter order: %+v", book.Chapters)
	}

	chapter, err := s.FetchChapter(context.Background(), book.ID, book.Chapters[0])
	if err != nil {
		t.Fatalf("fetch chapter: %v", err)
	}
	if chapter.Title != "第1章" {
		t.Fatalf("unexpected chapter title: %q", chapter.Title)
	}
	if !strings.Contains(chapter.Content, "第一行内容") || !strings.Contains(chapter.Content, "第三行内容") {
		t.Fatalf("unexpected chapter content: %q", chapter.Content)
	}
	if strings.Contains(chapter.Content, "地址发布") || strings.Contains(chapter.Content, "下一页继续阅读") {
		t.Fatalf("ad lines leaked into content: %q", chapter.Content)
	}
	if !sawMobileUA {
		t.Fatalf("expected mobile user agent to be sent")
	}

	results, err := s.Search(context.Background(), "测试书", 10)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(results) != 2 || results[0].BookID != "123" || results[0].Title != "测试书名" || results[0].Author != "测试作者" {
		t.Fatalf("unexpected search results: %+v", results)
	}
	if results[0].URL != s.bookURL("123") {
		t.Fatalf("unexpected search result URL: %q", results[0].URL)
	}
}

func TestMobile17ChapterContentCleansObfuscatedAds(t *testing.T) {
	doc, err := parseHTML(`<html><body><div id="nr1">
&nbsp;&nbsp;凌晨两点，程晓羽还在做与音乐相关的工作。шщш.LтｘＳdz.соｍ<br><br>
&gt;ht\tｐ://www?ltxsdz?ｃｏｍ.ｃoｍ http://m.ltxswu.me/book/57430/<br><br>
程晓羽再次恢复意识，已经不知道时间过去多久。最新?地址)&nbsp;Ltxsｄz.€ǒm<br><br>
大飞在电视台做音乐总监。更多<img src="/zi/j1.png">彩<br><br>
1.哥哥，我喜欢你啊。发布页Ltxsdz…℃〇M更多彩<br><br>
走出酒吧正门，音乐声消失在脑后。『发布邮箱 Ltxs??A @ GmaiL.co??』<br><br>
程晓羽的心里才平复一点。<dfn>Ltxsｄz.€ǒm.com</dfn><br><br>
顾婉清吃完饭就坐到了客厅沙发上。lтｘＳb a @ ｇＭAil.c〇ｍ本子摊开，笔握在手里，坐得笔直。<br><br>
周六晚上，顾婉清又做了四个菜。地址发、布邮箱 Līx_SBǎ@ＧＭＡＩＬ．ｃＯＭ<br><br>
“我这辈子，二十二岁相亲，三面定婚期，然后结婚，生你，上班，下班，值夜班，上手术台。郵件LīxsBǎ@gｍail.ｃｏm?.ｃom发布(地址发布邮箱：ltxsba@gmail.com 发送任意邮件即可！<br><br>
ǎ@ＧＭＡＩＬ．￠ＯＭ╒寻╜回?<br><br>
<div id="center_tip"><b>地址发布邮箱：ltxsba@gmail.com 发送任意邮件即可！</b></div>
<font color="red">本章未完，请点击下一页继续阅读》》</font><br><br>
正常结尾。
</div></body></html>`)
	if err != nil {
		t.Fatalf("parse markup: %v", err)
	}

	got := strings.Join(mobileChapterParagraphs(doc), "\n")
	want := "凌晨两点，程晓羽还在做与音乐相关的工作。\n程晓羽再次恢复意识，已经不知道时间过去多久。\n大飞在电视台做音乐总监。\n1.哥哥，我喜欢你啊。\n走出酒吧正门，音乐声消失在脑后。\n程晓羽的心里才平复一点。\n顾婉清吃完饭就坐到了客厅沙发上。本子摊开，笔握在手里，坐得笔直。\n周六晚上，顾婉清又做了四个菜。\n“我这辈子，二十二岁相亲，三面定婚期，然后结婚，生你，上班，下班，值夜班，上手术台。\n正常结尾。"
	if got != want {
		t.Fatalf("unexpected cleaned content:\n got: %q\nwant: %q", got, want)
	}
}

func TestMobile17CharacterImageMap(t *testing.T) {
	tests := []struct {
		code string
		want string
	}{
		{"a1", "爱"}, {"b1", "爆"}, {"b2", "暴"}, {"b3", "屄"}, {"b4", "棒"}, {"b5", "逼"}, {"b6", "巴"},
		{"c1", "草"}, {"c2", "操"}, {"c3", "插"}, {"c4", "肏"}, {"c5", "潮"},
		{"d1", "荡"}, {"d2", "屌"}, {"d3", "洞"},
		{"f1", "妇"}, {"f2", "粉"},
		{"g1", "肛"}, {"g2", "龟"}, {"g3", "干"}, {"g4", "股"},
		{"j1", "精"}, {"j2", "奸"}, {"j3", "交"}, {"j4", "鸡"}, {"j5", "妓"},
		{"k1", "口"},
		{"l1", "轮"}, {"l2", "浪"}, {"l3", "乱"}, {"l4", "裸"},
		{"n1", "奶"}, {"n2", "女"}, {"n3", "嫩"}, {"n4", "奴"},
		{"p1", "喷"}, {"p2", "破"}, {"p3", "屁"},
		{"q1", "情"},
		{"r1", "肉"}, {"r2", "乳"}, {"r3", "人"}, {"r4", "入"}, {"r5", "日"},
		{"s1", "射"}, {"s2", "深"},
		{"t1", "头"}, {"t2", "臀"},
		{"x1", "穴"}, {"x2", "性"},
		{"y1", "液"}, {"y2", "淫"}, {"y3", "阴"},
	}
	if got := len(mobile17CharacterImages); got != len(tests) {
		t.Fatalf("unexpected character image count: got %d want %d", got, len(tests))
	}
	for _, tc := range tests {
		t.Run(tc.code, func(t *testing.T) {
			doc, err := parseHTML(`<html><body><div id="nr1">前<img src="/zi/` + tc.code + `.png">后</div></body></html>`)
			if err != nil {
				t.Fatalf("parse markup: %v", err)
			}
			got := strings.Join(mobileChapterParagraphs(doc), "\n")
			want := "前" + tc.want + "后"
			if got != want {
				t.Fatalf("unexpected restored character: got %q want %q", got, want)
			}
		})
	}
}

func TestMobile17RestoresCharacterImages(t *testing.T) {
	doc, err := parseHTML(`<html><body><div id="nr1">
出到达<img src="/zi/k1.png">，在<img src="/zi/r3.png">群里回<img src="/zi/t1.png">。
<div><img src="http://m.ltxswu.me/zi/n2.png"><img src="/zi/y1.png"><img src="/zi/unknown.png"></div>
</div></body></html>`)
	if err != nil {
		t.Fatalf("parse markup: %v", err)
	}

	got := strings.Join(mobileChapterParagraphs(doc), "\n")
	want := "出到达口，在人群里回头。\n女液[字图:unknown]"
	if got != want {
		t.Fatalf("unexpected restored content:\n got: %q\nwant: %q", got, want)
	}
}

func TestMobile17CloudflareChallengeDetected(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`<html><title>Just a moment...</title><script src="https://challenges.cloudflare.com"></script></html>`))
	}))
	defer server.Close()

	s := NewBanzhu66666Site(config.DefaultConfig().ResolveSiteConfig("banzhu66666"))
	s.baseURL = server.URL
	s.client = server.Client()
	s.html = NewHTMLSite(server.Client())

	_, err := s.DownloadPlan(context.Background(), model.BookRef{BookID: "1"})
	if err == nil {
		t.Fatalf("expected cloudflare error")
	}
	if !strings.Contains(err.Error(), "Cloudflare") {
		t.Fatalf("expected cloudflare hint, got: %v", err)
	}
}

func TestMobile17GBKMetaCharsetDecoded(t *testing.T) {
	// 联天书屋是 GBK 页面且响应头不带 charset，只能靠 <meta charset="gbk"> 判定，
	// 这里验证 HTMLSite.Get 的转码链路对真实场景有效。
	page := `<html><head><meta charset="gbk" /></head><body><h1>妻子自愿为母狗</h1></body></html>`
	encoded, err := simplifiedchinese.GBK.NewEncoder().Bytes([]byte(page))
	if err != nil {
		t.Fatalf("encode gbk: %v", err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write(encoded)
	}))
	defer server.Close()

	s := NewLtxswuSite(config.DefaultConfig().ResolveSiteConfig("ltxswu"))
	s.client = server.Client()
	s.html = NewHTMLSite(server.Client())

	markup, err := s.html.Get(context.Background(), server.URL)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if !strings.Contains(markup, "妻子自愿为母狗") {
		t.Fatalf("expected GBK meta charset decoding, got: %q", markup)
	}
}
