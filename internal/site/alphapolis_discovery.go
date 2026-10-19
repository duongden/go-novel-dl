package site

import (
	"context"
	"fmt"
	"strings"

	"golang.org/x/net/html"

	"github.com/guohuiyuan/go-novel-dl/internal/model"
)

var alphapolisDiscoveryLists = []DiscoveryList{
	{ID: "hot", Name: "综合热门榜", Description: "Alphapolis 综合热度最高的连载小说", Kind: "ranking", UpdateFrequency: "实时更新"},
	{ID: "annual", Name: "年度榜", Description: "Alphapolis 年度人气作品排行", Kind: "ranking", UpdateFrequency: "每年更新"},
	{ID: "completed", Name: "完结作品榜", Description: "Alphapolis 完结小说中的人气作品", Kind: "ranking", UpdateFrequency: "每日更新"},
}

var alphapolisDiscoveryPaths = map[string]string{
	"hot":       "/novel/ranking/hot",
	"annual":    "/novel/ranking/annual",
	"completed": "/novel/ranking/completed",
}

var _ DiscoveryProvider = (*AlphapolisSite)(nil)

func (s *AlphapolisSite) DiscoveryLists(ctx context.Context) ([]DiscoveryList, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return cloneDiscoveryLists(alphapolisDiscoveryLists), nil
}

func (s *AlphapolisSite) DiscoveryBooks(ctx context.Context, listID string, limit int) ([]model.SearchResult, error) {
	listID = strings.TrimSpace(listID)
	path, ok := alphapolisDiscoveryPaths[listID]
	if !ok {
		return nil, fmt.Errorf("alphapolis discovery list %q not found", listID)
	}
	rawURL := s.baseURL + path
	markup, err := s.html.GetWithHeaders(ctx, rawURL, japaneseHeaders(s.cfg.Cookie, s.baseURL+"/novel/ranking/"))
	if err != nil {
		return nil, err
	}
	if err := rejectChallengePage(s.Key(), markup); err != nil {
		return nil, err
	}
	return parseAlphapolisDiscoveryResults(markup, normalizeDiscoveryLimit(limit, 20))
}

func parseAlphapolisDiscoveryResults(markup string, limit int) ([]model.SearchResult, error) {
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
		return n.Type == html.ElementNode &&
			n.Data == "a" &&
			(hasClass(n, "c-link") || hasClass(n, "content-info__title")) &&
			alphapolisDiscoveryBookID(attrValue(n, "href")) != ""
	}) {
		bookID := alphapolisDiscoveryBookID(attrValue(link, "href"))
		title := cleanText(nodeText(link))
		if bookID == "" || title == "" {
			continue
		}
		if _, ok := seen[bookID]; ok {
			continue
		}
		seen[bookID] = struct{}{}

		card := nearestAlphapolisCard(link)
		coverLink := findFirst(card, func(n *html.Node) bool {
			return n.Type == html.ElementNode &&
				n.Data == "a" &&
				(hasClass(n, "c-content__cover") || hasClass(n, "c-content-cover") || hasClass(n, "cover"))
		})
		image := findFirst(coverLink, func(n *html.Node) bool {
			return n.Type == html.ElementNode && n.Data == "img"
		})
		author := cleanText(nodeText(findFirst(card, func(n *html.Node) bool {
			return n.Type == html.ElementNode &&
				(hasClass(n, "c-content__author-publink") || hasClass(n, "content-info__author"))
		})))
		description := cleanText(nodeText(findFirst(card, func(n *html.Node) bool {
			return n.Type == html.ElementNode &&
				(hasClass(n, "c-content__abstract") || hasClass(n, "content-info__abstract"))
		})))
		userID, novelID, _ := strings.Cut(bookID, "-")
		results = append(results, model.SearchResult{
			Site:        "alphapolis",
			BookID:      bookID,
			Title:       title,
			Author:      author,
			Description: description,
			URL:         fmt.Sprintf("%s/novel/%s/%s", "https://www.alphapolis.co.jp", userID, novelID),
			CoverURL:    absolutizeURL("https://www.alphapolis.co.jp", attrValue(image, "src")),
		})
		if len(results) >= limit {
			break
		}
	}
	return results, nil
}

func alphapolisDiscoveryBookID(raw string) string {
	parsed, err := normalizeURL(raw)
	if err != nil {
		return ""
	}
	match := alphapolisBookPathRe.FindStringSubmatch(parsed.Path)
	if len(match) != 3 {
		return ""
	}
	return match[1] + "-" + match[2]
}

func nearestAlphapolisCard(node *html.Node) *html.Node {
	for current := node; current != nil; current = current.Parent {
		if current.Type != html.ElementNode {
			continue
		}
		if current.Data == "li" ||
			current.Data == "article" ||
			hasClass(current, "c-content") ||
			hasClass(current, "ranking__content") {
			return current
		}
	}
	if node.Parent != nil {
		return node.Parent
	}
	return node
}
