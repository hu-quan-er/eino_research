package search

import (
	"context"
	"strconv"
	"strings"
)

// Source 是搜索 provider 传入 research 流程的标准引用单元。
//
// ID 通常只在当前结果集内有效，后续 researcher 结果归一化时可能会重写 ID，以保证不同
// researcher 返回的 citation 可以稳定合并。
type Source struct {
	// ID 是当前 research 结果内引用该来源的稳定标识。
	ID string `json:"id"`
	// Title 是搜索结果标题。
	Title string `json:"title"`
	// URL 是来源地址，也是去重的主键。
	URL string `json:"url"`
	// Snippet 是搜索 provider 返回的摘要文本，可用于构造兜底 SourceDocument。
	Snippet string `json:"snippet"`
	// Provider 标识来源 provider，例如 google 或 mock。
	Provider string `json:"provider"`
	// Query 记录发现该来源的搜索 query。
	Query string `json:"query"`
	// RankScore 是规则 ranker 给出的相对排序分数。
	RankScore float64 `json:"rank_score,omitempty"`
	// RankReason 记录主要排序依据，便于调试和审计来源选择。
	RankReason string `json:"rank_reason,omitempty"`
}

// Provider 是 research 引擎对搜索能力的最小抽象。
//
// 实现方应该遵守 context cancellation，并且返回数量不超过 limit 的 Source。
type Provider interface {
	Search(ctx context.Context, query string, limit int) ([]Source, error)
}

// Deduplicate 按 URL 去重，并重新分配连续的 src_N ID。
//
// 适用于 provider 没有稳定 ID，或调用方希望丢弃原始 ID 的场景。
func Deduplicate(in []Source) []Source {
	seen := make(map[string]struct{}, len(in))
	out := make([]Source, 0, len(in))

	for _, source := range in {
		if source.URL == "" {
			continue
		}
		if _, ok := seen[source.URL]; ok {
			continue
		}

		seen[source.URL] = struct{}{}
		source.ID = "src_" + strconv.Itoa(len(out)+1)
		out = append(out, source)
	}

	return out
}

// DeduplicateStable 按 URL 去重，同时保留不冲突的原始 source ID。
//
// 多个 todo execution 汇总 sources 时依赖这个函数，避免已经写入 findings/evidence_refs
// 的 source_id 被无意义重排。
func DeduplicateStable(in []Source) []Source {
	seenURL := make(map[string]struct{}, len(in))
	usedID := make(map[string]struct{}, len(in))
	out := make([]Source, 0, len(in))
	nextID := 1

	for _, source := range in {
		if source.URL == "" {
			continue
		}
		if _, ok := seenURL[source.URL]; ok {
			continue
		}

		source.ID = stableSourceID(source.ID, usedID, &nextID)
		seenURL[source.URL] = struct{}{}
		usedID[source.ID] = struct{}{}
		out = append(out, source)
	}

	return out
}

// stableSourceID 在保留原始 ID 和生成 src_N 之间做折中。
//
// 如果 provider 或上游已经给出不冲突 ID，则继续使用它，避免已经存在的引用失效；
// 如果为空或冲突，则按 next 分配新的 src_N。
func stableSourceID(id string, used map[string]struct{}, next *int) string {
	id = strings.TrimSpace(id)
	if id != "" {
		if _, ok := used[id]; !ok {
			return id
		}
	}
	for {
		candidate := "src_" + strconv.Itoa(*next)
		*next = *next + 1
		if _, ok := used[candidate]; !ok {
			return candidate
		}
	}
}
