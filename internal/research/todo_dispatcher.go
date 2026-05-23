package research

import (
	"context"
	"fmt"
	"strings"
)

type TodoResearchRole struct {
	ID    string
	Name  string
	Focus string
}

type TodoResearchBudget struct {
	MaxSearches int
	MaxFetches  int
	MaxTokens   int
}

type TodoResearchContext struct {
	Objective            string
	Todo                 ResearchTodo
	DependencyExecutions []TodoExecution
}

type TodoResearchJob struct {
	TodoID  string
	RoleID  string
	Name    string
	Focus   string
	Context TodoResearchContext
	Budget  TodoResearchBudget
}

type TodoDispatchInput struct {
	Plan                 ResearchTodoPlan
	Todo                 ResearchTodo
	DependencyExecutions []TodoExecution
	Budget               TodoResearchBudget
}

type TodoDispatcher interface {
	Dispatch(ctx context.Context, in TodoDispatchInput) ([]TodoResearchJob, error)
}

type RuleBasedTodoDispatcher struct {
	MaxResearchers int
}

func (d RuleBasedTodoDispatcher) Dispatch(ctx context.Context, in TodoDispatchInput) ([]TodoResearchJob, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(in.Todo.ID) == "" {
		return nil, fmt.Errorf("todo id is required")
	}

	roles := DeriveTodoResearchRoles(in.Todo, in.Plan)
	limit := d.MaxResearchers
	if limit <= 0 {
		limit = len(roles)
	}
	if limit > len(roles) {
		limit = len(roles)
	}

	jobs := make([]TodoResearchJob, 0, limit)
	for _, role := range roles[:limit] {
		jobs = append(jobs, TodoResearchJob{
			TodoID: strings.TrimSpace(in.Todo.ID),
			RoleID: role.ID,
			Name:   role.Name,
			Focus:  role.Focus,
			Context: TodoResearchContext{
				Objective:            in.Plan.Objective,
				Todo:                 in.Todo,
				DependencyExecutions: in.DependencyExecutions,
			},
			Budget: in.Budget,
		})
	}
	return jobs, nil
}

func DeriveTodoResearchRoles(todo ResearchTodo, plan ResearchTodoPlan) []TodoResearchRole {
	if isSynthesisTodo(todo) {
		return []TodoResearchRole{
			{
				ID:    "synthesis_researcher",
				Name:  "Synthesis Researcher",
				Focus: "synthesize completed dependency findings, identify the strongest conclusion, and preserve uncertainty",
			},
			{
				ID:    "gap_checker",
				Name:  "Gap Checker",
				Focus: "check unresolved questions, missing evidence, weak assumptions, and limitations before the final todo answer",
			},
		}
	}

	roles := []TodoResearchRole{
		{
			ID:    "background_researcher",
			Name:  "Background Researcher",
			Focus: "definitions, context, timeline, prerequisites, and key concepts for this todo",
		},
		{
			ID:    "evidence_researcher",
			Name:  "Evidence Researcher",
			Focus: "authoritative evidence, examples, implementation details, and source-backed facts for this todo",
		},
	}
	if todoNeedsFreshness(todo, plan) {
		roles = append(roles, TodoResearchRole{
			ID:    "freshness_researcher",
			Name:  "Freshness Researcher",
			Focus: "current information, latest versions, recent changes, dates, and time-sensitive claims",
		})
	}
	roles = append(roles, TodoResearchRole{
		ID:    "counterpoint_researcher",
		Name:  "Counterpoint Researcher",
		Focus: "counterexamples, risks, limitations, conflicting evidence, and dissenting views for this todo",
	})
	return roles
}

func todoNeedsFreshness(todo ResearchTodo, plan ResearchTodoPlan) bool {
	text := normalizeLintText(strings.Join([]string{
		plan.Objective,
		todo.Title,
		todo.Question,
		strings.Join(todo.SearchQueries, " "),
	}, " "))
	for _, keyword := range []string{
		"2026",
		"current",
		"latest",
		"market",
		"modern",
		"new",
		"news",
		"recent",
		"today",
		"version",
	} {
		if strings.Contains(text, keyword) {
			return true
		}
	}
	return false
}

func todoToResearchStep(todo ResearchTodo) ResearchStep {
	title := strings.TrimSpace(todo.Title)
	if title == "" {
		title = strings.TrimSpace(todo.ID)
	}
	return ResearchStep{
		ID:              strings.TrimSpace(todo.ID),
		Title:           title,
		Question:        strings.TrimSpace(todo.Question),
		SearchQueries:   nonEmptyStrings(todo.SearchQueries),
		SuccessCriteria: nonEmptyStrings(todo.AcceptanceCriteria),
	}
}

func dependencyExecutionsAsSteps(executions []TodoExecution) []StepExecution {
	steps := make([]StepExecution, 0, len(executions))
	for _, execution := range executions {
		steps = append(steps, StepExecution{
			Step:              todoToResearchStep(execution.Todo),
			ResearcherResults: execution.ResearcherResults,
			Summary:           execution.Summary,
			Gaps:              execution.Gaps,
			Sources:           execution.Sources,
		})
	}
	return steps
}

func stepExecutionToTodoExecution(todo ResearchTodo, step StepExecution) TodoExecution {
	step = normalizeStepExecutionSources(step)
	findings := make([]Finding, 0)
	for _, result := range step.ResearcherResults {
		findings = append(findings, result.Findings...)
	}
	findings = enrichFindingsEvidence(findings, step.Documents)

	summary := strings.TrimSpace(step.Summary)
	if summary == "" {
		summary = strings.TrimSpace(todo.Title)
	}

	return TodoExecution{
		Todo:              todo,
		Status:            TodoDone,
		ResearcherResults: step.ResearcherResults,
		Summary:           summary,
		Findings:          findings,
		Gaps:              step.Gaps,
		Sources:           step.Sources,
		Documents:         step.Documents,
	}
}

func nonEmptyStrings(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			out = append(out, value)
		}
	}
	return out
}
