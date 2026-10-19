package site

import (
	"context"
	"fmt"
	"strings"

	"golang.org/x/net/html"

	"github.com/guohuiyuan/go-novel-dl/internal/model"
)

var qidianDiscoveryLists = []DiscoveryList{
	{ID: "yuepiao", Name: "月票榜", Description: "读者月票支持最多的连载作品", Kind: "ranking", UpdateFrequency: "实时更新"},
	{ID: "hotsales", Name: "畅销榜", Description: "近期付费阅读与销售热度领先的作品", Kind: "ranking", UpdateFrequency: "实时更新"},
	{ID: "readindex", Name: "阅读指数榜", Description: "综合阅读量与追读表现的阅读指数排行", Kind: "ranking", UpdateFrequency: "每日更新"},
	{ID: "newfans", Name: "新粉丝榜", Description: "近期新增粉丝增长最快的作品", Kind: "ranking", UpdateFrequency: "每日更新"},
	{ID: "rec", Name: "推荐榜", Description: "读者推荐票集中的热门作品", Kind: "ranking", UpdateFrequency: "实时更新"},
	{ID: "update", Name: "更新榜", Description: "保持稳定更新的人气作品", Kind: "ranking", UpdateFrequency: "每日更新"},
	{ID: "sign", Name: "签约榜", Description: "起点签约作品中的热度排行", Kind: "ranking", UpdateFrequency: "每周更新"},
	{ID: "newbook", Name: "新书榜", Description: "近期上架新书中的热门作品", Kind: "ranking", UpdateFrequency: "每日更新"},
	{ID: "newauthor", Name: "新人榜", Description: "新锐作者与新人作品排行", Kind: "ranking", UpdateFrequency: "每周更新"},
}

var _ DiscoveryProvider = (*QidianSite)(nil)

func (s *QidianSite) DiscoveryLists(ctx context.Context) ([]DiscoveryList, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return cloneDiscoveryLists(qidianDiscoveryLists), nil
}

func (s *QidianSite) DiscoveryBooks(ctx context.Context, listID string, limit int) ([]model.SearchResult, error) {
	listID = strings.TrimSpace(listID)
	known := false
	for _, item := range qidianDiscoveryLists {
		if item.ID == listID {
			known = true
			break
		}
	}
	if !known {
		return nil, fmt.Errorf("qidian discovery list %q not found", listID)
	}
	limit = normalizeDiscoveryLimit(limit, 20)
	rawURL := fmt.Sprintf("https://m.qidian.com/rank/%s/", listID)
	markup, err := s.fetchQidianHTML(ctx, rawURL, qidianMobileHeaders("https://m.qidian.com/rank/"))
	if err != nil {
		return nil, err
	}
	return parseQidianDiscoveryResults(markup, limit)
}

func parseQidianDiscoveryResults(markup string, limit int) ([]model.SearchResult, error) {
	doc, err := parseHTML(markup)
	if err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 20
	}

	results := make([]model.SearchResult, 0, limit)
	seen := make(map[string]struct{}, limit)
	for _, link := range findAll(doc, func(n *html.Node) bool {
		return n.Type == html.ElementNode && n.Data == "a" && qidianSearchBookID(attrValue(n, "href")) != ""
	}) {
		bookID := qidianSearchBookID(attrValue(link, "href"))
		if bookID == "" {
			continue
		}
		if _, ok := seen[bookID]; ok {
			continue
		}
		seen[bookID] = struct{}{}

		card := qidianDiscoveryCard(link)
		title := qidianFirstText(card, qidianByTag("h2"), qidianByTag("h3"))
		if title == "" {
			title = strings.TrimSpace(strings.TrimSuffix(attrValue(link, "title"), "最新章节在线阅读"))
		}
		if title == "" {
			continue
		}

		subtitle := qidianFirstText(card, qidianByClassPart("subTitle"))
		author, _ := qidianDiscoverySubtitle(subtitle)
		description := qidianFirstText(card,
			qidianByClassPart("bookDesc"),
			qidianByClassPart("desc"),
		)
		cover := attrValue(findFirst(card, func(n *html.Node) bool {
			return n.Type == html.ElementNode && n.Data == "img"
		}), "data-src")
		if cover == "" {
			cover = attrValue(findFirst(card, func(n *html.Node) bool {
				return n.Type == html.ElementNode && n.Data == "img"
			}), "src")
		}

		results = append(results, model.SearchResult{
			Site:        "qidian",
			BookID:      bookID,
			Title:       title,
			Author:      author,
			Description: description,
			URL:         fmt.Sprintf("https://www.qidian.com/book/%s/", bookID),
			CoverURL:    absolutizeURL("https://m.qidian.com", cover),
		})
		if len(results) >= limit {
			break
		}
	}
	return results, nil
}

func qidianDiscoveryCard(link *html.Node) *html.Node {
	for current := link.Parent; current != nil; current = current.Parent {
		if current.Type == html.ElementNode && hasClass(current, "y-list__item") {
			return current
		}
	}
	return link
}

func qidianDiscoverySubtitle(value string) (string, string) {
	value = cleanText(value)
	if value == "" {
		return "", ""
	}
	parts := make([]string, 0, 3)
	for _, part := range strings.Split(value, "·") {
		if part = strings.TrimSpace(part); part != "" {
			parts = append(parts, part)
		}
	}
	switch len(parts) {
	case 0:
		return "", ""
	case 1:
		return parts[0], ""
	case 2:
		return parts[0], parts[1]
	default:
		return parts[0], strings.Join(parts[1:], " · ")
	}
}
