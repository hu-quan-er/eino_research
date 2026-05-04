package research

import (
	"context"
	"strings"
	"testing"

	"github.com/hu-quan-er/eino_research/internal/search"
)

type recordingProvider struct {
	calls []string
}

func (p *recordingProvider) Search(ctx context.Context, query string, limit int) ([]search.Source, error) {
	p.calls = append(p.calls, query)
	return []search.Source{{ID: "src_1", Title: "Result", URL: "https://example.com", Provider: "mock", Query: query}}, nil
}

func TestWebSearchToolRunsProvider(t *testing.T) {
	provider := &recordingProvider{}
	tool, err := NewWebSearchTool(provider, SearchLimits{MaxSearchesPerStep: 2, ResultsPerSearch: 5})
	if err != nil {
		t.Fatalf("NewWebSearchTool: %v", err)
	}

	out, err := tool.InvokableRun(context.Background(), `{"query":"eino","limit":3}`)
	if err != nil {
		t.Fatalf("InvokableRun: %v", err)
	}
	if !strings.Contains(out, "https://example.com") {
		t.Fatalf("output = %s", out)
	}
	if len(provider.calls) != 1 || provider.calls[0] != "eino" {
		t.Fatalf("calls = %+v", provider.calls)
	}
}

func TestWebSearchToolEnforcesLimit(t *testing.T) {
	provider := &recordingProvider{}
	tool, err := NewWebSearchTool(provider, SearchLimits{MaxSearchesPerStep: 1, ResultsPerSearch: 5})
	if err != nil {
		t.Fatalf("NewWebSearchTool: %v", err)
	}

	if _, err := tool.InvokableRun(context.Background(), `{"query":"one"}`); err != nil {
		t.Fatalf("first search returned error: %v", err)
	}
	_, err = tool.InvokableRun(context.Background(), `{"query":"two"}`)
	if err == nil {
		t.Fatal("second search returned nil error, want limit error")
	}
	if !strings.Contains(err.Error(), "search limit exceeded") {
		t.Fatalf("error = %q", err.Error())
	}
}
