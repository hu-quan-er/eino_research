package search

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
)

const (
	defaultGoogleBaseURL = "https://www.googleapis.com/customsearch/v1"
	maxGoogleSearchLimit = 10
)

// GoogleConfig 配置 Google Custom Search provider。
//
// BaseURL 和 Client 可注入，方便测试时把请求导向 httptest server。
type GoogleConfig struct {
	// APIKey 是 Google Custom Search API key。
	APIKey string
	// CSEID 是 Google Custom Search Engine ID。
	CSEID string
	// BaseURL 是 API 地址，测试时可替换为 httptest server。
	BaseURL string
	// Client 是 HTTP 客户端，未设置时使用 http.DefaultClient。
	Client *http.Client
}

// GoogleProvider 基于 Google Custom Search JSON API 实现 Provider。
type GoogleProvider struct {
	apiKey  string
	cseID   string
	baseURL string
	client  *http.Client
}

// NewGoogleProvider 创建 GoogleProvider，并在未显式传入 BaseURL/Client 时使用生产默认值。
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

// Search 执行一次 Google Custom Search，并把响应转换为标准 Source。
//
// Google 的 num 参数最多为 10，因此这里会在发请求前拒绝更大的 limit。
func (p *GoogleProvider) Search(ctx context.Context, query string, limit int) ([]Source, error) {
	if limit < 1 || limit > maxGoogleSearchLimit {
		return nil, fmt.Errorf("google search query %q: limit %d out of range 1..10", query, limit)
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
