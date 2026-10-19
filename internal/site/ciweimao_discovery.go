package site

import (
	"context"
	"fmt"
	"strings"

	"golang.org/x/net/html"

	"github.com/guohuiyuan/go-novel-dl/internal/model"
)

var ciweimaoDiscoveryLists = []DiscoveryList{
	{ID: "rank:recommend", Name: "推荐榜", Description: "编辑推荐与读者热度综合排行", Kind: "ranking", UpdateFrequency: "每日更新"},
	{ID: "rank:buy", Name: "订阅榜", Description: "近期订阅表现领先的连载作品", Kind: "ranking", UpdateFrequency: "每日更新"},
	{ID: "rank:yp", Name: "月票榜", Description: "本月获得月票支持最多的作品", Kind: "ranking", UpdateFrequency: "每月更新"},
	{ID: "rank:tsukkomi", Name: "吐槽榜", Description: "读者讨论与吐槽最活跃的作品", Kind: "ranking", UpdateFrequency: "每日更新"},
	{ID: "rank:yp_new", Name: "新书月票榜", Description: "新书作品中的月票排行", Kind: "ranking", UpdateFrequency: "每月更新"},
	{ID: "rank:blade", Name: "刀片榜", Description: "读者刀片数量领先的作品", Kind: "ranking", UpdateFrequency: "每月更新"},
	{ID: "rank:get-update-most-week", Name: "更新榜", Description: "本周更新最勤奋的作品", Kind: "ranking", UpdateFrequency: "每周更新"},
	{ID: "rank:no-vip-click", Name: "免费点击榜", Description: "免费章节点击热度排行", Kind: "ranking", UpdateFrequency: "每日更新"},
	{ID: "rank:favor", Name: "收藏榜", Description: "最受读者收藏欢迎的作品", Kind: "ranking", UpdateFrequency: "每日更新"},
	{
		ID:              "booklist:27872",
		Name:            "精品力荐 1",
		Description:     "刺猬猫官方整理的作品精选书单",
		Kind:            "booklist",
		CoverURL:        "https://e2.kuangxiangit.com/novel/img_index_recommend/booklist/20211028032240935.jpg",
		UpdateFrequency: "官方书单",
	},
	{
		ID:              "booklist:27873",
		Name:            "好书精选 1",
		Description:     "刺猬猫官方整理的高口碑作品书单",
		Kind:            "booklist",
		UpdateFrequency: "官方书单",
	},
}

var _ DiscoveryProvider = (*CiweimaoSite)(nil)

func (s *CiweimaoSite) DiscoveryLists(ctx context.Context) ([]DiscoveryList, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return cloneDiscoveryLists(ciweimaoDiscoveryLists), nil
}

func (s *CiweimaoSite) DiscoveryBooks(ctx context.Context, listID string, limit int) ([]model.SearchResult, error) {
	listID = strings.TrimSpace(listID)
	limit = normalizeDiscoveryLimit(limit, 20)
	switch {
	case strings.HasPrefix(listID, "rank:"):
		rankID := strings.TrimPrefix(listID, "rank:")
		if rankID == "" {
			return nil, fmt.Errorf("ciweimao rank id is empty")
		}
		markup, err := s.html.Get(ctx, ciweimaoURL("/rank-index/"+rankID))
		if err != nil {
			return nil, err
		}
		return parseCiweimaoDiscoveryRankResults(markup, limit)
	case strings.HasPrefix(listID, "booklist:"):
		bookListID := strings.TrimPrefix(listID, "booklist:")
		if bookListID == "" {
			return nil, fmt.Errorf("ciweimao booklist id is empty")
		}
		markup, err := s.html.Get(ctx, ciweimaoURL("/book/booklist_detail?list_id="+bookListID))
		if err != nil {
			return nil, err
		}
		return parseCiweimaoDiscoveryBooklistResults(markup, limit)
	default:
		return nil, fmt.Errorf("ciweimao discovery list %q not found", listID)
	}
}

func parseCiweimaoDiscoveryRankResults(markup string, limit int) ([]model.SearchResult, error) {
	doc, err := parseHTML(markup)
	if err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 20
	}

	results := make([]model.SearchResult, 0, limit)
	seen := make(map[string]struct{}, limit)
	for _, item := range findAll(doc, func(n *html.Node) bool {
		return n.Type == html.ElementNode && n.Data == "li" && strings.TrimSpace(attrValue(n, "data-book-id")) != ""
	}) {
		bookID := strings.TrimSpace(attrValue(item, "data-book-id"))
		if bookID == "" {
			continue
		}
		if _, ok := seen[bookID]; ok {
			continue
		}
		seen[bookID] = struct{}{}

		titleLink := findFirst(item, func(n *html.Node) bool {
			return n.Type == html.ElementNode &&
				n.Data == "a" &&
				strings.Contains(attrValue(n, "href"), "/book/"+bookID) &&
				hasAncestorClass(n, "tit")
		})
		if titleLink == nil {
			titleLink = findFirst(item, func(n *html.Node) bool {
				return n.Type == html.ElementNode &&
					n.Data == "a" &&
					strings.Contains(attrValue(n, "href"), "/book/"+bookID) &&
					(hasClass(n, "cover") || strings.TrimSpace(attrValue(n, "title")) != "")
			})
		}
		if titleLink == nil {
			continue
		}
		title := fallback(attrValue(titleLink, "title"), cleanText(nodeText(titleLink)))
		if title == "" {
			continue
		}

		var author, latest string
		for _, paragraph := range findAll(item, func(n *html.Node) bool {
			return n.Type == html.ElementNode && n.Data == "p"
		}) {
			text := cleanText(nodeText(paragraph))
			switch {
			case strings.HasPrefix(text, "小说作者："):
				author = strings.TrimSpace(strings.TrimPrefix(text, "小说作者："))
			case strings.HasPrefix(text, "最近更新："):
				text = strings.TrimSpace(strings.TrimPrefix(text, "最近更新："))
				if idx := strings.Index(text, "/"); idx >= 0 {
					text = strings.TrimSpace(text[idx+1:])
				}
				latest = text
			}
		}

		description := cleanText(nodeText(findFirst(item, func(n *html.Node) bool {
			return n.Type == html.ElementNode && n.Data == "p" && hasClass(n, "desc")
		})))
		cover := ciweimaoDiscoveryImage(findFirst(item, func(n *html.Node) bool {
			return n.Type == html.ElementNode && n.Data == "img" && hasAncestorClass(n, "cover")
		}))
		results = append(results, model.SearchResult{
			Site:          "ciweimao",
			BookID:        bookID,
			Title:         title,
			Author:        author,
			Description:   description,
			URL:           ciweimaoURL("/book/" + bookID),
			LatestChapter: latest,
			CoverURL:      cover,
		})
		if len(results) >= limit {
			break
		}
	}
	return results, nil
}

func parseCiweimaoDiscoveryBooklistResults(markup string, limit int) ([]model.SearchResult, error) {
	doc, err := parseHTML(markup)
	if err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 20
	}

	results := make([]model.SearchResult, 0, limit)
	seen := make(map[string]struct{}, limit)
	for _, item := range findAll(doc, func(n *html.Node) bool {
		return n.Type == html.ElementNode && n.Data == "li" && hasAncestorClass(n, "book-list")
	}) {
		titleLink := findFirst(item, func(n *html.Node) bool {
			return n.Type == html.ElementNode &&
				n.Data == "a" &&
				hasAncestorClass(n, "title") &&
				ciweimaoDiscoveryBookID(attrValue(n, "href")) != ""
		})
		if titleLink == nil {
			titleLink = findFirst(item, func(n *html.Node) bool {
				return n.Type == html.ElementNode &&
					n.Data == "a" &&
					hasClass(n, "img") &&
					ciweimaoDiscoveryBookID(attrValue(n, "href")) != ""
			})
		}
		if titleLink == nil {
			continue
		}
		bookID := ciweimaoDiscoveryBookID(attrValue(titleLink, "href"))
		if bookID == "" {
			continue
		}
		if _, ok := seen[bookID]; ok {
			continue
		}
		seen[bookID] = struct{}{}

		title := fallback(attrValue(titleLink, "title"), cleanText(nodeText(titleLink)))
		if title == "" {
			continue
		}
		var author string
		for _, paragraph := range findAll(item, func(n *html.Node) bool {
			return n.Type == html.ElementNode && n.Data == "p" && hasClass(n, "info")
		}) {
			text := cleanText(nodeText(paragraph))
			if strings.HasPrefix(text, "作者：") {
				author = strings.TrimSpace(strings.TrimPrefix(text, "作者："))
				break
			}
		}
		description := cleanText(nodeText(findFirst(item, func(n *html.Node) bool {
			return n.Type == html.ElementNode && n.Data == "p" && hasClass(n, "desc")
		})))
		cover := ciweimaoDiscoveryImage(findFirst(item, func(n *html.Node) bool {
			return n.Type == html.ElementNode && n.Data == "img"
		}))
		results = append(results, model.SearchResult{
			Site:        "ciweimao",
			BookID:      bookID,
			Title:       title,
			Author:      author,
			Description: description,
			URL:         ciweimaoURL("/book/" + bookID),
			CoverURL:    cover,
		})
		if len(results) >= limit {
			break
		}
	}
	return results, nil
}

func ciweimaoDiscoveryBookID(raw string) string {
	if match := ciweimaoBookHrefRe.FindStringSubmatch(raw); len(match) >= 2 {
		return match[1]
	}
	return ""
}

func ciweimaoDiscoveryImage(node *html.Node) string {
	if node == nil {
		return ""
	}
	for _, key := range []string{"data-original", "data-src", "src"} {
		raw := strings.TrimSpace(attrValue(node, key))
		if raw != "" && !strings.Contains(strings.ToLower(raw), "transparent.") {
			return absolutizeURL(ciweimaoBaseURL, raw)
		}
	}
	return ""
}
