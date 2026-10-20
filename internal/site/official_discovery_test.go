package site

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestParseN17KDiscoveryResults(t *testing.T) {
	markup := `<!doctype html>
<html>
<body>
  <div class="TABBOX">
    <div class="Title1"><h2 class="tit">推荐票榜</h2></div>
    <div class="TYPE">
      <ul class="BOX Top1">
        <li>
          <span class="num">1</span>
          <a href="//www.17k.com/book/3597196.html">太上无情</a>
          <a class="book" href="//www.17k.com/book/3597196.html">
            <img src="https://cdn.example/cover.jpg" alt="太上无情">
            受人闻疾苦窥悟仙道，参阴阳玄妙掌握天机
          </a>
        </li>
        <li>
          <span class="num">2</span>
          <a href="//www.17k.com/book/3597196.html">重复作品</a>
        </li>
        <li>
          <span class="num">3</span>
          <a href="//www.17k.com/book/3660517.html">脑子里长了个电视机</a>
        </li>
      </ul>
    </div>
  </div>
  <div class="TABBOX">
    <div class="Title1"><h2 class="tit">畅销榜</h2></div>
    <div class="TYPE"><ul class="BOX Top1"><li><a href="//www.17k.com/book/1.html">其他榜单</a></li></ul></div>
  </div>
</body>
</html>`

	results, err := parseN17KDiscoveryResults(markup, "推荐票榜", 2)
	if err != nil {
		t.Fatalf("parse 17k discovery: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected two results, got %d", len(results))
	}
	first := results[0]
	if first.Site != "n17k" || first.BookID != "3597196" || first.Title != "太上无情" {
		t.Fatalf("unexpected first result: %+v", first)
	}
	if first.URL != "https://www.17k.com/book/3597196.html" {
		t.Fatalf("unexpected URL: %q", first.URL)
	}
	if first.Description != "受人闻疾苦窥悟仙道，参阴阳玄妙掌握天机" {
		t.Fatalf("unexpected description: %q", first.Description)
	}
	if results[1].BookID != "3660517" {
		t.Fatalf("deduplication or ordering failed: %+v", results)
	}
}

func TestLoadN17KDiscoveryResultsFallsBackAfter405(t *testing.T) {
	markup := `<!doctype html>
<html>
<body>
  <div class="TABBOX">
    <h2 class="tit">推荐票榜</h2>
    <ul class="BOX Top1">
      <li><a href="//www.17k.com/book/3669552.html">娘子，我真不想考状元</a></li>
    </ul>
  </div>
</body>
</html>`
	calls := make([]string, 0, 2)
	fetch := func(_ context.Context, rawURL string) (string, error) {
		calls = append(calls, rawURL)
		if strings.Contains(rawURL, "from=novel-dl") {
			return "", errors.New("http 405 for primary ranking URL")
		}
		return markup, nil
	}

	results, err := loadN17KDiscoveryResults(context.Background(), fetch, "推荐票榜", 10)
	if err != nil {
		t.Fatalf("load 17k discovery with fallback: %v", err)
	}
	if len(results) != 1 || results[0].BookID != "3669552" {
		t.Fatalf("unexpected fallback results: %+v", results)
	}
	if len(calls) != 2 {
		t.Fatalf("expected primary and fallback requests, got %v", calls)
	}
	if !strings.Contains(calls[0], "from=novel-dl") || !strings.HasSuffix(calls[1], "/top/") {
		t.Fatalf("unexpected request order: %v", calls)
	}
}

func TestParseSfacgDiscoveryResultsUsesStaticRankingsOnly(t *testing.T) {
	markup := `<!doctype html>
<html>
<body>
  <ul class="bd_PHB">
    <li class="bd_PHB_li1">SF轻小说月度月票榜</li>
    <li><span>1</span><span><a href="http://book.sfacg.com/Novel/714059/">转生贫穷小绿茶</a></span></li>
    <li><span>2</span><span><a href="http://book.sfacg.com/Novel/750700/">黑道公主</a></span></li>
  </ul>
  <ul class="bd_PHB">
    <li class="bd_PHB_li1">打赏榜</li>
    <li><span>1</span><span><a href="http://book.sfacg.com/Novel/12345/">打赏作品</a></span></li>
    <li><span>2</span><span><a href="https://book.sfacg.com/Novel/12345/">重复作品</a></span></li>
  </ul>
  <ul class="bd_PHB">
    <li class="bd_PHB_li1">热销榜</li>
    <li><span>1</span><span><a href="http://book.sfacg.com/Novel/22222/">热销作品</a></span></li>
  </ul>
  <ul class="bd_PHB">
    <li class="bd_PHB_li1">粉丝榜</li>
    <li><span>1</span><span><a href="/user/1">粉丝用户</a></span></li>
  </ul>
</body>
</html>`

	if len(sfacgDiscoverySpecs) != 3 {
		t.Fatalf("expected three work rankings, got %d", len(sfacgDiscoverySpecs))
	}
	for _, spec := range sfacgDiscoverySpecs {
		if spec.Index >= 3 {
			t.Fatalf("fan ranking must not be exposed: %+v", spec.List)
		}
	}

	results, err := parseSfacgDiscoveryResults(markup, 1, 1)
	if err != nil {
		t.Fatalf("parse sfacg discovery: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected one result, got %d", len(results))
	}
	item := results[0]
	if item.Site != "sfacg" || item.BookID != "12345" || item.Title != "打赏作品" {
		t.Fatalf("unexpected sfacg result: %+v", item)
	}
	if item.URL != "https://book.sfacg.com/Novel/12345/" {
		t.Fatalf("unexpected sfacg URL: %q", item.URL)
	}
}

func TestParseCiyuanjiDiscoveryResults(t *testing.T) {
	markup := `<!doctype html>
<html>
<body>
  <script id="__NEXT_DATA__" type="application/json">
  {
    "props": {
      "pageProps": {
        "rankListData": {
          "list": [
            {
              "bookId": 30278,
              "bookName": "咒回：憧憬成为不朽之神",
              "authorName": "远古叶子",
              "imgUrl": "https://img.ciyuanji.com/cover.jpg",
              "notes": "",
              "firstClassifyName": "同人",
              "secondClassifyName": "综漫同人",
              "latestChapterName": "第一章"
            },
            {
              "bookId": 30278,
              "bookName": "重复作品",
              "authorName": "重复作者"
            },
            {
              "bookId": 35695,
              "bookName": "归乡：越权访问",
              "authorName": "艳鸣",
              "imgUrl": "//img.ciyuanji.com/cover-2.jpg"
            }
          ]
        }
      }
    }
  }
  </script>
</body>
</html>`

	results, err := parseCiyuanjiDiscoveryResults(markup, 2)
	if err != nil {
		t.Fatalf("parse ciyuanji discovery: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected two results, got %d", len(results))
	}
	first := results[0]
	if first.Site != "ciyuanji" || first.BookID != "30278" || first.Author != "远古叶子" {
		t.Fatalf("unexpected ciyuanji result: %+v", first)
	}
	if first.Description != "同人 · 综漫同人" {
		t.Fatalf("expected category fallback description, got %q", first.Description)
	}
	if first.URL != "https://www.ciyuanji.com/b_d_30278.html" {
		t.Fatalf("unexpected ciyuanji URL: %q", first.URL)
	}
	if results[1].CoverURL != "https://img.ciyuanji.com/cover-2.jpg" {
		t.Fatalf("unexpected protocol-relative cover: %q", results[1].CoverURL)
	}
}

func TestParseKadokadoDiscoveryResults(t *testing.T) {
	markup := `<!doctype html>
<html>
<body>
  <script id="__NEXT_DATA__" type="application/json">
  {
    "props": {
      "pageProps": {
        "topRankingTitlesData": [
          {
            "titleId": 91931,
            "displayName": "有谁共鸣",
            "ownerDisplayName": "作者江东",
            "coverUrls": ["", "https://img.kadokado.com.tw/cover.jpg"],
            "logline": "",
            "oneLineIntro": "温情动人的中年故事！",
            "authorsDisplayNames": ["作者江东"]
          },
          {
            "titleId": 91306,
            "displayName": "人资长日志",
            "ownerDisplayName": "",
            "coverUrls": ["//img.kadokado.com.tw/cover-2.jpg"],
            "logline": "一段职场故事",
            "authorsDisplayNames": ["墨行舟", "另一位作者"]
          }
        ]
      }
    }
  }
  </script>
</body>
</html>`

	results, err := parseKadokadoDiscoveryResults(markup, 10)
	if err != nil {
		t.Fatalf("parse kadokado discovery: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected two results, got %d", len(results))
	}
	first := results[0]
	if first.Site != "kadokado" || first.BookID != "91931" || first.Title != "有谁共鸣" {
		t.Fatalf("unexpected kadokado result: %+v", first)
	}
	if first.Author != "作者江东" || first.Description != "温情动人的中年故事！" {
		t.Fatalf("unexpected kadokado metadata: %+v", first)
	}
	if first.URL != "https://www.kadokado.com.tw/book/91931" {
		t.Fatalf("unexpected kadokado URL: %q", first.URL)
	}
	if first.CoverURL != "https://img.kadokado.com.tw/cover.jpg" {
		t.Fatalf("unexpected kadokado cover: %q", first.CoverURL)
	}
	if results[1].Author != "墨行舟, 另一位作者" {
		t.Fatalf("unexpected fallback author: %q", results[1].Author)
	}
}

func TestParseAlphapolisDiscoveryResults(t *testing.T) {
	markup := `<!doctype html>
<html>
<body>
  <ul>
    <li class="p-ranking__item">
      <div class="c-content">
        <img src="/v2/img/project_v2/ranking/icon_stay.svg" alt="stay">
        <a class="c-content__cover" href="/novel/195605754/55093625">
          <img src="https://cdn-image.alphapolis.co.jp/cover.jpg" alt="封面">
        </a>
        <div class="c-content__body">
          <h2 class="c-content__title">
            <a href="/novel/195605754/55093625" class="c-link">世界十杰全是我的学生</a>
          </h2>
          <div class="c-content__author-publink"><a>鹰木</a></div>
          <div class="c-content__abstract">被流放到最差要塞的原奴隶，重新集结十名学生并建立国家。</div>
        </div>
      </div>
    </li>
    <li class="p-ranking__item">
      <div class="c-content">
        <h2 class="c-content__title"><a href="/novel/195605754/55093625" class="c-link">重复作品</a></h2>
      </div>
    </li>
  </ul>
</body>
</html>`

	results, err := parseAlphapolisDiscoveryResults(markup, 10)
	if err != nil {
		t.Fatalf("parse alphapolis discovery: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected one result, got %d", len(results))
	}
	item := results[0]
	if item.Site != "alphapolis" || item.BookID != "195605754-55093625" {
		t.Fatalf("unexpected alphapolis identity: %+v", item)
	}
	if item.Title != "世界十杰全是我的学生" || item.Author != "鹰木" {
		t.Fatalf("unexpected alphapolis metadata: %+v", item)
	}
	if item.Description != "被流放到最差要塞的原奴隶，重新集结十名学生并建立国家。" {
		t.Fatalf("unexpected alphapolis description: %q", item.Description)
	}
	if item.URL != "https://www.alphapolis.co.jp/novel/195605754/55093625" {
		t.Fatalf("unexpected alphapolis URL: %q", item.URL)
	}
	if item.CoverURL != "https://cdn-image.alphapolis.co.jp/cover.jpg" {
		t.Fatalf("unexpected alphapolis cover: %q", item.CoverURL)
	}
}

func TestParseAlphapolisAnnualDiscoveryResults(t *testing.T) {
	markup := `<!doctype html>
<html>
<body>
  <section class="ranking">
    <div class="ranking__content ranking__content--large">
      <div class="rank rank--1"><div class="rank__text">第<span class="rank__text-large">1</span>位</div></div>
      <a href="/novel/500033287/271935870" class="cover cover--large">
        <img src="/v2/img/annual-cover.png?id=first" class="cover__image" alt="不爱了">
      </a>
      <div class="content-info content-info--large">
        <div class="content-info__tag">恋爱</div>
        <a href="/novel/500033287/271935870" class="content-info__title content-info__title--large">【完结】不爱了、你</a>
        <a href="/author/detail/500033287" class="content-info__author content-info__author--large">野村仁礼</a>
        <div class="content-info__abstract">婚约破裂之后，她重新寻找自己的归处。</div>
      </div>
    </div>
    <div class="ranking__content">
      <div class="rank rank--2"><div class="rank__text">第<span class="rank__text-large">2</span>位</div></div>
      <a href="/novel/500033287/983873132" class="cover"><img src="/cover-2.jpg"></a>
      <div class="content-info">
        <a href="/novel/500033287/983873132" class="content-info__title">第二部作品</a>
        <a href="/author/detail/2" class="content-info__author">作者二</a>
      </div>
    </div>
  </section>
</body>
</html>`

	results, err := parseAlphapolisDiscoveryResults(markup, 2)
	if err != nil {
		t.Fatalf("parse alphapolis annual discovery: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected two annual results, got %d", len(results))
	}
	first := results[0]
	if first.BookID != "500033287-271935870" || first.Title != "【完结】不爱了、你" {
		t.Fatalf("unexpected annual identity: %+v", first)
	}
	if first.Author != "野村仁礼" || first.Description != "婚约破裂之后，她重新寻找自己的归处。" {
		t.Fatalf("unexpected annual metadata: %+v", first)
	}
	if first.CoverURL != "https://www.alphapolis.co.jp/v2/img/annual-cover.png?id=first" {
		t.Fatalf("unexpected annual cover: %q", first.CoverURL)
	}
}
