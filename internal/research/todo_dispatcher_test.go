package research

import (
	"context"
	"testing"

	"github.com/hu-quan-er/eino_research/internal/search"
)

func TestRuleBasedTodoDispatcherCreatesEvidenceFanout(t *testing.T) {
	plan := validTodoPlan()
	dispatcher := RuleBasedTodoDispatcher{MaxResearchers: 3}

	jobs, err := dispatcher.Dispatch(context.Background(), TodoDispatchInput{
		Plan: plan,
		Todo: plan.Todos[1],
	})
	if err != nil {
		t.Fatalf("Dispatch() error = %v", err)
	}

	want := []string{"background_researcher", "evidence_researcher", "counterpoint_researcher"}
	assertJobRoleIDs(t, jobs, want)
}

func TestRuleBasedTodoDispatcherPrioritizesFreshnessWhenCapped(t *testing.T) {
	plan := validTodoPlan()
	todo := plan.Todos[1]
	todo.Title = "Check latest implementation evidence"
	todo.Question = "What current evidence shows the latest Eino workflow is supported?"
	dispatcher := RuleBasedTodoDispatcher{MaxResearchers: 3}

	jobs, err := dispatcher.Dispatch(context.Background(), TodoDispatchInput{
		Plan: plan,
		Todo: todo,
	})
	if err != nil {
		t.Fatalf("Dispatch() error = %v", err)
	}

	want := []string{"background_researcher", "evidence_researcher", "freshness_researcher"}
	assertJobRoleIDs(t, jobs, want)
}

func TestRuleBasedTodoDispatcherCreatesSynthesisFanout(t *testing.T) {
	plan := validTodoPlan()
	dispatcher := RuleBasedTodoDispatcher{MaxResearchers: 3}

	jobs, err := dispatcher.Dispatch(context.Background(), TodoDispatchInput{
		Plan: plan,
		Todo: plan.Todos[2],
	})
	if err != nil {
		t.Fatalf("Dispatch() error = %v", err)
	}

	want := []string{"synthesis_researcher", "gap_checker"}
	assertJobRoleIDs(t, jobs, want)
}

func TestStepExecutionToTodoExecutionPreservesFanoutResults(t *testing.T) {
	todo := validTodoPlan().Todos[1]
	step := StepExecution{
		Step:    todoToResearchStep(todo),
		Summary: "combined todo research",
		ResearcherResults: []ResearcherResult{{
			Role: "evidence_researcher",
			Findings: []Finding{{
				Claim:     "evidence claim",
				SourceIDs: []string{"src_1"},
			}},
		}},
		Sources: []search.Source{{
			ID:    "src_1",
			Title: "Evidence",
			URL:   "https://example.com/evidence",
		}},
	}

	execution := stepExecutionToTodoExecution(todo, step)

	if execution.Todo.ID != todo.ID {
		t.Fatalf("todo id = %q, want %q", execution.Todo.ID, todo.ID)
	}
	if execution.Status != TodoDone {
		t.Fatalf("status = %q, want %q", execution.Status, TodoDone)
	}
	if execution.Summary != "combined todo research" {
		t.Fatalf("summary = %q, want combined todo research", execution.Summary)
	}
	if len(execution.ResearcherResults) != 1 {
		t.Fatalf("researcher results = %d, want 1", len(execution.ResearcherResults))
	}
	if len(execution.Findings) != 1 {
		t.Fatalf("findings = %d, want 1", len(execution.Findings))
	}
	if len(execution.Sources) != 1 {
		t.Fatalf("sources = %d, want 1", len(execution.Sources))
	}
}

func assertJobRoleIDs(t *testing.T, jobs []TodoResearchJob, want []string) {
	t.Helper()
	if len(jobs) != len(want) {
		t.Fatalf("jobs = %d, want %d: %#v", len(jobs), len(want), jobs)
	}
	for i := range want {
		if jobs[i].RoleID != want[i] {
			t.Fatalf("jobs[%d].RoleID = %q, want %q", i, jobs[i].RoleID, want[i])
		}
	}
}
