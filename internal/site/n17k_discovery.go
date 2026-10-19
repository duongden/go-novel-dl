package site

import (
	"context"
	"fmt"
	"strings"

	"golang.org/x/net/html"

	"github.com/guohuiyuan/go-novel-dl/internal/model"
)

type n17kDiscoverySpec struct {
	List  DiscoveryList
	Title string
}

var n17kDiscoverySpecs = []n17kDiscoverySpec{
	{
		List:  DiscoveryList{ID: "recommend-votes", Name: "推荐票榜", Description: "本周获得读者推荐票最多的作品", Kind: "ranking", UpdateFrequency: "实时更新"},
		Title: "推荐票榜",
	},
	{
		List:  DiscoveryList{ID: "best-sellers", Name: "畅销榜", Description: "17K 近期畅销作品排行", Kind: "ranking", UpdateFrequency: "实时更新"},
		Title: "畅销榜",
	},
	{
		List:  DiscoveryList{ID: "gifts", Name: "礼物榜", Description: "收到读者礼物支持最多的作品", Kind: "ranking", UpdateFrequency: "实时更新"},
		Title: "礼物榜",
	},
	{
		List:  DiscoveryList{ID: "new-clicks", Name: "新作品点击榜", Description: "近期新作中点击热度最高的作品", Kind: "ranking", UpdateFrequency: "每日更新"},
		Title: "新作品点击榜",
	},
	{
		List:  DiscoveryList{ID: "potential", Name: "潜力作品榜", Description: "17K 编辑与读者共同关注的高潜力作品", Kind: "ranking", UpdateFrequency: "每日更新"},
		Title: "潜力作品榜",
	},
	{
		List:  DiscoveryList{ID: "favorites", Name: "收藏榜", Description: "站内收藏人数领先的热门作品", Kind: "ranking", UpdateFrequency: "每日更新"},
		Title: "收藏榜",
	},
}

var _ DiscoveryProvider = (*N17KSite)(nil)

func (s *N17KSite) DiscoveryLists(ctx context.Context) ([]DiscoveryList, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	lists := make([]DiscoveryList, 0, len(n17kDiscoverySpecs))
	for _, spec := range n17kDiscoverySpecs {
		lists = append(lists, spec.List)
	}
	return lists, nil
}

func (s *N17KSite) DiscoveryBooks(ctx context.Context, listID string, limit int) ([]model.SearchResult, error) {
	listID = strings.TrimSpace(listID)
	var selected *n17kDiscoverySpec
	for idx := range n17kDiscoverySpecs {
		if n17kDiscoverySpecs[idx].List.ID == listID {
			selected = &n17kDiscoverySpecs[idx]
			break
		}
	}
	if selected == nil {
		return nil, fmt.Errorf("n17k discovery list %q not found", listID)
	}

	markup, err := s.fetch(ctx, "https://www.17k.com/top/")
	if err != nil {
		return nil, err
	}
	return parseN17KDiscoveryResults(markup, selected.Title, normalizeDiscoveryLimit(limit, 20))
}

func parseN17KDiscoveryResults(markup, listTitle string, limit int) ([]model.SearchResult, error) {
	doc, err := parseHTML(markup)
	if err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 20
	}
	listTitle = strings.TrimSpace(listTitle)

	var ranking *html.Node
	for _, box := range findAll(doc, func(n *html.Node) bool {
		return n.Type == html.ElementNode && n.Data == "div" && hasClass(n, "TABBOX")
	}) {
		title := cleanText(nodeText(findFirst(box, func(n *html.Node) bool {
			return n.Type == html.ElementNode && n.Data == "h2" && hasClass(n, "tit")
		})))
		if title != listTitle {
			continue
		}
		ranking = findFirst(box, func(n *html.Node) bool {
			return n.Type == html.ElementNode && n.Data == "ul" && hasClass(n, "BOX") && hasClass(n, "Top1")
		})
		break
	}
	if ranking == nil {
		return nil, fmt.Errorf("17k ranking %q not found", listTitle)
	}

	results := make([]model.SearchResult, 0, limit)
	seen := make(map[string]struct{}, limit)
	for _, item := range directChildElements(ranking, "li") {
		titleLink := findFirst(item, func(n *html.Node) bool {
			return n.Type == html.ElementNode &&
				n.Data == "a" &&
				!hasClass(n, "book") &&
				n17KSearchBookID(attrValue(n, "href")) != ""
		})
		bookID := n17KSearchBookID(attrValue(titleLink, "href"))
		if bookID == "" {
			continue
		}
		if _, ok := seen[bookID]; ok {
			continue
		}

		image := findFirst(item, func(n *html.Node) bool {
			return n.Type == html.ElementNode && n.Data == "img"
		})
		title := firstNonEmptyString(
			cleanText(attrValue(image, "alt")),
			attrValue(titleLink, "title"),
			cleanText(nodeText(titleLink)),
		)
		if title == "" {
			continue
		}
		seen[bookID] = struct{}{}

		description := cleanText(nodeText(findFirst(item, func(n *html.Node) bool {
			return n.Type == html.ElementNode && n.Data == "a" && hasClass(n, "book")
		})))
		description = strings.TrimSpace(strings.TrimPrefix(description, title))

		author := cleanText(nodeText(findFirst(item, func(n *html.Node) bool {
			return n.Type == html.ElementNode && hasClassContains(n, "author")
		})))
		cover := firstNonEmptyString(
			attrValue(image, "data-original"),
			attrValue(image, "data-src"),
			attrValue(image, "src"),
		)
		results = append(results, model.SearchResult{
			Site:        "n17k",
			BookID:      bookID,
			Title:       title,
			Author:      author,
			Description: description,
			URL:         "https://www.17k.com/book/" + bookID + ".html",
			CoverURL:    absolutizeURL("https://www.17k.com", cover),
		})
		if len(results) >= limit {
			break
		}
	}
	return results, nil
}
