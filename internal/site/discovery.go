package site

import (
	"context"

	"github.com/guohuiyuan/go-novel-dl/internal/model"
)

// DiscoveryProvider is implemented by sites that publish public rankings or
// curated book lists. It is optional so adding discovery does not change the
// existing Site contract for every source.
type DiscoveryProvider interface {
	DiscoveryLists(ctx context.Context) ([]DiscoveryList, error)
	DiscoveryBooks(ctx context.Context, listID string, limit int) ([]model.SearchResult, error)
}

type DiscoveryList struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	Description     string `json:"description,omitempty"`
	Kind            string `json:"kind"`
	CoverURL        string `json:"cover_url,omitempty"`
	UpdateFrequency string `json:"update_frequency,omitempty"`
	BookCount       int    `json:"book_count,omitempty"`
}

func cloneDiscoveryLists(items []DiscoveryList) []DiscoveryList {
	if len(items) == 0 {
		return nil
	}
	cloned := make([]DiscoveryList, len(items))
	copy(cloned, items)
	return cloned
}

func normalizeDiscoveryLimit(limit, fallback int) int {
	if limit <= 0 {
		limit = fallback
	}
	if limit < 1 {
		return 1
	}
	if limit > 50 {
		return 50
	}
	return limit
}
