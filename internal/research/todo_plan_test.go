package research

import (
	"strings"
	"testing"
)

func TestResearchTodoPlanValidateAcceptsValidPlan(t *testing.T) {
	plan := validTodoPlan()

	if err := plan.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestResearchTodoPlanValidateRejectsMissingObjective(t *testing.T) {
	plan := validTodoPlan()
	plan.Objective = " "

	assertTodoPlanErrorContains(t, plan, "objective is required")
}

func TestResearchTodoPlanValidateRejectsDuplicateSectionIDs(t *testing.T) {
	plan := validTodoPlan()
	plan.Sections = append(plan.Sections, ResearchSection{
		ID:    "background",
		Title: "Duplicate Background",
	})

	assertTodoPlanErrorContains(t, plan, "section id \"background\" is duplicated")
}

func TestResearchTodoPlanValidateRejectsDuplicateTodoIDs(t *testing.T) {
	plan := validTodoPlan()
	plan.Todos = append(plan.Todos, ResearchTodo{
		ID:                 "todo_background",
		SectionID:          "evidence",
		Title:              "Duplicate todo",
		Question:           "What duplicates this todo?",
		AcceptanceCriteria: []string{"The duplicate is detected."},
	})

	assertTodoPlanErrorContains(t, plan, "todo id \"todo_background\" is duplicated")
}

func TestResearchTodoPlanValidateRejectsMissingSectionReference(t *testing.T) {
	plan := validTodoPlan()
	plan.Todos[0].SectionID = "missing"

	assertTodoPlanErrorContains(t, plan, "todo todo_background references unknown section_id \"missing\"")
}

func TestResearchTodoPlanValidateRejectsMissingAcceptanceCriteria(t *testing.T) {
	plan := validTodoPlan()
	plan.Todos[0].AcceptanceCriteria = []string{" "}

	assertTodoPlanErrorContains(t, plan, "todo todo_background acceptance_criteria is required")
}

func TestResearchTodoPlanValidateRejectsUnknownDependency(t *testing.T) {
	plan := validTodoPlan()
	plan.Todos[1].DependsOn = []string{"missing_todo"}

	assertTodoPlanErrorContains(t, plan, "todo todo_evidence depends on unknown todo \"missing_todo\"")
}

func TestResearchTodoPlanValidateRejectsCyclicDependencies(t *testing.T) {
	plan := validTodoPlan()
	plan.Todos[0].DependsOn = []string{"todo_synthesis"}
	plan.Todos[1].DependsOn = []string{"todo_background"}
	plan.Todos[2].DependsOn = []string{"todo_evidence"}

	assertTodoPlanErrorContains(t, plan, "dependency cycle")
}

func validTodoPlan() ResearchTodoPlan {
	return ResearchTodoPlan{
		Objective: "Assess whether Eino is suitable for building a research agent.",
		Sections: []ResearchSection{
			{
				ID:          "background",
				Title:       "Background",
				Description: "Define the core concepts.",
			},
			{
				ID:    "evidence",
				Title: "Evidence",
			},
		},
		Todos: []ResearchTodo{
			{
				ID:                 "todo_background",
				SectionID:          "background",
				Title:              "Clarify Eino agent concepts",
				Question:           "What Eino agent concepts matter for research agents?",
				SearchQueries:      []string{"Eino ADK planexecute"},
				AcceptanceCriteria: []string{"Names the relevant Eino ADK components."},
			},
			{
				ID:                 "todo_evidence",
				SectionID:          "evidence",
				Title:              "Collect implementation evidence",
				Question:           "What evidence shows Eino can support the workflow?",
				SearchQueries:      []string{"Eino ADK ParallelAgent tools"},
				AcceptanceCriteria: []string{"Includes evidence from implementation or docs."},
				DependsOn:          []string{"todo_background"},
			},
			{
				ID:                 "todo_synthesis",
				SectionID:          "evidence",
				Title:              "Synthesize conclusion",
				Question:           "What conclusion follows from the collected evidence?",
				AcceptanceCriteria: []string{"Produces a clear recommendation."},
				DependsOn:          []string{"todo_evidence"},
			},
		},
	}
}

func assertTodoPlanErrorContains(t *testing.T, plan ResearchTodoPlan, want string) {
	t.Helper()

	err := plan.Validate()
	if err == nil {
		t.Fatalf("Validate() error = nil, want error containing %q", want)
	}
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("Validate() error = %q, want substring %q", err.Error(), want)
	}
}
