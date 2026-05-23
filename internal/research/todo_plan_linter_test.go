package research

import "testing"

func TestLintResearchTodoPlanDetectsEvidenceTodoWithoutSearchQueries(t *testing.T) {
	plan := validTodoPlan()
	plan.Todos[1].SearchQueries = nil

	issues := LintResearchTodoPlan(plan)

	assertPlanIssue(t, issues, "missing_search_queries", "todos[1].search_queries")
}

func TestLintResearchTodoPlanDetectsDuplicateTodoQuestions(t *testing.T) {
	plan := validTodoPlan()
	plan.Todos[2].Question = plan.Todos[1].Question

	issues := LintResearchTodoPlan(plan)

	assertPlanIssue(t, issues, "duplicate_todo_question", "todos[2].question")
}

func TestLintResearchTodoPlanDetectsSynthesisWithoutDependencies(t *testing.T) {
	plan := validTodoPlan()
	plan.Todos[2].DependsOn = nil

	issues := LintResearchTodoPlan(plan)

	assertPlanIssue(t, issues, "synthesis_missing_dependencies", "todos[2].depends_on")
}

func TestLintResearchTodoPlanAllowsValidPlan(t *testing.T) {
	issues := LintResearchTodoPlan(validTodoPlan())

	if HasPlanLintErrors(issues) {
		t.Fatalf("LintResearchTodoPlan returned errors for valid plan: %#v", issues)
	}
}

func assertPlanIssue(t *testing.T, issues []PlanIssue, code, path string) {
	t.Helper()

	for _, issue := range issues {
		if issue.Code == code && issue.Path == path && issue.Severity == PlanIssueError {
			return
		}
	}
	t.Fatalf("issues = %#v, want error issue %s at %s", issues, code, path)
}
