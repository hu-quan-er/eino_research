package search

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
)

const defaultGoogleBaseURL = "https://www.googleapis.com/customsearch/v1"

type GoogleConfig struct {
	APIKey  string
	CSEID   string
	BaseURL string
	Client  *http.Client
}

type GoogleProvider struct {
	apiKey  string
	cseID   string
	baseURL string
	client  *http.Client
}

func NewGoogleProvider(cfg GoogleConfig) *GoogleProvider {
	baseURL := cfg.BaseURL
	if baseURL == "" {
		baseURL = defaultGoogleBaseURL
	}

	client := cfg.Client
	if client == nil {
		client = http.DefaultClient
	}

	return &GoogleProvider{
		apiKey:  cfg.APIKey,
		cseID:   cfg.CSEID,
		baseURL: baseURL,
		client:  client,
	}
}

func (p *GoogleProvider) Search(ctx context.Context, query string, limit int) ([]Source, error) {
	if limit <= 0 {
		return nil, fmt.Errorf("google search query %q: limit must be positive", query)
	}

	endpoint, err := url.Parse(p.baseURL)
	if err != nil {
		return nil, fmt.Errorf("google search query %q: parse base URL: %w", query, err)
	}

	params := endpoint.Query()
	params.Set("key", p.apiKey)
	params.Set("cx", p.cseID)
	params.Set("q", query)
	params.Set("num", strconv.Itoa(limit))
	endpoint.RawQuery = params.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("google search query %q: create request: %w", query, err)
	}

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("google search query %q: request failed: %w", query, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("google search query %q: HTTP status %s", query, resp.Status)
	}

	var parsed googleSearchResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, fmt.Errorf("google search query %q: decode response: %w", query, err)
	}

	sources := make([]Source, 0, len(parsed.Items))
	for i, item := range parsed.Items {
		sources = append(sources, Source{
			ID:       "src_" + strconv.Itoa(i+1),
			Title:    item.Title,
			URL:      item.Link,
			Snippet:  item.Snippet,
			Provider: "google",
			Query:    query,
		})
	}

	return sources, nil
}

type googleSearchResponse struct {
	Items []googleSearchItem `json:"items"`
}

type googleSearchItem struct {
	Title   string `json:"title"`
	Link    string `json:"link"`
	Snippet string `json:"snippet"`
}
