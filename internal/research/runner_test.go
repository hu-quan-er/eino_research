package research

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/hu-quan-er/eino_research/internal/search"
)

type staticToolCallingModel struct {
	content         string
	contents        []string
	toolCalls       []schema.ToolCall
	supportTools    bool
	withToolsCalled bool
	boundTools      []*schema.ToolInfo
	lastOptions     *model.Options
	calls           int
}

func (m *staticToolCallingModel) Generate(_ context.Context, _ []*schema.Message, opts ...model.Option) (*schema.Message, error) {
	m.calls++
	m.lastOptions = model.GetCommonOptions(nil, opts...)
	if len(m.toolCalls) > 0 {
		return schema.AssistantMessage("", m.toolCalls), nil
	}
	if len(m.contents) > 0 {
		idx := m.calls - 1
		if idx >= len(m.contents) {
			idx = len(m.contents) - 1
		}
		return schema.AssistantMessage(m.contents[idx], nil), nil
	}
	return schema.AssistantMessage(m.content, nil), nil
}

func (m *staticToolCallingModel) Stream(_ context.Context, _ []*schema.Message, _ ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	return schema.StreamReaderFromArray([]*schema.Message{schema.AssistantMessage(m.content, nil)}), nil
}

func (m *staticToolCallingModel) WithTools(tools []*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	m.withToolsCalled = true
	m.boundTools = tools
	if !m.supportTools {
		return nil, errors.New("tools unsupported")
	}
	return m, nil
}

func TestRunnerPlanReturnsValidatedTodoPlan(t *testing.T) {
	planJSON, err := json.Marshal(validTodoPlan())
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	runner := newTestRunner(t, RunnerConfig{
		Model:          &staticToolCallingModel{content: string(planJSON)},
		SearchProvider: search.NewMockProvider(),
	})

	plan, err := runner.Plan(context.Background(), "Should we use Eino?")
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	if plan.Objective != validTodoPlan().Objective {
		t.Fatalf("Objective = %q, want %q", plan.Objective, validTodoPlan().Objective)
	}
	if len(plan.Todos) != 3 {
		t.Fatalf("todos = %d, want 3", len(plan.Todos))
	}
}

func TestRunnerPlanRepairsInvalidPlannerOutput(t *testing.T) {
	planJSON, err := json.Marshal(validTodoPlan())
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	plannerModel := &staticToolCallingModel{
		contents: []string{
			`not json`,
			string(planJSON),
		},
	}
	runner := newTestRunner(t, RunnerConfig{
		Model:          plannerModel,
		SearchProvider: search.NewMockProvider(),
	})

	plan, err := runner.Plan(context.Background(), "Should we use Eino?")
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	if plannerModel.calls != 2 {
		t.Fatalf("model calls = %d, want 2", plannerModel.calls)
	}
	if plan.Objective != validTodoPlan().Objective {
		t.Fatalf("Objective = %q, want %q", plan.Objective, validTodoPlan().Objective)
	}
}

func TestRunnerPlanRepairsLowQualityPlannerOutput(t *testing.T) {
	lowQualityPlan := validTodoPlan()
	lowQualityPlan.Todos[1].SearchQueries = nil
	lowQualityPlanJSON, err := json.Marshal(lowQualityPlan)
	if err != nil {
		t.Fatalf("Marshal lowQualityPlan: %v", err)
	}
	planJSON, err := json.Marshal(validTodoPlan())
	if err != nil {
		t.Fatalf("Marshal valid plan: %v", err)
	}
	plannerModel := &staticToolCallingModel{
		contents: []string{
			string(lowQualityPlanJSON),
			string(planJSON),
		},
	}
	runner := newTestRunner(t, RunnerConfig{
		Model:          plannerModel,
		SearchProvider: search.NewMockProvider(),
	})

	plan, err := runner.Plan(context.Background(), "Should we use Eino?")
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	if plannerModel.calls != 2 {
		t.Fatalf("model calls = %d, want 2", plannerModel.calls)
	}
	if len(plan.Todos[1].SearchQueries) == 0 {
		t.Fatal("Plan() returned low-quality plan without repaired search queries")
	}
}

func TestRunnerPlanReturnsLastRepairError(t *testing.T) {
	plannerModel := &staticToolCallingModel{
		contents: []string{
			`not json`,
			`{"objective":"still invalid"}`,
			`{"objective":"still invalid"}`,
		},
	}
	runner := newTestRunner(t, RunnerConfig{
		Model:          plannerModel,
		SearchProvider: search.NewMockProvider(),
	})

	_, err := runner.Plan(context.Background(), "Should we use Eino?")
	if err == nil {
		t.Fatal("Plan() error = nil, want repair failure")
	}
	if plannerModel.calls != 3 {
		t.Fatalf("model calls = %d, want 3", plannerModel.calls)
	}
	if !strings.Contains(err.Error(), "planner output invalid after 3 attempts") ||
		!strings.Contains(err.Error(), "sections is required") {
		t.Fatalf("error = %q, want attempts and validation reason", err.Error())
	}
}

func TestRunnerPlanPrefersToolCallingPlanner(t *testing.T) {
	planJSON, err := json.Marshal(validTodoPlan())
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	plannerModel := &staticToolCallingModel{
		supportTools: true,
		toolCalls: []schema.ToolCall{{
			ID:   "call_1",
			Type: "function",
			Function: schema.FunctionCall{
				Name:      researchTodoPlanToolName,
				Arguments: string(planJSON),
			},
		}},
	}
	runner := newTestRunner(t, RunnerConfig{
		Model:          plannerModel,
		SearchProvider: search.NewMockProvider(),
	})

	plan, err := runner.Plan(context.Background(), "Should we use Eino?")
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	if !plannerModel.withToolsCalled {
		t.Fatal("WithTools was not called, want tool-calling planner path")
	}
	if len(plannerModel.boundTools) != 1 || plannerModel.boundTools[0].Name != researchTodoPlanToolName {
		t.Fatalf("bound tools = %#v, want %s", plannerModel.boundTools, researchTodoPlanToolName)
	}
	if plannerModel.lastOptions == nil ||
		plannerModel.lastOptions.ToolChoice == nil ||
		*plannerModel.lastOptions.ToolChoice != schema.ToolChoiceForced {
		t.Fatalf("ToolChoice = %#v, want forced", plannerModel.lastOptions)
	}
	if len(plannerModel.lastOptions.AllowedToolNames) != 1 ||
		plannerModel.lastOptions.AllowedToolNames[0] != researchTodoPlanToolName {
		t.Fatalf("AllowedToolNames = %#v, want %s", plannerModel.lastOptions.AllowedToolNames, researchTodoPlanToolName)
	}
	if plannerModel.calls != 1 {
		t.Fatalf("model calls = %d, want 1", plannerModel.calls)
	}
	if plan.Objective != validTodoPlan().Objective {
		t.Fatalf("Objective = %q, want %q", plan.Objective, validTodoPlan().Objective)
	}
}

func TestRunnerPlanFallsBackToTextRepairWhenToolCallInvalid(t *testing.T) {
	planJSON, err := json.Marshal(validTodoPlan())
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	plannerModel := &staticToolCallingModel{
		supportTools: true,
		contents: []string{
			`tool response without a tool call`,
			`not json`,
			string(planJSON),
		},
	}
	runner := newTestRunner(t, RunnerConfig{
		Model:          plannerModel,
		SearchProvider: search.NewMockProvider(),
	})

	plan, err := runner.Plan(context.Background(), "Should we use Eino?")
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	if !plannerModel.withToolsCalled {
		t.Fatal("WithTools was not called, want tool-calling planner attempt")
	}
	if plannerModel.calls != 3 {
		t.Fatalf("model calls = %d, want one tool attempt plus two text attempts", plannerModel.calls)
	}
	if plan.Objective != validTodoPlan().Objective {
		t.Fatalf("Objective = %q, want %q", plan.Objective, validTodoPlan().Objective)
	}
}

func TestParseResearchTodoPlanReturnsJSONError(t *testing.T) {
	_, err := parseResearchTodoPlan(`not json`)
	if err == nil {
		t.Fatal("parseResearchTodoPlan returned nil error, want JSON error")
	}
	if !strings.Contains(err.Error(), "invalid ResearchTodoPlan JSON") {
		t.Fatalf("error = %q, want JSON context", err.Error())
	}
}

func TestParseResearchTodoPlanReturnsValidationError(t *testing.T) {
	_, err := parseResearchTodoPlan(`{"objective":"missing sections and todos"}`)
	if err == nil {
		t.Fatal("parseResearchTodoPlan returned nil error, want validation error")
	}
	if !strings.Contains(err.Error(), "invalid ResearchTodoPlan") ||
		!strings.Contains(err.Error(), "section") {
		t.Fatalf("error = %q, want validation context", err.Error())
	}
}

func TestRunnerExecuteAggregatesTodoResultsBySection(t *testing.T) {
	plan := validTodoPlan()
	runner := newTestRunner(t, RunnerConfig{
		Model:            &staticToolCallingModel{content: `{}`},
		SearchProvider:   search.NewMockProvider(),
		MaxParallelTodos: 2,
		TodoExecutor: func(_ context.Context, in TodoExecutorInput) (TodoExecution, error) {
			return TodoExecution{
				Todo:    in.Todo,
				Status:  TodoDone,
				Summary: in.Todo.Title + " complete",
				Sources: []search.Source{{
					ID:    in.Todo.ID + "_src_1",
					Title: in.Todo.Title,
					URL:   "https://example.com/" + in.Todo.ID,
				}},
			}, nil
		},
	})

	result, err := runner.Execute(context.Background(), "Should we use Eino?", plan)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Plan.Objective != plan.Objective {
		t.Fatalf("result plan objective = %q, want %q", result.Plan.Objective, plan.Objective)
	}
	if len(result.TodoExecutions) != 3 {
		t.Fatalf("todo executions = %d, want 3", len(result.TodoExecutions))
	}
	if len(result.SectionExecutions) != 2 {
		t.Fatalf("section executions = %d, want 2", len(result.SectionExecutions))
	}
	if len(result.SectionExecutions[0].Todos) != 1 {
		t.Fatalf("background todos = %d, want 1", len(result.SectionExecutions[0].Todos))
	}
	if len(result.SectionExecutions[1].Todos) != 2 {
		t.Fatalf("evidence todos = %d, want 2", len(result.SectionExecutions[1].Todos))
	}
	if len(result.Sources) != 3 {
		t.Fatalf("sources = %d, want 3", len(result.Sources))
	}
}

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

func newTestRunner(t *testing.T, cfg RunnerConfig) *Runner {
	t.Helper()
	if cfg.ModelName == "" {
		cfg.ModelName = "test-model"
	}
	if cfg.SearchProviderName == "" {
		cfg.SearchProviderName = "mock"
	}
	runner, err := NewRunner(cfg)
	if err != nil {
		t.Fatalf("NewRunner() error = %v", err)
	}
	return runner
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
