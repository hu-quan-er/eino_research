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

type fakeFinalSynthesizer struct {
	answer Answer
	err    error
	input  FinalSynthesisInput
	calls  int
}

func (s *fakeFinalSynthesizer) SynthesizeFinal(_ context.Context, in FinalSynthesisInput) (Answer, error) {
	s.calls++
	s.input = in
	if s.err != nil {
		return Answer{}, s.err
	}
	return s.answer, nil
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
	finalSynthesizer := &fakeFinalSynthesizer{
		answer: Answer{
			Summary:     "Use Eino when you need a composable agent workflow.",
			Markdown:    "# Final Answer\n\nUse Eino for this prototype.",
			KeyFindings: []string{"Eino supports composable workflows."},
		},
	}
	runner := newTestRunner(t, RunnerConfig{
		Model:            &staticToolCallingModel{content: `{}`},
		SearchProvider:   search.NewMockProvider(),
		MaxParallelTodos: 2,
		FinalSynthesizer: finalSynthesizer,
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
	if finalSynthesizer.calls != 1 {
		t.Fatalf("final synthesizer calls = %d, want 1", finalSynthesizer.calls)
	}
	if finalSynthesizer.input.Question != "Should we use Eino?" {
		t.Fatalf("final synthesizer question = %q", finalSynthesizer.input.Question)
	}
	if len(finalSynthesizer.input.SectionExecutions) != 2 {
		t.Fatalf("final synthesizer sections = %d, want 2", len(finalSynthesizer.input.SectionExecutions))
	}
	if result.Answer.Summary != "Use Eino when you need a composable agent workflow." {
		t.Fatalf("answer summary = %q, want final synthesizer answer", result.Answer.Summary)
	}
}

func TestRunnerRunUsesTodoPlanPipeline(t *testing.T) {
	plan := validTodoPlan()
	planJSON, err := json.Marshal(plan)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	runner := newTestRunner(t, RunnerConfig{
		Model:          &staticToolCallingModel{content: string(planJSON)},
		SearchProvider: search.NewMockProvider(),
		TodoExecutor: func(_ context.Context, in TodoExecutorInput) (TodoExecution, error) {
			return TodoExecution{
				Todo:    in.Todo,
				Status:  TodoDone,
				Summary: in.Todo.Title + " complete",
			}, nil
		},
	})

	result, err := runner.Run(context.Background(), "Should we use Eino?")
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Plan.Objective != plan.Objective {
		t.Fatalf("result plan objective = %q, want %q", result.Plan.Objective, plan.Objective)
	}
	if len(result.TodoExecutions) != len(plan.Todos) {
		t.Fatalf("todo executions = %d, want %d", len(result.TodoExecutions), len(plan.Todos))
	}
	if result.Answer.Summary != "Completed 3 todo(s)." {
		t.Fatalf("summary = %q, want completed todo summary", result.Answer.Summary)
	}
}

func TestAgentFinalSynthesizerParsesAnswerJSON(t *testing.T) {
	synthesizer := NewAgentFinalSynthesizer(&staticToolCallingModel{content: `{
		"markdown":"# Final\n\nUse Eino with citations [src_1].",
		"summary":"Use Eino with citations.",
		"key_findings":["Composable workflows are supported [src_1]."],
		"limitations":["Only mock evidence was used."]
	}`})

	answer, err := synthesizer.SynthesizeFinal(context.Background(), FinalSynthesisInput{
		Question:       "Should we use Eino?",
		Plan:           validTodoPlan(),
		TodoExecutions: []TodoExecution{{Todo: validTodoPlan().Todos[0], Status: TodoDone}},
		Sources: []search.Source{{
			ID:    "src_1",
			Title: "Eino docs",
			URL:   "https://example.com/eino",
		}},
	})
	if err != nil {
		t.Fatalf("SynthesizeFinal() error = %v", err)
	}
	if answer.Summary != "Use Eino with citations." {
		t.Fatalf("summary = %q, want parsed summary", answer.Summary)
	}
	if len(answer.KeyFindings) != 1 || !strings.Contains(answer.KeyFindings[0], "[src_1]") {
		t.Fatalf("key findings = %#v, want cited finding", answer.KeyFindings)
	}
}

func TestAgentFinalSynthesizerIgnoresWrongSchemaJSONFallbackMarkdown(t *testing.T) {
	synthesizer := NewAgentFinalSynthesizer(&staticToolCallingModel{content: `{"objective":"not an answer"}`})

	answer, err := synthesizer.SynthesizeFinal(context.Background(), FinalSynthesisInput{
		Question:       "Should we use Eino?",
		Plan:           validTodoPlan(),
		TodoExecutions: []TodoExecution{{Todo: validTodoPlan().Todos[0], Status: TodoDone}},
	})
	if err != nil {
		t.Fatalf("SynthesizeFinal() error = %v", err)
	}
	if strings.Contains(answer.Markdown, "not an answer") {
		t.Fatalf("markdown = %q, want deterministic fallback instead of wrong-schema JSON", answer.Markdown)
	}
	if answer.Summary != "Completed 1 todo(s)." {
		t.Fatalf("summary = %q, want fallback completed summary", answer.Summary)
	}
}

func TestRunnerExecuteFallsBackWhenFinalSynthesizerFails(t *testing.T) {
	plan := validTodoPlan()
	runner := newTestRunner(t, RunnerConfig{
		Model:            &staticToolCallingModel{content: `{}`},
		SearchProvider:   search.NewMockProvider(),
		FinalSynthesizer: &fakeFinalSynthesizer{err: errors.New("final model unavailable")},
		TodoExecutor: func(_ context.Context, in TodoExecutorInput) (TodoExecution, error) {
			return TodoExecution{
				Todo:    in.Todo,
				Status:  TodoDone,
				Summary: in.Todo.Title + " complete",
				Findings: []Finding{{
					Claim:     "Eino can compose agent workflows.",
					SourceIDs: []string{in.Todo.ID + "_src_1"},
				}},
				Gaps: []string{"needs more production examples"},
			}, nil
		},
	})

	result, err := runner.Execute(context.Background(), "Should we use Eino?", plan)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Answer.Summary != "Completed 3 todo(s)." {
		t.Fatalf("summary = %q, want fallback completed summary", result.Answer.Summary)
	}
	if len(result.Answer.KeyFindings) != 0 {
		t.Fatalf("key findings = %#v, want unsupported fallback finding removed", result.Answer.KeyFindings)
	}
	if len(result.Answer.Limitations) == 0 {
		t.Fatal("limitations empty, want final synthesis failure recorded")
	}
	foundFailure := false
	for _, limitation := range result.Answer.Limitations {
		if strings.Contains(limitation, "Final synthesis fallback was used") {
			foundFailure = true
			break
		}
	}
	if !foundFailure {
		t.Fatalf("limitations = %#v, want final synthesis fallback reason", result.Answer.Limitations)
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

func TestMetadataTraceAndBudgetSerialization(t *testing.T) {
	md := Metadata{
		Model:          "gpt-4.1",
		SearchProvider: "mock",
	}
	data, err := json.Marshal(md)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(data), `"trace"`) {
		t.Errorf("trace must be omitempty when empty, got %s", data)
	}
	if strings.Contains(string(data), `"budget"`) {
		t.Errorf("budget must be omitempty when nil, got %s", data)
	}

	md.Trace = []Event{{Kind: EventPlanStarted}}
	md.Budget = &BudgetReport{ModelCalls: 3}
	data, err = json.Marshal(md)
	if err != nil {
		t.Fatalf("marshal with values: %v", err)
	}
	if !strings.Contains(string(data), `"trace"`) {
		t.Errorf("trace should be present when set, got %s", data)
	}
	if !strings.Contains(string(data), `"budget"`) {
		t.Errorf("budget should be present when set, got %s", data)
	}
}
