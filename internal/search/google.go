package search

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
)

// Google Custom Search provider 的 API 默认值和请求约束。
const (
	// defaultGoogleBaseURL 是 Google Custom Search JSON API 的生产地址。
	defaultGoogleBaseURL = "https://www.googleapis.com/customsearch/v1"
	// maxGoogleSearchLimit 是 Google API num 参数允许的最大值。
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
	// apiKey 是请求中的 key 参数。
	apiKey string
	// cseID 是请求中的 cx 参数。
	cseID string
	// baseURL 是 Custom Search JSON API 地址，可在测试中替换。
	baseURL string
	// client 发起 HTTP 请求；nil 不会出现，因为构造函数会填默认值。
	client *http.Client
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

// googleSearchResponse 是当前代码关心的 Google API 响应子集。
type googleSearchResponse struct {
	// Items 是 Google API 返回的搜索结果数组；没有结果时为空。
	Items []googleSearchItem `json:"items"`
}

// googleSearchItem 是 Google API 单条搜索结果的最小字段集。
type googleSearchItem struct {
	// Title 是搜索结果标题。
	Title string `json:"title"`
	// Link 是结果 URL。
	Link string `json:"link"`
	// Snippet 是结果摘要。
	Snippet string `json:"snippet"`
}
