package research

import (
	"context"
	"errors"
	"testing"

	"github.com/hu-quan-er/eino_research/internal/search"
)

type fakeResearcher struct {
	role string
	err  error
}

func (r fakeResearcher) Research(ctx context.Context, in ResearcherInput) (ResearcherResult, error) {
	if r.err != nil {
		return ResearcherResult{}, r.err
	}
	return ResearcherResult{
		Role:     r.role,
		Focus:    in.Focus,
		Queries:  []string{in.Step.Question},
		Findings: []Finding{{Claim: r.role + " finding", Rationale: "test rationale"}},
		Sources:  []search.Source{{ID: r.role, Title: r.role, URL: "https://example.com/" + r.role}},
	}, nil
}

type fakeSynthesizer struct{}

func (s fakeSynthesizer) Synthesize(ctx context.Context, in SynthesisInput) (StepExecution, error) {
	return StepExecution{
		Step:              in.Step,
		ResearcherResults: in.Results,
		Summary:           "combined",
		Sources:           []search.Source{{ID: "src_1", Title: "combined", URL: "https://example.com/combined"}},
	}, nil
}

func TestParallelStepExecutorRunsAllResearchers(t *testing.T) {
	exec := NewParallelStepExecutor([]Researcher{
		fakeResearcher{role: "background_researcher"},
		fakeResearcher{role: "evidence_researcher"},
		fakeResearcher{role: "counterpoint_researcher"},
	}, fakeSynthesizer{})

	out, err := exec.ExecuteStep(context.Background(), StepExecutionInput{
		Question: "Should we use Eino?",
		Step:     ResearchStep{ID: "step_1", Question: "What is Eino?"},
	})
	if err != nil {
		t.Fatalf("ExecuteStep returned error: %v", err)
	}
	if out.Summary != "combined" {
		t.Fatalf("Summary = %q, want combined", out.Summary)
	}
	if len(out.ResearcherResults) != 3 {
		t.Fatalf("researcher results = %d, want 3", len(out.ResearcherResults))
	}
}

func TestParallelStepExecutorContinuesWhenOneResearcherFails(t *testing.T) {
	exec := NewParallelStepExecutor([]Researcher{
		fakeResearcher{role: "background_researcher"},
		fakeResearcher{role: "evidence_researcher", err: errors.New("model failed")},
		fakeResearcher{role: "counterpoint_researcher"},
	}, fakeSynthesizer{})

	out, err := exec.ExecuteStep(context.Background(), StepExecutionInput{
		Question: "Should we use Eino?",
		Step:     ResearchStep{ID: "step_1", Question: "What is Eino?"},
	})
	if err != nil {
		t.Fatalf("ExecuteStep returned error: %v", err)
	}
	if len(out.ResearcherResults) != 3 {
		t.Fatalf("researcher results = %d, want 3 including failed result", len(out.ResearcherResults))
	}
	if len(out.ResearcherResults[1].Errors) == 0 {
		t.Fatalf("failed researcher errors = 0, want error recorded")
	}
}

func TestParallelStepExecutorFailsWhenAllResearchersFail(t *testing.T) {
	exec := NewParallelStepExecutor([]Researcher{
		fakeResearcher{role: "background_researcher", err: errors.New("failed")},
		fakeResearcher{role: "evidence_researcher", err: errors.New("failed")},
		fakeResearcher{role: "counterpoint_researcher", err: errors.New("failed")},
	}, fakeSynthesizer{})

	_, err := exec.ExecuteStep(context.Background(), StepExecutionInput{
		Question: "Should we use Eino?",
		Step:     ResearchStep{ID: "step_1", Question: "What is Eino?"},
	})
	if err == nil {
		t.Fatal("ExecuteStep returned nil error, want all researchers failed")
	}
}
