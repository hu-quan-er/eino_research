package search

import (
	"context"
	"fmt"
	"strconv"
)

type MockProvider struct{}

func NewMockProvider() *MockProvider {
	return &MockProvider{}
}

func (p *MockProvider) Search(ctx context.Context, query string, limit int) ([]Source, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if limit <= 0 {
		return nil, fmt.Errorf("mock search query %q: limit must be positive", query)
	}

	sources := make([]Source, 0, limit)
	for i := 1; i <= limit; i++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		id := "src_" + strconv.Itoa(i)
		sources = append(sources, Source{
			ID:       id,
			Title:    "Mock result " + strconv.Itoa(i),
			URL:      "https://example.com/mock/" + strconv.Itoa(i),
			Snippet:  "Mock search result for " + query,
			Provider: "mock",
			Query:    query,
		})
	}

	return sources, nil
}
