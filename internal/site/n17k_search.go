package site

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/guohuiyuan/go-novel-dl/internal/model"
)

const (
	n17kSearchAPI      = "https://h5.17k.com/ck/search/list"
	n17kSearchPageSize = 20
	n17kSearchMaxItems = 100
)

type n17kSearchAPIResponse struct {
	Status struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
	} `json:"status"`
	Data struct {
		HasMore bool                `json:"hasMore"`
		Lists   []n17kSearchAPIBook `json:"lists"`
	} `json:"data"`
}

type n17kSearchAPIBook struct {
	ID                    json.RawMessage `json:"id"`
	BookName              string          `json:"bookName"`
	AuthorPenName         string          `json:"authorPenName"`
	Introduction          string          `json:"introduction"`
	CoverImg              string          `json:"coverImg"`
	LastUpdateChapterName string          `json:"lastUpdateChapterName"`
	BookCategory          struct {
		Name string `json:"name"`
	} `json:"bookCategory"`
	BookChannel struct {
		Name string `json:"name"`
	} `json:"bookChannel"`
}

func loadN17KSearchResults(
	ctx context.Context,
	fetch func(context.Context, string) (string, error),
	keyword string,
	limit int,
) ([]model.SearchResult, error) {
	keyword = strings.TrimSpace(keyword)
	if keyword == "" {
		return nil, nil
	}
	if fetch == nil {
		return nil, errors.New("17k search fetcher is nil")
	}

	target := limit
	if target <= 0 {
		target = n17kSearchPageSize
	}
	if target > n17kSearchMaxItems {
		target = n17kSearchMaxItems
	}

	results := make([]model.SearchResult, 0, target)
	seen := make(map[string]struct{}, target)
	for page := 1; len(results) < target; page++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		payload, err := fetch(ctx, n17kSearchAPIURL(keyword, page))
		if err != nil {
			return nil, err
		}
		response, err := parseN17KSearchAPIResponse(payload)
		if err != nil {
			return nil, err
		}
		if len(response.Data.Lists) == 0 {
			break
		}

		added := 0
		for _, item := range response.Data.Lists {
			bookID := n17kSearchAPIBookID(item.ID)
			if bookID == "" {
				continue
			}
			if _, exists := seen[bookID]; exists {
				continue
			}
			title := cleanText(item.BookName)
			if title == "" {
				continue
			}
			seen[bookID] = struct{}{}
			results = append(results, model.SearchResult{
				Site:          "n17k",
				BookID:        bookID,
				Title:         title,
				Author:        cleanText(item.AuthorPenName),
				Description:   n17kSearchDescription(item),
				URL:           "https://www.17k.com/book/" + bookID + ".html",
				LatestChapter: cleanText(item.LastUpdateChapterName),
				CoverURL:      absolutizeURL("https://www.17k.com", item.CoverImg),
			})
			added++
			if len(results) >= target {
				break
			}
		}
		if !response.Data.HasMore || added == 0 {
			break
		}
	}
	return results, nil
}

func n17kSearchAPIURL(keyword string, page int) string {
	if page <= 0 {
		page = 1
	}
	query := url.Values{}
	query.Set("appKey", "1351550300")
	query.Set("page", strconv.Itoa(page))
	query.Set("fuzzySearchType", "1")
	query.Set("keyword", keyword)
	return n17kSearchAPI + "?" + query.Encode()
}

func parseN17KSearchAPIResponse(payload string) (n17kSearchAPIResponse, error) {
	var response n17kSearchAPIResponse
	if n17kLooksLikeWAFChallenge(payload) {
		return response, errors.New("17k search api returned a waf challenge")
	}
	if err := json.Unmarshal([]byte(payload), &response); err != nil {
		return response, fmt.Errorf("decode 17k search api: %w", err)
	}
	if response.Status.Code != 0 {
		return response, fmt.Errorf("17k search api status %d: %s", response.Status.Code, cleanText(response.Status.Msg))
	}
	return response, nil
}

func n17kSearchAPIBookID(raw json.RawMessage) string {
	value := strings.TrimSpace(string(raw))
	if value == "" || value == "null" {
		return ""
	}
	if strings.HasPrefix(value, `"`) {
		var text string
		if json.Unmarshal(raw, &text) == nil {
			return strings.TrimSpace(text)
		}
		return ""
	}
	if number, err := strconv.ParseInt(value, 10, 64); err == nil && number > 0 {
		return strconv.FormatInt(number, 10)
	}
	return ""
}

func n17kSearchDescription(item n17kSearchAPIBook) string {
	if description := cleanText(item.Introduction); description != "" {
		return description
	}
	category := cleanText(item.BookCategory.Name)
	channel := cleanText(item.BookChannel.Name)
	switch {
	case category != "" && channel != "":
		return channel + " · " + category
	case category != "":
		return category
	default:
		return channel
	}
}
