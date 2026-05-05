package research

import (
	"encoding/json"
	"testing"

	"github.com/hu-quan-er/eino_research/internal/search"
)

func TestApplyRunnerContentDoesNotTreatStepExecutionAsFinalAnswer(t *testing.T) {
	result := ResearchResult{}
	step := StepExecution{
		Step:    ResearchStep{ID: "step_1", Question: "What evidence exists?"},
		Summary: "intermediate summary",
		Sources: []search.Source{{
			ID:    "step_1_src_1",
			Title: "Evidence",
			URL:   "https://example.com/evidence",
		}},
	}
	b, err := json.Marshal(step)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	finalAnswer, sawFinalResponse := applyRunnerContent(&result, string(b))
	if sawFinalResponse {
		t.Fatal("sawFinalResponse = true, want false")
	}
	if finalAnswer != "" {
		t.Fatalf("finalAnswer = %q, want empty", finalAnswer)
	}
	if result.Answer.Markdown != "" {
		t.Fatalf("Answer.Markdown = %q, want empty", result.Answer.Markdown)
	}
	if len(result.ExecutedSteps) != 1 {
		t.Fatalf("executed steps = %d, want 1", len(result.ExecutedSteps))
	}
	if len(result.Sources) != 1 {
		t.Fatalf("sources = %d, want 1", len(result.Sources))
	}
	if result.Sources[0].ID != "step_1_src_1" {
		t.Fatalf("source ID = %q, want preserved step source ID", result.Sources[0].ID)
	}
}

func TestApplyRunnerContentPreservesDistinctStepSourceIDs(t *testing.T) {
	result := ResearchResult{}
	first := StepExecution{
		Step: ResearchStep{ID: "step_1", Question: "First step"},
		ResearcherResults: []ResearcherResult{{
			Role: "background_researcher",
			Findings: []Finding{{
				Claim:     "first claim",
				SourceIDs: []string{"step_1_src_1"},
			}},
			Sources: []search.Source{{
				ID:    "step_1_src_1",
				Title: "First",
				URL:   "https://example.com/first",
			}},
		}},
	}
	second := StepExecution{
		Step: ResearchStep{ID: "step_2", Question: "Second step"},
		ResearcherResults: []ResearcherResult{{
			Role: "evidence_researcher",
			Findings: []Finding{{
				Claim:     "second claim",
				SourceIDs: []string{"step_2_src_1"},
			}},
			Sources: []search.Source{{
				ID:    "step_2_src_1",
				Title: "Second",
				URL:   "https://example.com/second",
			}},
		}},
	}

	applyStepContent(t, &result, first)
	applyStepContent(t, &result, second)

	if len(result.Sources) != 2 {
		t.Fatalf("sources = %d, want 2", len(result.Sources))
	}
	if result.Sources[0].ID != "step_1_src_1" || result.Sources[1].ID != "step_2_src_1" {
		t.Fatalf("source IDs = %q, %q; want step-specific IDs", result.Sources[0].ID, result.Sources[1].ID)
	}
	if got := result.ExecutedSteps[1].ResearcherResults[0].Findings[0].SourceIDs[0]; got != "step_2_src_1" {
		t.Fatalf("second finding source ID = %q, want step_2_src_1", got)
	}
}

func TestFinalizeRunnerAnswerRequiresFinalResponse(t *testing.T) {
	result := ResearchResult{}

	err := finalizeRunnerAnswer(&result, "", false)
	assertErrorContains(t, err, "final response", "max iterations")
	if result.Error == nil {
		t.Fatal("result.Error = nil, want finalize error")
	}
	if result.Error.Stage != "finalize" {
		t.Fatalf("error stage = %q, want finalize", result.Error.Stage)
	}
	if result.Answer.Markdown != "" {
		t.Fatalf("Answer.Markdown = %q, want empty", result.Answer.Markdown)
	}
}

func TestFinalizeRunnerAnswerStoresFinalResponse(t *testing.T) {
	result := ResearchResult{}

	if err := finalizeRunnerAnswer(&result, "final answer", true); err != nil {
		t.Fatalf("finalizeRunnerAnswer returned error: %v", err)
	}
	if result.Answer.Markdown != "final answer" {
		t.Fatalf("Answer.Markdown = %q, want final answer", result.Answer.Markdown)
	}
	if result.Answer.Summary != "final answer" {
		t.Fatalf("Answer.Summary = %q, want final answer", result.Answer.Summary)
	}
}

func applyStepContent(t *testing.T, result *ResearchResult, step StepExecution) {
	t.Helper()
	b, err := json.Marshal(step)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	finalAnswer, sawFinalResponse := applyRunnerContent(result, string(b))
	if sawFinalResponse {
		t.Fatal("sawFinalResponse = true, want false")
	}
	if finalAnswer != "" {
		t.Fatalf("finalAnswer = %q, want empty", finalAnswer)
	}
}
