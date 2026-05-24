package research

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hu-quan-er/eino_research/internal/search"
)

type recordingProvider struct {
	calls  []string
	limits []int
}

func (p *recordingProvider) Search(ctx context.Context, query string, limit int) ([]search.Source, error) {
	p.calls = append(p.calls, query)
	p.limits = append(p.limits, limit)
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

func TestWebSearchToolCapsInputLimit(t *testing.T) {
	provider := &recordingProvider{}
	tool, err := NewWebSearchTool(provider, SearchLimits{MaxSearchesPerStep: 2, ResultsPerSearch: 5})
	if err != nil {
		t.Fatalf("NewWebSearchTool: %v", err)
	}

	if _, err := tool.InvokableRun(context.Background(), `{"query":"eino","limit":100}`); err != nil {
		t.Fatalf("InvokableRun: %v", err)
	}
	if len(provider.limits) != 1 || provider.limits[0] != 5 {
		t.Fatalf("limits = %+v, want [5]", provider.limits)
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

func TestWebFetchToolFetchesHTMLContent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<html>
			<head><title>Example Research Page</title><script>hidden()</script></head>
			<body><main><h1>Visible Heading</h1><p>Useful evidence for the report.</p></main></body>
		</html>`))
	}))
	defer server.Close()

	tool, err := NewWebFetchTool(HTTPPageFetcher{Client: server.Client()}, FetchLimits{MaxFetchesPerStep: 1, MaxContentChars: 2000})
	if err != nil {
		t.Fatalf("NewWebFetchTool: %v", err)
	}

	out, err := tool.InvokableRun(context.Background(), `{"url":"`+server.URL+`"}`)
	if err != nil {
		t.Fatalf("InvokableRun: %v", err)
	}
	for _, want := range []string{"Example Research Page", "Visible Heading", "Useful evidence"} {
		if !strings.Contains(out, want) {
			t.Fatalf("output = %s, want %q", out, want)
		}
	}
	if strings.Contains(out, "hidden") {
		t.Fatalf("output = %s, want script content removed", out)
	}
}

func TestWebFetchToolRecordsFetchedPages(t *testing.T) {
	fetcher := &recordingFetcher{}
	recorder := NewFetchedPageStore()
	tool, err := NewWebFetchTool(fetcher, FetchLimits{
		MaxFetchesPerStep: 2,
		MaxContentChars:   1000,
		Recorder:          recorder,
	})
	if err != nil {
		t.Fatalf("NewWebFetchTool: %v", err)
	}

	if _, err := tool.InvokableRun(context.Background(), `{"url":"https://example.com/recorded"}`); err != nil {
		t.Fatalf("InvokableRun: %v", err)
	}

	pages := recorder.Pages()
	if len(pages) != 1 {
		t.Fatalf("recorded pages = %d, want 1", len(pages))
	}
	if pages[0].URL != "https://example.com/recorded" {
		t.Fatalf("recorded URL = %q, want https://example.com/recorded", pages[0].URL)
	}
	if pages[0].Text != "Recorded content" {
		t.Fatalf("recorded text = %q, want Recorded content", pages[0].Text)
	}
}

func TestWebFetchToolEnforcesLimit(t *testing.T) {
	fetcher := &recordingFetcher{}
	tool, err := NewWebFetchTool(fetcher, FetchLimits{MaxFetchesPerStep: 1, MaxContentChars: 1000})
	if err != nil {
		t.Fatalf("NewWebFetchTool: %v", err)
	}

	if _, err := tool.InvokableRun(context.Background(), `{"url":"https://example.com/one"}`); err != nil {
		t.Fatalf("first fetch returned error: %v", err)
	}
	_, err = tool.InvokableRun(context.Background(), `{"url":"https://example.com/two"}`)
	if err == nil {
		t.Fatal("second fetch returned nil error, want limit error")
	}
	if !strings.Contains(err.Error(), "fetch limit exceeded") {
		t.Fatalf("error = %q", err.Error())
	}
}

func TestHTTPPageFetcherRejectsNonHTTPURL(t *testing.T) {
	_, err := HTTPPageFetcher{}.Fetch(context.Background(), "file:///etc/passwd", 1000)
	if err == nil {
		t.Fatal("Fetch returned nil error, want scheme error")
	}
	if !strings.Contains(err.Error(), "unsupported URL scheme") {
		t.Fatalf("error = %q", err.Error())
	}
}

type recordingFetcher struct {
	calls []string
}

func (f *recordingFetcher) Fetch(_ context.Context, url string, _ int) (FetchedPage, error) {
	f.calls = append(f.calls, url)
	return FetchedPage{
		URL:   url,
		Title: "Recorded",
		Text:  "Recorded content",
	}, nil
}
