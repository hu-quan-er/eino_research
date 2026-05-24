package research

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	"github.com/hu-quan-er/eino_research/internal/search"
	"golang.org/x/net/html"
)

type SearchLimits struct {
	MaxSearchesPerStep int
	ResultsPerSearch   int
	SourceIDPrefix     string
}

type FetchLimits struct {
	MaxFetchesPerStep int
	MaxContentChars   int
	MaxBodyBytes      int64
	Recorder          FetchedPageRecorder
}

type WebSearchInput struct {
	Query string `json:"query" jsonschema:"description=Search query to run"`
	Limit int    `json:"limit,omitempty" jsonschema:"description=Maximum number of results to return"`
}

type WebFetchInput struct {
	URL      string `json:"url" jsonschema:"description=HTTP or HTTPS URL to fetch and read"`
	MaxChars int    `json:"max_chars,omitempty" jsonschema:"description=Maximum number of extracted text characters to return"`
}

type FetchedPage struct {
	URL         string `json:"url"`
	Title       string `json:"title,omitempty"`
	Text        string `json:"text"`
	ContentType string `json:"content_type,omitempty"`
}

type PageFetcher interface {
	Fetch(ctx context.Context, url string, maxChars int) (FetchedPage, error)
}

type FetchedPageRecorder interface {
	RecordFetchedPage(page FetchedPage)
}

type FetchedPageStore struct {
	mu    sync.Mutex
	pages []FetchedPage
}

func NewFetchedPageStore() *FetchedPageStore {
	return &FetchedPageStore{}
}

func (s *FetchedPageStore) RecordFetchedPage(page FetchedPage) {
	if s == nil {
		return
	}
	if strings.TrimSpace(page.URL) == "" || strings.TrimSpace(page.Text) == "" {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.pages = append(s.pages, page)
}

func (s *FetchedPageStore) Pages() []FetchedPage {
	if s == nil {
		return nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]FetchedPage, len(s.pages))
	copy(out, s.pages)
	return out
}

type HTTPPageFetcher struct {
	Client       *http.Client
	MaxBodyBytes int64
}

func NewWebSearchTool(provider search.Provider, limits SearchLimits) (tool.InvokableTool, error) {
	var count atomic.Int64
	var sourceCount atomic.Int64
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
		sources, err := provider.Search(ctx, input.Query, limit)
		if err != nil {
			return nil, err
		}
		prefix := strings.TrimSpace(limits.SourceIDPrefix)
		if prefix == "" {
			prefix = "src"
		}
		for i := range sources {
			sources[i].ID = fmt.Sprintf("%s_%d", prefix, sourceCount.Add(1))
		}
		return sources, nil
	})
}

func NewWebFetchTool(fetcher PageFetcher, limits FetchLimits) (tool.InvokableTool, error) {
	if isNilDependency(fetcher) {
		fetcher = HTTPPageFetcher{MaxBodyBytes: limits.MaxBodyBytes}
	}

	var count atomic.Int64
	return utils.InferTool("web_fetch", "Fetch and read the visible text from a web page URL found by web_search.", func(ctx context.Context, input WebFetchInput) (FetchedPage, error) {
		if strings.TrimSpace(input.URL) == "" {
			return FetchedPage{}, fmt.Errorf("url is required")
		}
		next := count.Add(1)
		if limits.MaxFetchesPerStep > 0 && int(next) > limits.MaxFetchesPerStep {
			return FetchedPage{}, fmt.Errorf("fetch limit exceeded for current step")
		}

		maxChars := limits.MaxContentChars
		if maxChars <= 0 {
			maxChars = 4000
		}
		if input.MaxChars > 0 && input.MaxChars < maxChars {
			maxChars = input.MaxChars
		}
		page, err := fetcher.Fetch(ctx, input.URL, maxChars)
		if err != nil {
			return FetchedPage{}, err
		}
		if limits.Recorder != nil {
			limits.Recorder.RecordFetchedPage(page)
		}
		return page, nil
	})
}

func (f HTTPPageFetcher) Fetch(ctx context.Context, rawURL string, maxChars int) (FetchedPage, error) {
	rawURL = strings.TrimSpace(rawURL)
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return FetchedPage{}, fmt.Errorf("parse URL: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return FetchedPage{}, fmt.Errorf("unsupported URL scheme %q", parsed.Scheme)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return FetchedPage{}, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("User-Agent", "eino-research/0.1")

	client := f.Client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return FetchedPage{}, fmt.Errorf("fetch %s: %w", parsed.String(), err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return FetchedPage{}, fmt.Errorf("fetch %s: HTTP status %s", parsed.String(), resp.Status)
	}

	maxBodyBytes := f.MaxBodyBytes
	if maxBodyBytes <= 0 {
		maxBodyBytes = 1 << 20
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	if err != nil {
		return FetchedPage{}, fmt.Errorf("read body: %w", err)
	}

	contentType := resp.Header.Get("Content-Type")
	title, text := extractReadableText(contentType, string(body))
	return FetchedPage{
		URL:         parsed.String(),
		Title:       title,
		Text:        truncateText(text, maxChars),
		ContentType: contentType,
	}, nil
}

func extractReadableText(contentType, body string) (string, string) {
	if !strings.Contains(strings.ToLower(contentType), "html") {
		return "", normalizeWhitespace(body)
	}

	doc, err := html.Parse(strings.NewReader(body))
	if err != nil {
		return "", normalizeWhitespace(body)
	}

	var titleParts []string
	var textParts []string
	var walk func(*html.Node, bool, bool)
	walk = func(n *html.Node, skip bool, inTitle bool) {
		if n.Type == html.ElementNode {
			name := strings.ToLower(n.Data)
			if name == "script" || name == "style" || name == "noscript" || name == "svg" {
				skip = true
			}
			if name == "title" {
				inTitle = true
			}
		}
		if n.Type == html.TextNode && !skip {
			text := normalizeWhitespace(n.Data)
			if text != "" {
				if inTitle {
					titleParts = append(titleParts, text)
				} else {
					textParts = append(textParts, text)
				}
			}
		}
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			walk(child, skip, inTitle)
		}
	}
	walk(doc, false, false)

	return normalizeWhitespace(strings.Join(titleParts, " ")), normalizeWhitespace(strings.Join(textParts, " "))
}

func normalizeWhitespace(value string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
}

func truncateText(value string, maxChars int) string {
	value = strings.TrimSpace(value)
	if maxChars <= 0 {
		return value
	}
	runes := []rune(value)
	if len(runes) <= maxChars {
		return value
	}
	return string(runes[:maxChars])
}
