package research

import (
	"context"
	"fmt"
	"sync/atomic"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	"github.com/hu-quan-er/eino_research/internal/search"
)

type SearchLimits struct {
	MaxSearchesPerStep int
	ResultsPerSearch   int
}

type WebSearchInput struct {
	Query string `json:"query" jsonschema:"description=Search query to run"`
	Limit int    `json:"limit,omitempty" jsonschema:"description=Maximum number of results to return"`
}

func NewWebSearchTool(provider search.Provider, limits SearchLimits) (tool.InvokableTool, error) {
	var count atomic.Int64
	return utils.InferTool("web_search", "Search the web for current research sources.", func(ctx context.Context, input WebSearchInput) ([]search.Source, error) {
		if input.Query == "" {
			return nil, fmt.Errorf("query is required")
		}
		next := count.Add(1)
		if limits.MaxSearchesPerStep > 0 && int(next) > limits.MaxSearchesPerStep {
			return nil, fmt.Errorf("search limit exceeded for current step")
		}
		limit := input.Limit
		if limit <= 0 {
			limit = limits.ResultsPerSearch
		}
		if limit <= 0 {
			limit = 5
		}
		return provider.Search(ctx, input.Query, limit)
	})
}
