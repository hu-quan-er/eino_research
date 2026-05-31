package research

import "strings"

// defaultExpandedSearchQueries 限制扩展后的初始 query 数量，控制 researcher prompt 长度。
const defaultExpandedSearchQueries = 8

// ExpandTodoSearchQueries 为 todo 生成 researcher 初始检索 query 列表。
//
// planner 给出的 search_queries 仍然作为最高优先级；这里追加 official/source-scoped、
// freshness、comparison、quantitative、counterexample 等变体，提高第一轮检索覆盖面。
func ExpandTodoSearchQueries(todo ResearchTodo, plan ResearchTodoPlan) []string {
	baseQueries := nonEmptyStrings(todo.SearchQueries)
	topic := compactQueryText(firstNonEmptyQuery(baseQueries, todo.Question, todo.Title, plan.Objective))
	if topic == "" {
		return baseQueries
	}

	queries := make([]string, 0, defaultExpandedSearchQueries)
	queries = append(queries, baseQueries...)
	if len(baseQueries) == 0 {
		queries = append(queries, topic)
	}
	queries = append(queries, topic+" official documentation")
	if todoNeedsImplementationFocus(todo, plan) {
		queries = append(queries, topic+" github repository examples")
	}
	if todoNeedsFreshness(todo, plan) {
		queries = append(queries, topic+" latest 2026")
	}
	if todoNeedsComparisonFocus(todo, plan) {
		queries = append(queries, topic+" alternatives comparison tradeoffs")
	}
	if todoNeedsQuantitativeFocus(todo, plan) {
		queries = append(queries, topic+" benchmark performance pricing metrics")
	}
	queries = append(queries, topic+" limitations risks counterexamples")

	return limitStrings(dedupeStrings(queries), defaultExpandedSearchQueries)
}

// firstNonEmptyQuery 按优先级从字符串或字符串数组中取第一个非空值。
//
// 用 any 是为了让调用处可以自然传入 search_queries、question、title、objective，
// 同时保持“第一个有效上下文作为 topic”的规则集中在一个函数里。
func firstNonEmptyQuery(values ...any) string {
	for _, value := range values {
		switch v := value.(type) {
		case []string:
			for _, item := range v {
				if item = strings.TrimSpace(item); item != "" {
					return item
				}
			}
		case string:
			if v = strings.TrimSpace(v); v != "" {
				return v
			}
		}
	}
	return ""
}

// compactQueryText 把 query topic 压缩到适合搜索框的长度。
//
// 截断按 rune 计算，避免中文等多字节字符被截坏。
func compactQueryText(value string) string {
	value = strings.Join(strings.Fields(value), " ")
	runes := []rune(value)
	if len(runes) <= 120 {
		return value
	}
	return string(runes[:120])
}

// limitStrings 保留前 limit 个字符串；limit <= 0 表示不截断。
func limitStrings(values []string, limit int) []string {
	if limit <= 0 || len(values) <= limit {
		return values
	}
	return values[:limit]
}
