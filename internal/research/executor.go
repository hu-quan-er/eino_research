package research

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/adk/prebuilt/planexecute"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
)

type Researcher interface {
	Research(ctx context.Context, in ResearcherInput) (ResearcherResult, error)
}

type Synthesizer interface {
	Synthesize(ctx context.Context, in SynthesisInput) (StepExecution, error)
}

type ResearcherInput struct {
	Question      string
	Step          ResearchStep
	ExecutedSteps []StepExecution
	Focus         string
}

type SynthesisInput struct {
	Question      string
	Step          ResearchStep
	ExecutedSteps []StepExecution
	Results       []ResearcherResult
}

type StepExecutionInput struct {
	Question      string
	Step          ResearchStep
	ExecutedSteps []StepExecution
}

type ParallelStepExecutor struct {
	researchers []Researcher
	synthesizer Synthesizer
}

func NewParallelStepExecutor(researchers []Researcher, synthesizer Synthesizer) *ParallelStepExecutor {
	return &ParallelStepExecutor{researchers: researchers, synthesizer: synthesizer}
}

func (e *ParallelStepExecutor) ExecuteStep(ctx context.Context, in StepExecutionInput) (StepExecution, error) {
	if e == nil {
		return StepExecution{}, fmt.Errorf("parallel step executor is nil")
	}
	if isNilDependency(e.synthesizer) {
		return StepExecution{}, fmt.Errorf("synthesizer is nil")
	}
	for i, researcher := range e.researchers {
		if isNilDependency(researcher) {
			return StepExecution{}, fmt.Errorf("researcher %d (%s) is nil", i, roleForIndex(i))
		}
	}

	results := make([]ResearcherResult, len(e.researchers))
	researcherErrors := make([]error, len(e.researchers))
	var wg sync.WaitGroup

	for i, researcher := range e.researchers {
		wg.Add(1)
		go func(idx int, r Researcher) {
			defer wg.Done()
			focus := focusForIndex(idx)
			result, err := r.Research(ctx, ResearcherInput{
				Question:      in.Question,
				Step:          in.Step,
				ExecutedSteps: in.ExecutedSteps,
				Focus:         focus,
			})
			if err != nil {
				researcherErrors[idx] = err
				results[idx] = ResearcherResult{
					Role:   roleForIndex(idx),
					Focus:  focus,
					Errors: []string{err.Error()},
				}
				return
			}
			results[idx] = result
		}(i, researcher)
	}

	wg.Wait()
	successes := 0
	for _, result := range results {
		if len(result.Errors) == 0 {
			successes++
		}
	}
	if successes == 0 {
		return StepExecution{}, allResearchersFailedError(results, researcherErrors)
	}

	return e.synthesizer.Synthesize(ctx, SynthesisInput{
		Question:      in.Question,
		Step:          in.Step,
		ExecutedSteps: in.ExecutedSteps,
		Results:       results,
	})
}

func allResearchersFailedError(results []ResearcherResult, researcherErrors []error) error {
	errs := []error{errors.New("all researchers failed")}
	for i, result := range results {
		role := result.Role
		if role == "" {
			role = roleForIndex(i)
		}
		if i < len(researcherErrors) && researcherErrors[i] != nil {
			errs = append(errs, fmt.Errorf("%s: %w", role, researcherErrors[i]))
			continue
		}
		for _, msg := range result.Errors {
			errs = append(errs, fmt.Errorf("%s: %s", role, msg))
		}
	}
	return errors.Join(errs...)
}

func isNilDependency(v any) bool {
	if v == nil {
		return true
	}
	value := reflect.ValueOf(v)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Ptr, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}

func roleForIndex(i int) string {
	switch i {
	case 0:
		return "background_researcher"
	case 1:
		return "evidence_researcher"
	default:
		return "counterpoint_researcher"
	}
}

func focusForIndex(i int) string {
	switch i {
	case 0:
		return "background, definitions, context, timeline, and key concepts"
	case 1:
		return "data, facts, examples, authoritative evidence, and mainstream positions"
	default:
		return "counterexamples, controversies, limitations, failures, and dissenting views"
	}
}

func DefaultResearcherRoles() []string {
	return []string{
		"background_researcher",
		"evidence_researcher",
		"counterpoint_researcher",
	}
}

const ResearchExecutedStepsSessionKey = "research_executed_steps"

type EinoParallelExecutor struct {
	cfg RunnerConfig
}

func NewEinoParallelExecutor(cfg RunnerConfig) *EinoParallelExecutor {
	return &EinoParallelExecutor{cfg: cfg}
}

func (e *EinoParallelExecutor) Name(_ context.Context) string {
	return "executor"
}

func (e *EinoParallelExecutor) Description(_ context.Context) string {
	return "parallel research executor"
}

func (e *EinoParallelExecutor) Run(ctx context.Context, _ *adk.AgentInput, _ ...adk.AgentRunOption) *adk.AsyncIterator[*adk.AgentEvent] {
	iterator, generator := adk.NewAsyncIteratorPair[*adk.AgentEvent]()

	go func() {
		defer generator.Close()
		defer func() {
			if panicErr := recover(); panicErr != nil {
				generator.Send(&adk.AgentEvent{Err: fmt.Errorf("research executor panic: %v", panicErr)})
			}
		}()

		step, err := e.run(ctx)
		if err != nil {
			generator.Send(&adk.AgentEvent{Err: err})
			return
		}

		b, err := json.Marshal(step)
		if err != nil {
			generator.Send(&adk.AgentEvent{Err: fmt.Errorf("marshal step execution: %w", err)})
			return
		}

		content := string(b)
		adk.AddSessionValue(ctx, planexecute.ExecutedStepSessionKey, content)
		appendResearchStep(ctx, step)
		generator.Send(adk.EventFromMessage(schema.AssistantMessage(content, nil), nil, schema.Assistant, ""))
	}()

	return iterator
}

func (e *EinoParallelExecutor) run(ctx context.Context) (StepExecution, error) {
	if e == nil {
		return StepExecution{}, fmt.Errorf("eino parallel executor is nil")
	}
	if isNilDependency(e.cfg.Model) {
		return StepExecution{}, fmt.Errorf("model is nil")
	}
	if isNilDependency(e.cfg.SearchProvider) {
		return StepExecution{}, fmt.Errorf("search provider is nil")
	}

	rawPlan, ok := adk.GetSessionValue(ctx, planexecute.PlanSessionKey)
	if !ok {
		return StepExecution{}, fmt.Errorf("plan not found in session")
	}
	plan, ok := rawPlan.(*ResearchPlan)
	if !ok {
		return StepExecution{}, fmt.Errorf("plan session value has type %T, want *ResearchPlan", rawPlan)
	}
	if err := plan.Validate(); err != nil {
		return StepExecution{}, fmt.Errorf("invalid research plan: %w", err)
	}

	step, err := decodeResearchStep(plan.FirstStep())
	if err != nil {
		return StepExecution{}, err
	}

	var question string
	if rawUserInput, ok := adk.GetSessionValue(ctx, planexecute.UserInputSessionKey); ok {
		question = formatUserInput(rawUserInput)
	}
	if strings.TrimSpace(question) == "" {
		question = step.Question
	}

	searchTool, err := NewWebSearchTool(e.cfg.SearchProvider, SearchLimits{
		MaxSearchesPerStep: e.cfg.MaxSearchesPerStep,
		ResultsPerSearch:   e.cfg.ResultsPerSearch,
		SourceIDPrefix:     sourceIDPrefix(step.ID),
	})
	if err != nil {
		return StepExecution{}, fmt.Errorf("new web search tool: %w", err)
	}
	fetchTool, err := NewWebFetchTool(HTTPPageFetcher{}, FetchLimits{
		MaxFetchesPerStep: e.cfg.MaxSearchesPerStep,
		MaxContentChars:   4000,
	})
	if err != nil {
		return StepExecution{}, fmt.Errorf("new web fetch tool: %w", err)
	}

	researchers, err := buildResearchers(ctx, e.cfg, searchTool, fetchTool)
	if err != nil {
		return StepExecution{}, err
	}

	executor := NewParallelStepExecutor(researchers, NewAgentSynthesizer(e.cfg.Model))
	execution, err := executor.ExecuteStep(ctx, StepExecutionInput{
		Question:      question,
		Step:          step,
		ExecutedSteps: getResearchSteps(ctx),
	})
	if err != nil {
		return StepExecution{}, err
	}
	return normalizeStepExecutionSources(execution), nil
}

func sourceIDPrefix(stepID string) string {
	stepID = strings.TrimSpace(stepID)
	if stepID == "" {
		return "src"
	}
	return stepID + "_src"
}

func buildResearchers(ctx context.Context, cfg RunnerConfig, researchTools ...tool.BaseTool) ([]Researcher, error) {
	roles := DefaultResearcherRoles()
	researchers := make([]Researcher, 0, len(roles))
	for i, role := range roles {
		researcher, err := NewAgentResearcher(ctx, role, focusForIndex(i), cfg.Model, researchTools...)
		if err != nil {
			return nil, fmt.Errorf("new %s: %w", role, err)
		}
		researchers = append(researchers, researcher)
	}
	return researchers, nil
}

func appendResearchStep(ctx context.Context, step StepExecution) {
	steps := getResearchSteps(ctx)
	steps = append(steps, step)
	adk.AddSessionValue(ctx, ResearchExecutedStepsSessionKey, steps)
}

func getResearchSteps(ctx context.Context) []StepExecution {
	raw, ok := adk.GetSessionValue(ctx, ResearchExecutedStepsSessionKey)
	if !ok {
		return nil
	}
	steps, ok := raw.([]StepExecution)
	if !ok {
		return nil
	}
	out := make([]StepExecution, len(steps))
	copy(out, steps)
	return out
}

func formatUserInput(raw any) string {
	switch v := raw.(type) {
	case string:
		return v
	case []adk.Message:
		parts := make([]string, 0, len(v))
		for _, msg := range v {
			if msg == nil {
				continue
			}
			if content := strings.TrimSpace(msg.Content); content != "" {
				parts = append(parts, content)
			}
		}
		return strings.Join(parts, "\n")
	case adk.Message:
		if v == nil {
			return ""
		}
		return v.Content
	default:
		return fmt.Sprint(v)
	}
}

func decodeResearchStep(stepContent string) (ResearchStep, error) {
	stepContent = strings.TrimSpace(stepContent)
	if stepContent == "" {
		return ResearchStep{}, fmt.Errorf("plan first step is empty")
	}

	var step ResearchStep
	if err := json.Unmarshal([]byte(stepContent), &step); err == nil {
		if strings.TrimSpace(step.Question) == "" {
			step.Question = step.Title
		}
		return step, nil
	}

	return ResearchStep{
		ID:              "step_1",
		Title:           stepContent,
		Question:        stepContent,
		SuccessCriteria: []string{"Answer the step with cited evidence."},
	}, nil
}
