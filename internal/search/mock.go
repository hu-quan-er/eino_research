package search

import (
	"context"
	"fmt"
	"strconv"
)

// MockProvider 是测试和本地 dry-run 使用的确定性 Provider。
//
// 它让 planner/executor/render 等主流程在没有真实网络凭据时也能跑通。
type MockProvider struct{}

// NewMockProvider 创建一个确定性的 mock 搜索 provider。
func NewMockProvider() *MockProvider {
	return &MockProvider{}
}

// Search 返回稳定的 example.com 结果，同时遵守 context cancellation，方便测试调度器和
// runner 的取消路径。
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
