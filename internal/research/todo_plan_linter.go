package research

import (
	"fmt"
	"strings"
)

const (
	PlanIssueError   = "error"
	PlanIssueWarning = "warning"
)

// PlanIssue 描述 planner 输出中的一个质量问题。
//
// Severity 当前主要使用 error；保留 warning 是为了后续允许非阻断式质量提示。
type PlanIssue struct {
	Severity string
	Code     string
	Path     string
	Message  string
	Hint     string
}

// PlanLintError 把多个 PlanIssue 聚合成 error，供 planner repair prompt 使用。
type PlanLintError struct {
	Issues []PlanIssue
}

func (e PlanLintError) Error() string {
	if len(e.Issues) == 0 {
		return "plan lint failed"
	}

	parts := make([]string, 0, len(e.Issues))
	for _, issue := range e.Issues {
		if issue.Severity != PlanIssueError {
			continue
		}
		parts = append(parts, fmt.Sprintf("%s at %s: %s", issue.Code, issue.Path, issue.Message))
	}
	if len(parts) == 0 {
		return "plan lint failed"
	}
	return "plan lint failed: " + strings.Join(parts, "; ")
}

// LintResearchTodoPlan 对结构合法的 ResearchTodoPlan 做质量检查。
//
// 这些规则面向“能否执行出有效 research”，例如重复问题、过泛 acceptance criteria、缺少
// search_queries 等；它们不替代 Validate 的结构校验。
func LintResearchTodoPlan(plan ResearchTodoPlan) []PlanIssue {
	issues := make([]PlanIssue, 0)
	issues = append(issues, lintSectionCoverage(plan)...)
	issues = append(issues, lintTodoQuestions(plan)...)
	issues = append(issues, lintAcceptanceCriteria(plan)...)
	issues = append(issues, lintSearchQueries(plan)...)
	issues = append(issues, lintSynthesisDependencies(plan)...)
	return issues
}

// HasPlanLintErrors 判断 lint 结果中是否存在阻断执行的 error。
func HasPlanLintErrors(issues []PlanIssue) bool {
	for _, issue := range issues {
		if issue.Severity == PlanIssueError {
			return true
		}
	}
	return false
}

// validateResearchTodoPlanQuality 将 lint error 转为 planner 可感知的 error。
func validateResearchTodoPlanQuality(plan ResearchTodoPlan) error {
	issues := LintResearchTodoPlan(plan)
	if !HasPlanLintErrors(issues) {
		return nil
	}
	errorsOnly := make([]PlanIssue, 0, len(issues))
	for _, issue := range issues {
		if issue.Severity == PlanIssueError {
			errorsOnly = append(errorsOnly, issue)
		}
	}
	return PlanLintError{Issues: errorsOnly}
}

// lintSectionCoverage 要求每个 section 至少包含一个 todo。
func lintSectionCoverage(plan ResearchTodoPlan) []PlanIssue {
	countBySection := make(map[string]int, len(plan.Sections))
	for _, todo := range plan.Todos {
		countBySection[strings.TrimSpace(todo.SectionID)]++
	}

	issues := make([]PlanIssue, 0)
	for i, section := range plan.Sections {
		id := strings.TrimSpace(section.ID)
		if countBySection[id] > 0 {
			continue
		}
		issues = append(issues, PlanIssue{
			Severity: PlanIssueError,
			Code:     "section_without_todos",
			Path:     fmt.Sprintf("sections[%d]", i),
			Message:  "section has no todos",
			Hint:     "Add at least one todo to this section or remove the section.",
		})
	}
	return issues
}

// lintTodoQuestions 检查 todo question 是否重复或过于泛化。
func lintTodoQuestions(plan ResearchTodoPlan) []PlanIssue {
	issues := make([]PlanIssue, 0)
	seen := make(map[string]int, len(plan.Todos))

	for i, todo := range plan.Todos {
		question := normalizeLintText(todo.Question)
		if question == "" {
			continue
		}
		if first, ok := seen[question]; ok {
			issues = append(issues, PlanIssue{
				Severity: PlanIssueError,
				Code:     "duplicate_todo_question",
				Path:     fmt.Sprintf("todos[%d].question", i),
				Message:  fmt.Sprintf("duplicates todos[%d].question", first),
				Hint:     "Make each todo answer a distinct research question.",
			})
		} else {
			seen[question] = i
		}
		if isGenericText(question, genericTodoQuestions()) {
			issues = append(issues, PlanIssue{
				Severity: PlanIssueError,
				Code:     "todo_question_too_generic",
				Path:     fmt.Sprintf("todos[%d].question", i),
				Message:  "todo question is too generic to execute reliably",
				Hint:     "Replace it with a specific, answerable research question.",
			})
		}
	}
	return issues
}

// lintAcceptanceCriteria 检查 acceptance criteria 是否具体到可以验收。
func lintAcceptanceCriteria(plan ResearchTodoPlan) []PlanIssue {
	issues := make([]PlanIssue, 0)
	for i, todo := range plan.Todos {
		for j, criterion := range todo.AcceptanceCriteria {
			if !isGenericText(normalizeLintText(criterion), genericAcceptanceCriteria()) {
				continue
			}
			issues = append(issues, PlanIssue{
				Severity: PlanIssueError,
				Code:     "acceptance_criteria_too_generic",
				Path:     fmt.Sprintf("todos[%d].acceptance_criteria[%d]", i, j),
				Message:  "acceptance criterion is too generic to verify",
				Hint:     "Use a concrete condition that can be checked from the todo output.",
			})
		}
	}
	return issues
}

// lintSearchQueries 检查证据型 todo 是否提供可执行的初始搜索 query。
func lintSearchQueries(plan ResearchTodoPlan) []PlanIssue {
	issues := make([]PlanIssue, 0)
	seenQueries := map[string]string{}

	for i, todo := range plan.Todos {
		if requiresSearchQueries(plan, todo) && countNonEmptyStrings(todo.SearchQueries) == 0 {
			issues = append(issues, PlanIssue{
				Severity: PlanIssueError,
				Code:     "missing_search_queries",
				Path:     fmt.Sprintf("todos[%d].search_queries", i),
				Message:  "evidence-gathering todo has no search queries",
				Hint:     "Add 1 to 3 concise search queries for this todo.",
			})
		}

		for j, query := range todo.SearchQueries {
			normalized := normalizeLintText(query)
			if normalized == "" {
				continue
			}
			path := fmt.Sprintf("todos[%d].search_queries[%d]", i, j)
			if len(query) > 160 {
				issues = append(issues, PlanIssue{
					Severity: PlanIssueError,
					Code:     "search_query_too_long",
					Path:     path,
					Message:  "search query is too long to work well",
					Hint:     "Use a concise keyword-style query instead of a full paragraph.",
				})
			}
			if previousPath, ok := seenQueries[normalized]; ok {
				issues = append(issues, PlanIssue{
					Severity: PlanIssueError,
					Code:     "duplicate_search_query",
					Path:     path,
					Message:  "duplicates " + previousPath,
					Hint:     "Use distinct queries to broaden evidence collection.",
				})
			} else {
				seenQueries[normalized] = path
			}
		}
	}
	return issues
}

// lintSynthesisDependencies 要求 synthesis/final todo 依赖前置证据 todo。
func lintSynthesisDependencies(plan ResearchTodoPlan) []PlanIssue {
	issues := make([]PlanIssue, 0)
	if len(plan.Todos) <= 1 {
		return issues
	}

	for i, todo := range plan.Todos {
		if !isSynthesisTodo(todo) || countNonEmptyStrings(todo.DependsOn) > 0 {
			continue
		}
		issues = append(issues, PlanIssue{
			Severity: PlanIssueError,
			Code:     "synthesis_missing_dependencies",
			Path:     fmt.Sprintf("todos[%d].depends_on", i),
			Message:  "synthesis todo has no dependencies",
			Hint:     "Make synthesis depend on the evidence or background todos it summarizes.",
		})
	}
	return issues
}

// requiresSearchQueries 用 section/todo 关键词判断某个 todo 是否必须提供搜索 query。
func requiresSearchQueries(plan ResearchTodoPlan, todo ResearchTodo) bool {
	if isSynthesisTodo(todo) {
		return false
	}

	section := findTodoSection(plan, todo.SectionID)
	text := normalizeLintText(strings.Join([]string{
		section.ID,
		section.Title,
		section.Description,
		todo.Title,
		todo.Question,
	}, " "))
	for _, keyword := range []string{
		"benchmark",
		"case",
		"collect",
		"compare",
		"current",
		"docs",
		"documentation",
		"evidence",
		"implementation",
		"latest",
		"market",
		"search",
		"source",
	} {
		if strings.Contains(text, keyword) {
			return true
		}
	}
	return false
}

// isSynthesisTodo 用关键词识别汇总/结论型 todo。
func isSynthesisTodo(todo ResearchTodo) bool {
	text := normalizeLintText(todo.Title + " " + todo.Question)
	for _, keyword := range []string{"conclusion", "final", "recommendation", "recommend", "summarize", "synthesis", "synthesize"} {
		if strings.Contains(text, keyword) {
			return true
		}
	}
	return false
}

// findTodoSection 按 section id 查找 section；找不到时返回空结构。
func findTodoSection(plan ResearchTodoPlan, sectionID string) ResearchSection {
	for _, section := range plan.Sections {
		if strings.TrimSpace(section.ID) == strings.TrimSpace(sectionID) {
			return section
		}
	}
	return ResearchSection{}
}

// countNonEmptyStrings 统计非空字符串数量。
func countNonEmptyStrings(values []string) int {
	count := 0
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			count++
		}
	}
	return count
}

// normalizeLintText 统一大小写和空白，降低 lint 比较时的格式噪音。
func normalizeLintText(value string) string {
	return strings.Join(strings.Fields(strings.ToLower(strings.TrimSpace(value))), " ")
}

// isGenericText 判断文本是否命中过泛模板。
func isGenericText(value string, generic map[string]struct{}) bool {
	_, ok := generic[value]
	return ok
}

// genericTodoQuestions 返回常见但不可执行的 todo question 模板。
func genericTodoQuestions() map[string]struct{} {
	return map[string]struct{}{
		"analyze the topic":      {},
		"answer the question":    {},
		"collect information":    {},
		"find information":       {},
		"investigate the topic":  {},
		"research the topic":     {},
		"summarize findings":     {},
		"write the final answer": {},
	}
}

// genericAcceptanceCriteria 返回常见但无法验收的 acceptance criteria 模板。
func genericAcceptanceCriteria() map[string]struct{} {
	return map[string]struct{}{
		"answer the question": {},
		"complete":            {},
		"complete analysis":   {},
		"done":                {},
		"finish task":         {},
		"good answer":         {},
		"provide summary":     {},
	}
}
