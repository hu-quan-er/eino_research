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

// SearchLimits 控制单个 step/todo 内 web_search 工具的预算和 source ID 前缀。
type SearchLimits struct {
	// MaxSearchesPerStep 是该工具实例最多允许调用 web_search 的次数。
	MaxSearchesPerStep int
	// ResultsPerSearch 是每次搜索默认返回的结果数量。
	ResultsPerSearch int
	// SourceIDPrefix 用于重写 source id，避免不同 step/todo 间冲突。
	SourceIDPrefix string
}

// FetchLimits 控制单个 step/todo 内 web_fetch 工具的预算、正文长度和抓取结果记录器。
type FetchLimits struct {
	// MaxFetchesPerStep 是该工具实例最多允许调用 web_fetch 的次数。
	MaxFetchesPerStep int
	// MaxContentChars 是抽取文本返回给模型的最大字符数。
	MaxContentChars int
	// MaxBodyBytes 是 HTTP 响应体读取上限，防止大页面占用过多内存。
	MaxBodyBytes int64
	// Recorder 记录成功 fetch 的页面，供执行结束后转为 SourceDocument。
	Recorder FetchedPageRecorder
}

// WebSearchInput 是暴露给模型的 web_search 工具入参。
type WebSearchInput struct {
	// Query 是模型希望执行的搜索 query。
	Query string `json:"query" jsonschema:"description=Search query to run"`
	// Limit 是模型请求的结果上限，最终还会受 ResultsPerSearch 约束。
	Limit int `json:"limit,omitempty" jsonschema:"description=Maximum number of results to return"`
}

// WebFetchInput 是暴露给模型的 web_fetch 工具入参。
type WebFetchInput struct {
	// URL 是要读取的 HTTP/HTTPS 页面地址，通常来自 web_search 结果。
	URL string `json:"url" jsonschema:"description=HTTP or HTTPS URL to fetch and read"`
	// MaxChars 是模型请求的返回文本上限，最终还会受 MaxContentChars 约束。
	MaxChars int `json:"max_chars,omitempty" jsonschema:"description=Maximum number of extracted text characters to return"`
}

// FetchedPage 是 web_fetch 抓取并抽取可读文本后的结果。
type FetchedPage struct {
	// URL 是规范化后的最终请求 URL。
	URL string `json:"url"`
	// Title 是 HTML title 或空值。
	Title string `json:"title,omitempty"`
	// Text 是抽取并截断后的可读正文。
	Text string `json:"text"`
	// ContentType 是 HTTP Content-Type，便于调试抽取策略。
	ContentType string `json:"content_type,omitempty"`
}

// PageFetcher 抽象实际页面抓取逻辑，便于测试中替换 HTTP 实现。
type PageFetcher interface {
	Fetch(ctx context.Context, url string, maxChars int) (FetchedPage, error)
}

// FetchedPageRecorder 记录 web_fetch 成功读取的页面，执行结束后会转换为 SourceDocument。
type FetchedPageRecorder interface {
	RecordFetchedPage(page FetchedPage)
}

// FetchedPageStore 是线程安全的页面记录器。
//
// 多个 researcher 可能并行调用 web_fetch，因此这里用 mutex 保护内部 slice。
type FetchedPageStore struct {
	mu    sync.Mutex
	pages []FetchedPage
}

// NewFetchedPageStore 创建页面抓取记录器。
func NewFetchedPageStore() *FetchedPageStore {
	return &FetchedPageStore{}
}

// RecordFetchedPage 保存非空 URL 且非空正文的页面。
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

// Pages 返回已记录页面的副本，避免调用方修改内部状态。
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

// HTTPPageFetcher 使用 net/http 抓取页面，并抽取 HTML 可见文本。
type HTTPPageFetcher struct {
	// Client 是 HTTP 客户端，未设置时使用 http.DefaultClient。
	Client *http.Client
	// MaxBodyBytes 是读取响应体的最大字节数；<=0 时使用默认 1MiB。
	MaxBodyBytes int64
}

// NewWebSearchTool 创建模型可调用的 web_search 工具。
//
// 工具内部使用 atomic 计数限制调用次数；返回的 Source ID 会按 SourceIDPrefix 重写，
// 避免不同 step/todo 的 source ID 冲突。
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
			// 模型可以请求更少结果，但不能突破工具配置的上限。
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
			// provider 返回的 ID 可能在不同 query 间重复，这里统一改成本 step/todo 局部递增 ID。
			sources[i].ID = fmt.Sprintf("%s_%d", prefix, sourceCount.Add(1))
		}
		return sources, nil
	})
}

// NewWebFetchTool 创建模型可调用的 web_fetch 工具。
//
// fetch 成功后会写入 Recorder，供执行层在 step 结束时生成更完整的 SourceDocument。
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
			// 模型可以主动缩短返回正文，降低上下文占用。
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

// Fetch 抓取 HTTP/HTTPS 页面并返回截断后的可读文本。
//
// 非 HTML 响应会按纯文本处理；HTML 响应会过滤 script/style/noscript/svg 等不可读节点。
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
	// LimitReader 防止抓取超大页面时把整个响应读入内存。
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

// extractReadableText 从响应正文中抽取标题和正文文本。
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

// normalizeWhitespace 把多种空白压缩为单个空格，便于 chunk 和 quote 稳定比较。
func normalizeWhitespace(value string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
}

// truncateText 按 rune 截断文本，避免中文等多字节字符被截坏。
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
