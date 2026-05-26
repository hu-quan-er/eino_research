package research

import "strings"

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

func compactQueryText(value string) string {
	value = strings.Join(strings.Fields(value), " ")
	runes := []rune(value)
	if len(runes) <= 120 {
		return value
	}
	return string(runes[:120])
}

func limitStrings(values []string, limit int) []string {
	if limit <= 0 || len(values) <= limit {
		return values
	}
	return values[:limit]
}
