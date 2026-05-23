package research

import (
	"fmt"
	"strings"
)

const (
	PlanIssueError   = "error"
	PlanIssueWarning = "warning"
)

type PlanIssue struct {
	Severity string
	Code     string
	Path     string
	Message  string
	Hint     string
}

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

func LintResearchTodoPlan(plan ResearchTodoPlan) []PlanIssue {
	issues := make([]PlanIssue, 0)
	issues = append(issues, lintSectionCoverage(plan)...)
	issues = append(issues, lintTodoQuestions(plan)...)
	issues = append(issues, lintAcceptanceCriteria(plan)...)
	issues = append(issues, lintSearchQueries(plan)...)
	issues = append(issues, lintSynthesisDependencies(plan)...)
	return issues
}

func HasPlanLintErrors(issues []PlanIssue) bool {
	for _, issue := range issues {
		if issue.Severity == PlanIssueError {
			return true
		}
	}
	return false
}

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

func isSynthesisTodo(todo ResearchTodo) bool {
	text := normalizeLintText(todo.Title + " " + todo.Question)
	for _, keyword := range []string{"conclusion", "final", "recommendation", "recommend", "summarize", "synthesis", "synthesize"} {
		if strings.Contains(text, keyword) {
			return true
		}
	}
	return false
}

func findTodoSection(plan ResearchTodoPlan, sectionID string) ResearchSection {
	for _, section := range plan.Sections {
		if strings.TrimSpace(section.ID) == strings.TrimSpace(sectionID) {
			return section
		}
	}
	return ResearchSection{}
}

func countNonEmptyStrings(values []string) int {
	count := 0
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			count++
		}
	}
	return count
}

func normalizeLintText(value string) string {
	return strings.Join(strings.Fields(strings.ToLower(strings.TrimSpace(value))), " ")
}

func isGenericText(value string, generic map[string]struct{}) bool {
	_, ok := generic[value]
	return ok
}

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
