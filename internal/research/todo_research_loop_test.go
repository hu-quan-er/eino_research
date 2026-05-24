package research

import (
	"context"
	"testing"

	"github.com/hu-quan-er/eino_research/internal/search"
)

func TestRunTodoResearchLoopRepeatsWhenGapsRemain(t *testing.T) {
	plan := validTodoPlan()
	todo := plan.Todos[1]
	dependency := TodoExecution{
		Todo:    plan.Todos[0],
		Status:  TodoDone,
		Summary: "background complete",
	}
	calls := 0

	out, err := runTodoResearchLoop(context.Background(), TodoResearchLoopInput{
		Plan:                 plan,
		Todo:                 todo,
		DependencyExecutions: []TodoExecution{dependency},
		MaxAttempts:          2,
		ExecuteStep: func(_ context.Context, in StepExecutionInput) (StepExecution, error) {
			calls++
			if calls == 1 {
				if len(in.ExecutedSteps) != 1 {
					t.Fatalf("first iteration executed steps = %d, want dependency context only", len(in.ExecutedSteps))
				}
				return StepExecution{
					Step:    in.Step,
					Summary: "partial",
					Gaps:    []string{"missing source-backed evidence"},
				}, nil
			}
			if len(in.ExecutedSteps) != 2 {
				t.Fatalf("second iteration executed steps = %d, want dependency plus first attempt", len(in.ExecutedSteps))
			}
			return StepExecution{
				Step:    in.Step,
				Summary: "complete",
				ResearcherResults: []ResearcherResult{{
					Role: "evidence_researcher",
					Findings: []Finding{{
						Claim:     "source-backed claim",
						SourceIDs: []string{"src_1"},
					}},
				}},
				Sources: []search.Source{{
					ID:    "src_1",
					Title: "Evidence",
					URL:   "https://example.com/evidence",
				}},
			}, nil
		},
	})
	if err != nil {
		t.Fatalf("runTodoResearchLoop returned error: %v", err)
	}
	if calls != 2 {
		t.Fatalf("calls = %d, want 2", calls)
	}
	if out.Summary != "complete" {
		t.Fatalf("summary = %q, want complete", out.Summary)
	}
	if len(out.Gaps) != 0 {
		t.Fatalf("gaps = %#v, want none", out.Gaps)
	}
}

func TestRunTodoResearchLoopStopsAtMaxAttemptsWithGaps(t *testing.T) {
	plan := validTodoPlan()
	todo := plan.Todos[1]
	calls := 0

	out, err := runTodoResearchLoop(context.Background(), TodoResearchLoopInput{
		Plan:        plan,
		Todo:        todo,
		MaxAttempts: 2,
		ExecuteStep: func(_ context.Context, in StepExecutionInput) (StepExecution, error) {
			calls++
			return StepExecution{
				Step:    in.Step,
				Summary: "still partial",
				Gaps:    []string{"still missing source-backed evidence"},
			}, nil
		},
	})
	if err != nil {
		t.Fatalf("runTodoResearchLoop returned error: %v", err)
	}
	if calls != 2 {
		t.Fatalf("calls = %d, want 2", calls)
	}
	assertContains(t, out.Gaps, "still missing source-backed evidence")
	assertContains(t, out.Gaps, "evidence-oriented todo returned no sources")
}

func TestRunTodoResearchLoopStopsWhenFirstAttemptSatisfiesTodo(t *testing.T) {
	plan := validTodoPlan()
	todo := plan.Todos[1]
	calls := 0

	_, err := runTodoResearchLoop(context.Background(), TodoResearchLoopInput{
		Plan:        plan,
		Todo:        todo,
		MaxAttempts: 3,
		ExecuteStep: func(_ context.Context, in StepExecutionInput) (StepExecution, error) {
			calls++
			return StepExecution{
				Step:    in.Step,
				Summary: "complete",
				ResearcherResults: []ResearcherResult{{
					Role: "evidence_researcher",
					Findings: []Finding{{
						Claim:     "source-backed claim",
						SourceIDs: []string{"src_1"},
					}},
				}},
				Sources: []search.Source{{
					ID:    "src_1",
					Title: "Evidence",
					URL:   "https://example.com/evidence",
				}},
			}, nil
		},
	})
	if err != nil {
		t.Fatalf("runTodoResearchLoop returned error: %v", err)
	}
	if calls != 1 {
		t.Fatalf("calls = %d, want 1", calls)
	}
}

func assertContains(t *testing.T, values []string, want string) {
	t.Helper()
	for _, value := range values {
		if value == want {
			return
		}
	}
	t.Fatalf("values = %#v, want %q", values, want)
}
