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
		maxResults := limits.ResultsPerSearch
		if maxResults <= 0 {
			maxResults = 5
		}
		limit := maxResults
		if input.Limit > 0 && input.Limit < maxResults {
			limit = input.Limit
		}
		return provider.Search(ctx, input.Query, limit)
	})
}
