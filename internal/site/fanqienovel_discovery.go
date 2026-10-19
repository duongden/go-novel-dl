package site

import (
	"context"
	"fmt"
	"strings"

	"github.com/guohuiyuan/go-novel-dl/internal/model"
)

var fanqieDiscoveryLists = []DiscoveryList{
	{
		ID:              "all",
		Name:            "番茄热读榜",
		Description:     "番茄小说全站阅读热度领先的作品",
		Kind:            "ranking",
		UpdateFrequency: "实时更新",
	},
}

var _ DiscoveryProvider = (*FanqieNovelSite)(nil)

func (s *FanqieNovelSite) DiscoveryLists(ctx context.Context) ([]DiscoveryList, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return cloneDiscoveryLists(fanqieDiscoveryLists), nil
}

func (s *FanqieNovelSite) DiscoveryBooks(ctx context.Context, listID string, limit int) ([]model.SearchResult, error) {
	if strings.TrimSpace(listID) != "all" {
		return nil, fmt.Errorf("fanqienovel discovery list %q not found", listID)
	}
	limit = normalizeDiscoveryLimit(limit, 20)
	markup, err := s.getHTML(ctx, "https://fanqienovel.com/rank", "https://fanqienovel.com/")
	if err != nil {
		return nil, err
	}
	state, err := extractFanqieInitialState(markup)
	if err != nil {
		return nil, err
	}
	return fanqieDiscoveryResults(state, limit), nil
}

func fanqieDiscoveryResults(state map[string]any, limit int) []model.SearchResult {
	rank := mapValue(state, "rank")
	books := sliceValue(rank["book_list"])
	if limit <= 0 {
		limit = 20
	}
	results := make([]model.SearchResult, 0, limit)
	for _, raw := range books {
		item := mapValue(raw)
		bookID := strings.TrimSpace(stringValue(item["bookId"]))
		if bookID == "" {
			bookID = strings.TrimSpace(stringValue(item["book_id"]))
		}
		title := applySubstMap(strings.TrimSpace(stringValue(item["bookName"])), fanqieMap)
		if title == "" {
			title = applySubstMap(strings.TrimSpace(stringValue(item["book_name"])), fanqieMap)
		}
		if bookID == "" || title == "" {
			continue
		}
		author := applySubstMap(strings.TrimSpace(stringValue(item["author"])), fanqieMap)
		description := applySubstMap(strings.TrimSpace(stringValue(item["abstract"])), fanqieMap)
		latest := applySubstMap(strings.TrimSpace(stringValue(item["lastChapterTitle"])), fanqieMap)
		cover := firstNonEmptyString(
			stringValue(item["thumbUri"]),
			stringValue(item["thumb_url"]),
			stringValue(item["horiz_thumb_url"]),
		)
		results = append(results, model.SearchResult{
			Site:          "fanqienovel",
			BookID:        bookID,
			Title:         title,
			Author:        author,
			Description:   description,
			URL:           fmt.Sprintf("https://fanqienovel.com/page/%s", bookID),
			LatestChapter: latest,
			CoverURL:      cover,
		})
		if len(results) >= limit {
			break
		}
	}
	return results
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
