package research

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/adk/prebuilt/planexecute"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
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

type fakeToolCallingModel struct{}

func (fakeToolCallingModel) Generate(context.Context, []*schema.Message, ...model.Option) (*schema.Message, error) {
	return schema.AssistantMessage(`{}`, nil), nil
}

func (fakeToolCallingModel) Stream(context.Context, []*schema.Message, ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	return schema.StreamReaderFromArray([]*schema.Message{schema.AssistantMessage(`{}`, nil)}), nil
}

func (m fakeToolCallingModel) WithTools([]*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	return m, nil
}

type fakePlan struct{}

func (fakePlan) FirstStep() string {
	return "plain step"
}

func (fakePlan) MarshalJSON() ([]byte, error) {
	return []byte(`{"steps":["plain step"]}`), nil
}

func (fakePlan) UnmarshalJSON([]byte) error {
	return nil
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
		fakeResearcher{role: "background_researcher", err: errors.New("background timeout")},
		fakeResearcher{role: "evidence_researcher", err: errors.New("evidence quota exceeded")},
		fakeResearcher{role: "counterpoint_researcher", err: errors.New("counterpoint context canceled")},
	}, fakeSynthesizer{})

	_, err := exec.ExecuteStep(context.Background(), StepExecutionInput{
		Question: "Should we use Eino?",
		Step:     ResearchStep{ID: "step_1", Question: "What is Eino?"},
	})
	assertErrorContains(t, err,
		"all researchers failed",
		"background_researcher",
		"background timeout",
		"evidence_researcher",
		"evidence quota exceeded",
		"counterpoint_researcher",
		"counterpoint context canceled",
	)
}

func TestParallelStepExecutorFailsForNilExecutor(t *testing.T) {
	var exec *ParallelStepExecutor

	_, err := exec.ExecuteStep(context.Background(), StepExecutionInput{
		Question: "Should we use Eino?",
		Step:     ResearchStep{ID: "step_1", Question: "What is Eino?"},
	})
	assertErrorContains(t, err, "executor", "nil")
}

func TestParallelStepExecutorFailsForNilSynthesizer(t *testing.T) {
	exec := NewParallelStepExecutor([]Researcher{
		fakeResearcher{role: "background_researcher"},
	}, nil)

	_, err := exec.ExecuteStep(context.Background(), StepExecutionInput{
		Question: "Should we use Eino?",
		Step:     ResearchStep{ID: "step_1", Question: "What is Eino?"},
	})
	assertErrorContains(t, err, "synthesizer", "nil")
}

func TestParallelStepExecutorFailsForNilResearcher(t *testing.T) {
	exec := NewParallelStepExecutor([]Researcher{
		fakeResearcher{role: "background_researcher"},
		nil,
	}, fakeSynthesizer{})

	_, err := exec.ExecuteStep(context.Background(), StepExecutionInput{
		Question: "Should we use Eino?",
		Step:     ResearchStep{ID: "step_1", Question: "What is Eino?"},
	})
	assertErrorContains(t, err, "researcher 1", "nil")
}

func TestBuildDefaultResearcherRoles(t *testing.T) {
	roles := DefaultResearcherRoles()
	want := []string{"background_researcher", "evidence_researcher", "counterpoint_researcher"}
	if len(roles) != len(want) {
		t.Fatalf("roles length = %d, want %d", len(roles), len(want))
	}
	for i := range want {
		if roles[i] != want[i] {
			t.Fatalf("roles[%d] = %q, want %q", i, roles[i], want[i])
		}
	}
}

func TestEinoParallelExecutorRequiresResearchPlanSessionValue(t *testing.T) {
	exec := NewEinoParallelExecutor(RunnerConfig{
		Model:          fakeToolCallingModel{},
		SearchProvider: search.NewMockProvider(),
	})
	runner := adk.NewRunner(context.Background(), adk.RunnerConfig{Agent: exec})

	iter := runner.Query(context.Background(), "Should we use Eino?", adk.WithSessionValues(map[string]any{
		planexecute.PlanSessionKey:      fakePlan{},
		planexecute.UserInputSessionKey: []adk.Message{schema.UserMessage("Should we use Eino?")},
	}))

	var err error
	for {
		event, ok := iter.Next()
		if !ok {
			break
		}
		if event != nil && event.Err != nil {
			err = event.Err
			break
		}
	}
	assertErrorContains(t, err, "plan session value has type", "want *ResearchPlan")
}

func assertErrorContains(t *testing.T, err error, substrings ...string) {
	t.Helper()
	if err == nil {
		t.Fatal("error = nil, want non-nil error")
	}
	for _, substring := range substrings {
		if !strings.Contains(err.Error(), substring) {
			t.Fatalf("error = %q, want substring %q", err.Error(), substring)
		}
	}
}
