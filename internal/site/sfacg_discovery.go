package site

import (
	"context"
	"fmt"
	"strings"

	"golang.org/x/net/html"

	"github.com/guohuiyuan/go-novel-dl/internal/model"
)

type sfacgDiscoverySpec struct {
	List  DiscoveryList
	Index int
}

var sfacgDiscoverySpecs = []sfacgDiscoverySpec{
	{
		List:  DiscoveryList{ID: "monthly-ticket", Name: "月票榜", Description: "本月获得月票最多的 SF 轻小说作品", Kind: "ranking", UpdateFrequency: "每月更新", BookCount: 20},
		Index: 0,
	},
	{
		List:  DiscoveryList{ID: "reward", Name: "打赏榜", Description: "近期获得读者打赏最多的作品", Kind: "ranking", UpdateFrequency: "实时更新", BookCount: 20},
		Index: 1,
	},
	{
		List:  DiscoveryList{ID: "best-seller", Name: "热销榜", Description: "SF 轻小说近期热销作品排行", Kind: "ranking", UpdateFrequency: "每日更新", BookCount: 20},
		Index: 2,
	},
}

var _ DiscoveryProvider = (*SfacgSite)(nil)

func (s *SfacgSite) DiscoveryLists(ctx context.Context) ([]DiscoveryList, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	lists := make([]DiscoveryList, 0, len(sfacgDiscoverySpecs))
	for _, spec := range sfacgDiscoverySpecs {
		lists = append(lists, spec.List)
	}
	return lists, nil
}

func (s *SfacgSite) DiscoveryBooks(ctx context.Context, listID string, limit int) ([]model.SearchResult, error) {
	listID = strings.TrimSpace(listID)
	var selected *sfacgDiscoverySpec
	for idx := range sfacgDiscoverySpecs {
		if sfacgDiscoverySpecs[idx].List.ID == listID {
			selected = &sfacgDiscoverySpecs[idx]
			break
		}
	}
	if selected == nil {
		return nil, fmt.Errorf("sfacg discovery list %q not found", listID)
	}

	markup, err := s.html.Get(ctx, "https://book.sfacg.com/rank/")
	if err != nil {
		return nil, err
	}
	return parseSfacgDiscoveryResults(markup, selected.Index, normalizeDiscoveryLimit(limit, 20))
}

func parseSfacgDiscoveryResults(markup string, rankingIndex, limit int) ([]model.SearchResult, error) {
	doc, err := parseHTML(markup)
	if err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 20
	}
	rankings := findAll(doc, func(n *html.Node) bool {
		return n.Type == html.ElementNode && n.Data == "ul" && hasClass(n, "bd_PHB")
	})
	if rankingIndex < 0 || rankingIndex >= len(rankings) {
		return nil, fmt.Errorf("sfacg ranking index %d not found", rankingIndex)
	}

	results := make([]model.SearchResult, 0, limit)
	seen := make(map[string]struct{}, limit)
	for _, link := range findAll(rankings[rankingIndex], func(n *html.Node) bool {
		return n.Type == html.ElementNode && n.Data == "a" && sfacgDiscoveryBookID(attrValue(n, "href")) != ""
	}) {
		bookID := sfacgDiscoveryBookID(attrValue(link, "href"))
		if bookID == "" {
			continue
		}
		if _, ok := seen[bookID]; ok {
			continue
		}
		title := cleanText(nodeText(link))
		if title == "" {
			continue
		}
		seen[bookID] = struct{}{}
		results = append(results, model.SearchResult{
			Site:   "sfacg",
			BookID: bookID,
			Title:  title,
			URL:    "https://book.sfacg.com/Novel/" + bookID + "/",
		})
		if len(results) >= limit {
			break
		}
	}
	return results, nil
}

func sfacgDiscoveryBookID(raw string) string {
	parsed, err := normalizeURL(raw)
	if err != nil {
		return ""
	}
	match := sfacgNovelRe.FindStringSubmatch(parsed.Path)
	if len(match) != 2 {
		return ""
	}
	return match[1]
}
