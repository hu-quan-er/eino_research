package research

import (
	"context"
	"testing"

	"github.com/hu-quan-er/eino_research/internal/search"
)

func TestRuleBasedTodoDispatcherCreatesImplementationFanout(t *testing.T) {
	plan := validTodoPlan()
	dispatcher := RuleBasedTodoDispatcher{MaxResearchers: 3}

	jobs, err := dispatcher.Dispatch(context.Background(), TodoDispatchInput{
		Plan: plan,
		Todo: plan.Todos[1],
	})
	if err != nil {
		t.Fatalf("Dispatch() error = %v", err)
	}

	want := []string{"background_researcher", "evidence_researcher", "implementation_researcher"}
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

func TestRuleBasedTodoDispatcherCreatesComparisonFanout(t *testing.T) {
	plan := validTodoPlan()
	todo := plan.Todos[1]
	todo.Title = "Compare agent framework options"
	todo.Question = "What tradeoffs exist between Eino and alternative agent frameworks?"
	todo.AcceptanceCriteria = []string{"Compares options and decision criteria."}
	dispatcher := RuleBasedTodoDispatcher{MaxResearchers: 3}

	jobs, err := dispatcher.Dispatch(context.Background(), TodoDispatchInput{
		Plan: plan,
		Todo: todo,
	})
	if err != nil {
		t.Fatalf("Dispatch() error = %v", err)
	}

	want := []string{"background_researcher", "evidence_researcher", "comparison_researcher"}
	assertJobRoleIDs(t, jobs, want)
}

func TestRuleBasedTodoDispatcherAddsGapCheckerForDependencyGaps(t *testing.T) {
	plan := validTodoPlan()
	dispatcher := RuleBasedTodoDispatcher{MaxResearchers: 4}

	jobs, err := dispatcher.Dispatch(context.Background(), TodoDispatchInput{
		Plan: plan,
		Todo: plan.Todos[1],
		DependencyExecutions: []TodoExecution{{
			Todo:   plan.Todos[0],
			Status: TodoDone,
			Gaps:   []string{"missing source-backed definition"},
		}},
	})
	if err != nil {
		t.Fatalf("Dispatch() error = %v", err)
	}

	want := []string{"evidence_researcher", "implementation_researcher", "gap_checker", "counterpoint_researcher"}
	assertJobRoleIDs(t, jobs, want)
}

func TestFinalizeTodoExecutionPreservesFanoutResults(t *testing.T) {
	todo := validTodoPlan().Todos[1]
	execution := TodoExecution{
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

	result := finalizeTodoExecution(todo, execution)

	if result.Todo.ID != todo.ID {
		t.Fatalf("todo id = %q, want %q", result.Todo.ID, todo.ID)
	}
	if result.Status != TodoDone {
		t.Fatalf("status = %q, want %q", result.Status, TodoDone)
	}
	if result.Summary != "combined todo research" {
		t.Fatalf("summary = %q, want combined todo research", result.Summary)
	}
	if len(result.ResearcherResults) != 1 {
		t.Fatalf("researcher results = %d, want 1", len(result.ResearcherResults))
	}
	if len(result.Findings) != 1 {
		t.Fatalf("findings = %d, want 1", len(result.Findings))
	}
	if len(result.Sources) != 1 {
		t.Fatalf("sources = %d, want 1", len(result.Sources))
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
