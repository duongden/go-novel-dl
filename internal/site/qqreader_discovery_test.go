package site

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

func TestQQReaderDiscoveryListsExposeOfficialRankingsAndBooklists(t *testing.T) {
	site := NewQQReaderSite(testQQReaderConfig())
	lists, err := site.DiscoveryLists(context.Background())
	if err != nil {
		t.Fatalf("DiscoveryLists: %v", err)
	}
	if len(lists) != len(qqReaderRankingSpecs)+len(qqReaderBooklistSpecs) {
		t.Fatalf("unexpected discovery list count: %d", len(lists))
	}
	rankings := 0
	booklists := 0
	for _, item := range lists {
		switch item.Kind {
		case "ranking":
			rankings++
		case "booklist":
			booklists++
		default:
			t.Fatalf("unexpected list kind: %+v", item)
		}
	}
	if rankings != 13 || booklists != 6 {
		t.Fatalf("unexpected discovery list categories: rankings=%d booklists=%d", rankings, booklists)
	}
}

func TestQQReaderDiscoveryRankingPaginatesAndDeduplicates(t *testing.T) {
	requestedPages := make([]string, 0, 2)
	site := newQQReaderTestSite(t, func(req *http.Request) (*http.Response, error) {
		if req.URL.Path != "/api/book/rank" || req.URL.Query().Get("columnId") != "534263" {
			return qqReaderTestResponse(req, http.StatusBadRequest, `{}`), nil
		}
		pageNo := req.URL.Query().Get("pageNo")
		requestedPages = append(requestedPages, pageNo)
		switch pageNo {
		case "1":
			return qqReaderTestResponse(req, http.StatusOK, `{
				"code":0,"msg":"ok","data":{"totalBooks":3,"books":[
					{"bid":101,"title":"First book","author":"Author A","intro":"Intro A"},
					{"bid":102,"title":"Second book","author":"Author B","intro":"Intro B"}
				]}
			}`), nil
		case "2":
			return qqReaderTestResponse(req, http.StatusOK, `{
				"code":0,"msg":"ok","data":{"totalBooks":3,"books":[
					{"bid":102,"title":"Second book duplicate","author":"Author B","intro":"Intro B"},
					{"bid":103,"title":"Third book","author":"Author C","intro":"Intro C"}
				]}
			}`), nil
		default:
			return qqReaderTestResponse(req, http.StatusBadRequest, `{}`), nil
		}
	})

	results, err := site.DiscoveryBooks(context.Background(), "male-sell", 3)
	if err != nil {
		t.Fatalf("DiscoveryBooks: %v", err)
	}
	if len(results) != 3 {
		t.Fatalf("expected three results, got %+v", results)
	}
	if results[0].BookID != "101" || results[2].BookID != "103" {
		t.Fatalf("unexpected ranking order: %+v", results)
	}
	if results[0].URL != "https://book.qq.com/book-detail/101" {
		t.Fatalf("unexpected ranking URL: %s", results[0].URL)
	}
	if strings.Join(requestedPages, ",") != "1,2" {
		t.Fatalf("unexpected requested pages: %v", requestedPages)
	}
}

func TestQQReaderDiscoveryBooklistCombinesTagsAndDeduplicates(t *testing.T) {
	requestedTags := make([]string, 0, 2)
	site := newQQReaderTestSite(t, func(req *http.Request) (*http.Response, error) {
		if req.URL.Path != "/api/site/seo/new/book/list" {
			return qqReaderTestResponse(req, http.StatusBadRequest, `{}`), nil
		}
		tagID := req.URL.Query().Get("tagId")
		requestedTags = append(requestedTags, tagID)
		switch tagID {
		case "9000722":
			return qqReaderTestResponse(req, http.StatusOK, `{
				"code":0,"msg":"ok","data":{"tagName":"Travel","list":[
					{"relatedBooks":[
						{"bid":201,"title":"Book 201","author":"Author 201","intro":"Intro 201"},
						{"bid":202,"title":"Book 202","author":"Author 202","intro":"Intro 202"}
					]}
				]}
			}`), nil
		case "8000565":
			return qqReaderTestResponse(req, http.StatusOK, `{
				"code":0,"msg":"ok","data":{"tagName":"Travel","list":[
					{"relatedBooks":[
						{"bid":202,"title":"Book 202 duplicate","author":"Author 202","intro":"Intro 202"},
						{"bid":203,"title":"Book 203","author":"Author 203","intro":"Intro 203"}
					]}
				]}
			}`), nil
		default:
			return qqReaderTestResponse(req, http.StatusBadRequest, `{}`), nil
		}
	})

	results, err := site.DiscoveryBooks(context.Background(), "booklist-travel", 3)
	if err != nil {
		t.Fatalf("DiscoveryBooks: %v", err)
	}
	if len(results) != 3 || results[0].BookID != "201" || results[2].BookID != "203" {
		t.Fatalf("unexpected booklist results: %+v", results)
	}
	if strings.Join(requestedTags, ",") != "9000722,8000565" {
		t.Fatalf("unexpected requested tags: %v", requestedTags)
	}
}

func TestQQReaderDiscoveryRejectsUnknownList(t *testing.T) {
	site := NewQQReaderSite(testQQReaderConfig())
	if _, err := site.DiscoveryBooks(context.Background(), "missing", 20); err == nil {
		t.Fatal("expected unknown discovery list error")
	}
}
