package site

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/guohuiyuan/go-novel-dl/internal/model"
)

var kadokadoNextDataRe = regexp.MustCompile(`(?s)<script[^>]+id="__NEXT_DATA__"[^>]*>(.*?)</script>`)

type kadokadoDiscoverySpec struct {
	List     DiscoveryList
	Tab      string
	Interval string
}

var kadokadoDiscoverySpecs = []kadokadoDiscoverySpec{
	{
		List:     DiscoveryList{ID: "most-viewed-week", Name: "本周阅读榜", Description: "KadoKado 本週阅读次数最多的作品", Kind: "ranking", UpdateFrequency: "每日更新"},
		Tab:      "MostViewed",
		Interval: "Week",
	},
	{
		List:     DiscoveryList{ID: "most-favorite-week", Name: "本周收藏榜", Description: "KadoKado 本週加入收藏最多的作品", Kind: "ranking", UpdateFrequency: "每日更新"},
		Tab:      "MostFavorite",
		Interval: "Week",
	},
	{
		List:     DiscoveryList{ID: "best-selling-week", Name: "本周畅销榜", Description: "KadoKado 本週付费与销售表现领先的作品", Kind: "ranking", UpdateFrequency: "每日更新"},
		Tab:      "BestSelling",
		Interval: "Week",
	},
}

var _ DiscoveryProvider = (*KadokadoSite)(nil)

func (s *KadokadoSite) DiscoveryLists(ctx context.Context) ([]DiscoveryList, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	lists := make([]DiscoveryList, 0, len(kadokadoDiscoverySpecs))
	for _, spec := range kadokadoDiscoverySpecs {
		lists = append(lists, spec.List)
	}
	return lists, nil
}

func (s *KadokadoSite) DiscoveryBooks(ctx context.Context, listID string, limit int) ([]model.SearchResult, error) {
	listID = strings.TrimSpace(listID)
	var selected *kadokadoDiscoverySpec
	for idx := range kadokadoDiscoverySpecs {
		if kadokadoDiscoverySpecs[idx].List.ID == listID {
			selected = &kadokadoDiscoverySpecs[idx]
			break
		}
	}
	if selected == nil {
		return nil, fmt.Errorf("kadokado discovery list %q not found", listID)
	}

	rawURL := fmt.Sprintf("%s/charts?tab=%s&interval=%s", s.baseURL, selected.Tab, selected.Interval)
	markup, err := s.fetchKadokadoCharts(ctx, rawURL)
	if err != nil {
		return nil, err
	}
	return parseKadokadoDiscoveryResults(markup, normalizeDiscoveryLimit(limit, 20))
}

func (s *KadokadoSite) fetchKadokadoCharts(ctx context.Context, rawURL string) (string, error) {
	headers := map[string]string{
		"Accept-Language": "zh-TW,zh;q=0.9,en;q=0.8",
		"Referer":         s.baseURL + "/charts",
	}
	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		attemptCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		markup, err := NewHTMLSite(s.client).GetWithHeaders(attemptCtx, rawURL, headers)
		cancel()
		if err == nil {
			return markup, nil
		}
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		lastErr = err
		if attempt == 0 {
			if sleepErr := sleepContext(ctx, 250*time.Millisecond); sleepErr != nil {
				return "", sleepErr
			}
		}
	}
	return "", lastErr
}

func parseKadokadoDiscoveryResults(markup string, limit int) ([]model.SearchResult, error) {
	data, err := extractJSONScript(markup, kadokadoNextDataRe)
	if err != nil {
		return nil, err
	}
	pageProps := mapPath(data, "props", "pageProps")
	rawList := sliceValue(pageProps["topRankingTitlesData"])
	if limit <= 0 {
		limit = 20
	}

	results := make([]model.SearchResult, 0, limit)
	seen := make(map[string]struct{}, limit)
	for _, raw := range rawList {
		item := mapValue(raw)
		bookID := strings.TrimSpace(stringValue(item["titleId"]))
		title := cleanText(stringValue(item["displayName"]))
		if bookID == "" || bookID == "0" || title == "" {
			continue
		}
		if _, ok := seen[bookID]; ok {
			continue
		}
		seen[bookID] = struct{}{}

		authors := make([]string, 0)
		for _, rawAuthor := range sliceValue(item["authorsDisplayNames"]) {
			if author := cleanText(stringValue(rawAuthor)); author != "" {
				authors = append(authors, author)
			}
		}
		author := firstNonEmptyString(
			stringValue(item["ownerDisplayName"]),
			strings.Join(authors, ", "),
		)
		description := firstNonEmptyString(
			cleanText(stringValue(item["logline"])),
			cleanText(stringValue(item["oneLineIntro"])),
		)
		results = append(results, model.SearchResult{
			Site:        "kadokado",
			BookID:      bookID,
			Title:       title,
			Author:      cleanText(author),
			Description: description,
			URL:         sKadokadoBookURL(bookID),
			CoverURL:    normalizeMaybeProtocol(kadokadoFirstString(valueStrings(item["coverUrls"]))),
		})
		if len(results) >= limit {
			break
		}
	}
	return results, nil
}

func sKadokadoBookURL(bookID string) string {
	return "https://www.kadokado.com.tw/book/" + strings.TrimSpace(bookID)
}

func valueStrings(value any) []string {
	rawItems := sliceValue(value)
	items := make([]string, 0, len(rawItems))
	for _, item := range rawItems {
		items = append(items, stringValue(item))
	}
	return items
}
