package site

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/guohuiyuan/go-novel-dl/internal/model"
)

type qqReaderRankingSpec struct {
	ColumnID int
	List     DiscoveryList
}

type qqReaderBooklistSpec struct {
	TagIDs []int
	List   DiscoveryList
}

var qqReaderRankingSpecs = []qqReaderRankingSpec{
	{
		ColumnID: 534263,
		List: DiscoveryList{
			ID:              "male-sell",
			Name:            "男生热门榜",
			Description:     "男生上架30天内的热门作品",
			Kind:            "ranking",
			UpdateFrequency: "实时更新",
		},
	},
	{
		ColumnID: 534269,
		List: DiscoveryList{
			ID:              "male-new",
			Name:            "男生新书榜",
			Description:     "近30天发布、按成长潜力排序的新书",
			Kind:            "ranking",
			UpdateFrequency: "每日更新",
		},
	},
	{
		ColumnID: 532707,
		List: DiscoveryList{
			ID:              "male-free",
			Name:            "男生免费榜",
			Description:     "近7天免费作品人气排行",
			Kind:            "ranking",
			UpdateFrequency: "每日更新",
		},
	},
	{
		ColumnID: 534271,
		List: DiscoveryList{
			ID:              "male-finish",
			Name:            "男生完结榜",
			Description:     "完结作品按最新阅读热度排序",
			Kind:            "ranking",
			UpdateFrequency: "每日更新",
		},
	},
	{
		ColumnID: 541569,
		List: DiscoveryList{
			ID:              "male-god",
			Name:            "男生封神榜",
			Description:     "2021年度男生封神作品",
			Kind:            "ranking",
			UpdateFrequency: "年度榜",
		},
	},
	{
		ColumnID: 534274,
		List: DiscoveryList{
			ID:              "female-sell",
			Name:            "女生热门榜",
			Description:     "女生上架30天内的热门作品",
			Kind:            "ranking",
			UpdateFrequency: "实时更新",
		},
	},
	{
		ColumnID: 534280,
		List: DiscoveryList{
			ID:              "female-new",
			Name:            "女生新书榜",
			Description:     "近30天发布、按成长潜力排序的新书",
			Kind:            "ranking",
			UpdateFrequency: "每日更新",
		},
	},
	{
		ColumnID: 532790,
		List: DiscoveryList{
			ID:              "female-free",
			Name:            "女生免费榜",
			Description:     "近7天免费作品人气排行",
			Kind:            "ranking",
			UpdateFrequency: "每日更新",
		},
	},
	{
		ColumnID: 534282,
		List: DiscoveryList{
			ID:              "female-finish",
			Name:            "女生完结榜",
			Description:     "完结作品按最新阅读热度排序",
			Kind:            "ranking",
			UpdateFrequency: "每日更新",
		},
	},
	{
		ColumnID: 541570,
		List: DiscoveryList{
			ID:              "female-god",
			Name:            "女生封神榜",
			Description:     "2021年度女生封神作品",
			Kind:            "ranking",
			UpdateFrequency: "年度榜",
		},
	},
	{
		ColumnID: 534064,
		List: DiscoveryList{
			ID:              "publish-sell",
			Name:            "出版热门榜",
			Description:     "近24小时出版作品销量排行",
			Kind:            "ranking",
			UpdateFrequency: "实时更新",
		},
	},
	{
		ColumnID: 534065,
		List: DiscoveryList{
			ID:              "publish-new",
			Name:            "出版新书榜",
			Description:     "近7日出版新书人气排行",
			Kind:            "ranking",
			UpdateFrequency: "每日更新",
		},
	},
	{
		ColumnID: 534223,
		List: DiscoveryList{
			ID:              "publish-knowledge",
			Name:            "出版新知榜",
			Description:     "非小说出版作品近7日热销排行",
			Kind:            "ranking",
			UpdateFrequency: "每日更新",
		},
	},
}

var qqReaderBooklistSpecs = []qqReaderBooklistSpec{
	{
		TagIDs: []int{9000722, 8000565},
		List: DiscoveryList{
			ID:              "booklist-travel",
			Name:            "穿越书单",
			Description:     "QQ阅读穿越题材相关书单精选",
			Kind:            "booklist",
			UpdateFrequency: "持续更新",
		},
	},
	{
		TagIDs: []int{9000837, 8000714},
		List: DiscoveryList{
			ID:              "booklist-rebirth",
			Name:            "重生书单",
			Description:     "QQ阅读重生题材相关书单精选",
			Kind:            "booklist",
			UpdateFrequency: "持续更新",
		},
	},
	{
		TagIDs: []int{9001035, 8000667},
		List: DiscoveryList{
			ID:              "booklist-campus",
			Name:            "校园甜宠",
			Description:     "校园与甜宠题材书单精选",
			Kind:            "booklist",
			UpdateFrequency: "持续更新",
		},
	},
	{
		TagIDs: []int{9000723, 8000571},
		List: DiscoveryList{
			ID:              "booklist-esports",
			Name:            "电竞书单",
			Description:     "QQ阅读电竞题材相关书单精选",
			Kind:            "booklist",
			UpdateFrequency: "持续更新",
		},
	},
	{
		TagIDs: []int{9000785, 8000648},
		List: DiscoveryList{
			ID:              "booklist-light",
			Name:            "轻松书单",
			Description:     "节奏轻快、阅读压力小的作品书单",
			Kind:            "booklist",
			UpdateFrequency: "持续更新",
		},
	},
	{
		TagIDs: []int{9000788, 9000795},
		List: DiscoveryList{
			ID:              "booklist-hotblood",
			Name:            "热血升级",
			Description:     "热血成长与升级流作品书单",
			Kind:            "booklist",
			UpdateFrequency: "持续更新",
		},
	},
}

type qqReaderDiscoveryBook struct {
	BookID     int64  `json:"bid"`
	Title      string `json:"title"`
	Author     string `json:"author"`
	Intro      string `json:"intro"`
	Finished   int    `json:"finished"`
	TotalWords int64  `json:"totalWords"`
}

type qqReaderRankingEnvelope struct {
	Code int    `json:"code"`
	Msg  string `json:"msg"`
	Data struct {
		Books      []qqReaderDiscoveryBook `json:"books"`
		TotalBooks int                     `json:"totalBooks"`
	} `json:"data"`
}

type qqReaderBooklistEnvelope struct {
	Code int    `json:"code"`
	Msg  string `json:"msg"`
	Data struct {
		TagName string `json:"tagName"`
		List    []struct {
			RelatedBooks []qqReaderDiscoveryBook `json:"relatedBooks"`
		} `json:"list"`
	} `json:"data"`
}

var _ DiscoveryProvider = (*QQReaderSite)(nil)

func (s *QQReaderSite) DiscoveryLists(ctx context.Context) ([]DiscoveryList, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	lists := make([]DiscoveryList, 0, len(qqReaderRankingSpecs)+len(qqReaderBooklistSpecs))
	for _, item := range qqReaderRankingSpecs {
		lists = append(lists, item.List)
	}
	for _, item := range qqReaderBooklistSpecs {
		lists = append(lists, item.List)
	}
	return cloneDiscoveryLists(lists), nil
}

func (s *QQReaderSite) DiscoveryBooks(ctx context.Context, listID string, limit int) ([]model.SearchResult, error) {
	listID = strings.TrimSpace(listID)
	for _, item := range qqReaderRankingSpecs {
		if item.List.ID == listID {
			return s.fetchQQReaderRanking(ctx, item, normalizeDiscoveryLimit(limit, 20))
		}
	}
	for _, item := range qqReaderBooklistSpecs {
		if item.List.ID == listID {
			return s.fetchQQReaderBooklist(ctx, item, normalizeDiscoveryLimit(limit, 20))
		}
	}
	return nil, fmt.Errorf("qqreader discovery list %q not found", listID)
}

func (s *QQReaderSite) fetchQQReaderRanking(
	ctx context.Context,
	spec qqReaderRankingSpec,
	limit int,
) ([]model.SearchResult, error) {
	results := make([]model.SearchResult, 0, limit)
	seen := make(map[string]struct{}, limit)
	for pageNo := 1; len(results) < limit; pageNo++ {
		endpoint := url.Values{}
		endpoint.Set("columnId", strconv.Itoa(spec.ColumnID))
		endpoint.Set("pageNo", strconv.Itoa(pageNo))
		rawURL := qqReaderBaseURL + "/api/book/rank?" + endpoint.Encode()

		var envelope qqReaderRankingEnvelope
		if err := s.getJSON(ctx, rawURL, &envelope); err != nil {
			return nil, err
		}
		if err := qqReaderEnvelopeError("ranking", spec.List.ID, envelope.Code, envelope.Msg); err != nil {
			return nil, err
		}
		if len(envelope.Data.Books) == 0 {
			break
		}
		before := len(results)
		for _, item := range envelope.Data.Books {
			appendQQReaderDiscoveryBook(&results, seen, item)
			if len(results) >= limit {
				break
			}
		}
		if len(results) == before {
			break
		}
	}
	return results, nil
}

func (s *QQReaderSite) fetchQQReaderBooklist(
	ctx context.Context,
	spec qqReaderBooklistSpec,
	limit int,
) ([]model.SearchResult, error) {
	results := make([]model.SearchResult, 0, limit)
	seen := make(map[string]struct{}, limit)
	var firstErr error
	for _, tagID := range spec.TagIDs {
		endpoint := url.Values{}
		endpoint.Set("tagId", strconv.Itoa(tagID))
		endpoint.Set("pageNo", "1")
		endpoint.Set("pageSize", strconv.Itoa(limit))
		rawURL := qqReaderBaseURL + "/api/site/seo/new/book/list?" + endpoint.Encode()

		var envelope qqReaderBooklistEnvelope
		if err := s.getJSON(ctx, rawURL, &envelope); err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if err := qqReaderEnvelopeError("booklist", strconv.Itoa(tagID), envelope.Code, envelope.Msg); err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		for _, group := range envelope.Data.List {
			for _, item := range group.RelatedBooks {
				appendQQReaderDiscoveryBook(&results, seen, item)
				if len(results) >= limit {
					return results, nil
				}
			}
		}
	}
	if len(results) == 0 && firstErr != nil {
		return nil, firstErr
	}
	return results, nil
}

func appendQQReaderDiscoveryBook(
	results *[]model.SearchResult,
	seen map[string]struct{},
	item qqReaderDiscoveryBook,
) {
	if item.BookID <= 0 {
		return
	}
	bookID := strconv.FormatInt(item.BookID, 10)
	if _, exists := seen[bookID]; exists {
		return
	}
	title := strings.TrimSpace(item.Title)
	if title == "" {
		return
	}
	seen[bookID] = struct{}{}
	*results = append(*results, model.SearchResult{
		Site:        "qqreader",
		BookID:      bookID,
		Title:       title,
		Author:      strings.TrimSpace(item.Author),
		Description: cleanText(item.Intro),
		URL:         qqReaderBookURL(bookID),
		CoverURL:    qqReaderCoverURL(bookID),
	})
}
