package research

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/adk/prebuilt/planexecute"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/hu-quan-er/eino_research/internal/search"
)

type RunnerConfig struct {
	Model              model.ToolCallingChatModel
	SearchProvider     search.Provider
	ModelName          string
	SearchProviderName string
	MaxIterations      int
	MaxSearchesPerStep int
	ResultsPerSearch   int
}

type Runner struct {
	cfg RunnerConfig
}

func NewRunner(cfg RunnerConfig) (*Runner, error) {
	if isNilDependency(cfg.Model) {
		return nil, fmt.Errorf("model is required")
	}
	if isNilDependency(cfg.SearchProvider) {
		return nil, fmt.Errorf("search provider is required")
	}
	if cfg.MaxIterations <= 0 {
		cfg.MaxIterations = 5
	}
	if cfg.MaxSearchesPerStep <= 0 {
		cfg.MaxSearchesPerStep = 6
	}
	if cfg.ResultsPerSearch <= 0 {
		cfg.ResultsPerSearch = 5
	}

	return &Runner{cfg: cfg}, nil
}

func (r *Runner) Run(ctx context.Context, question string) (result ResearchResult, err error) {
	started := time.Now()
	result = ResearchResult{
		Question: question,
		Metadata: Metadata{
			Model:          r.cfg.ModelName,
			SearchProvider: r.cfg.SearchProviderName,
			MaxIterations:  r.cfg.MaxIterations,
			StartedAt:      started.Format(time.RFC3339),
		},
	}
	defer func() {
		completed := time.Now()
		result.Metadata.CompletedAt = completed.Format(time.RFC3339)
		result.Metadata.DurationMS = completed.Sub(started).Milliseconds()
	}()

	if strings.TrimSpace(question) == "" {
		err := fmt.Errorf("question is required")
		result.Error = &RunError{Stage: "input", Message: err.Error()}
		return result, err
	}

	agent, err := r.buildAgent(ctx)
	if err != nil {
		result.Error = &RunError{Stage: "build", Message: err.Error()}
		return result, err
	}

	adkRunner := adk.NewRunner(ctx, adk.RunnerConfig{Agent: agent})
	iterator := adkRunner.Query(ctx, question)
	var finalAnswer string
	var sawFinalResponse bool
	for {
		event, ok := iterator.Next()
		if !ok {
			break
		}
		if event == nil {
			continue
		}
		if event.Err != nil {
			result.Error = &RunError{Stage: "run", Message: event.Err.Error()}
			return result, event.Err
		}
		content, err := assistantContent(event)
		if err != nil {
			result.Error = &RunError{Stage: "event", Message: err.Error()}
			return result, err
		}
		if content == "" {
			continue
		}

		if response, ok := applyRunnerContent(&result, content); ok {
			finalAnswer = response
			sawFinalResponse = true
		}
	}

	if err := finalizeRunnerAnswer(&result, finalAnswer, sawFinalResponse); err != nil {
		return result, err
	}
	return result, nil
}

func (r *Runner) buildAgent(ctx context.Context) (adk.ResumableAgent, error) {
	planTool := researchPlanToolInfo()
	newPlan := func(context.Context) planexecute.Plan {
		return &ResearchPlan{}
	}

	planner, err := planexecute.NewPlanner(ctx, &planexecute.PlannerConfig{
		ToolCallingChatModel: r.cfg.Model,
		ToolInfo:             planTool,
		GenInputFn:           genResearchPlannerInput,
		NewPlan:              newPlan,
	})
	if err != nil {
		return nil, fmt.Errorf("new planner: %w", err)
	}

	executor := NewEinoParallelExecutor(r.cfg)

	replanner, err := planexecute.NewReplanner(ctx, &planexecute.ReplannerConfig{
		ChatModel:  r.cfg.Model,
		PlanTool:   planTool,
		GenInputFn: genResearchReplannerInput,
		NewPlan:    newPlan,
	})
	if err != nil {
		return nil, fmt.Errorf("new replanner: %w", err)
	}

	agent, err := planexecute.New(ctx, &planexecute.Config{
		Planner:       planner,
		Executor:      executor,
		Replanner:     replanner,
		MaxIterations: r.cfg.MaxIterations,
	})
	if err != nil {
		return nil, fmt.Errorf("new plan execute agent: %w", err)
	}

	return agent, nil
}

func genResearchPlannerInput(_ context.Context, userInput []adk.Message) ([]adk.Message, error) {
	messages := []adk.Message{
		schema.SystemMessage(`Create a concise research plan. You must call the plan tool with {"steps":[ResearchStep,...]} where each step has id, title, question, search_queries, research_axes, and success_criteria. Do not use plain string steps.`),
	}
	messages = append(messages, userInput...)
	return messages, nil
}

func genResearchReplannerInput(_ context.Context, in *planexecute.ExecutionContext) ([]adk.Message, error) {
	planJSON, err := in.Plan.MarshalJSON()
	if err != nil {
		return nil, err
	}
	executedSteps, err := json.Marshal(in.ExecutedSteps)
	if err != nil {
		return nil, err
	}

	return []adk.Message{
		schema.SystemMessage(`Review progress. If the research objective is satisfied, call respond with the final answer. If more work is needed, call plan with only remaining ResearchStep objects; never emit plain string steps.`),
		schema.UserMessage(fmt.Sprintf(`Objective:
%s

Current plan JSON:
%s

Completed steps and results JSON:
%s`, formatUserInput(in.UserInput), string(planJSON), string(executedSteps))),
	}, nil
}

func researchPlanToolInfo() *schema.ToolInfo {
	stringArray := func(desc string, required bool) *schema.ParameterInfo {
		return &schema.ParameterInfo{
			Type:     schema.Array,
			ElemInfo: &schema.ParameterInfo{Type: schema.String},
			Desc:     desc,
			Required: required,
		}
	}
	step := &schema.ParameterInfo{
		Type: schema.Object,
		SubParams: map[string]*schema.ParameterInfo{
			"id": {
				Type:     schema.String,
				Desc:     "Stable step id such as step_1.",
				Required: true,
			},
			"title": {
				Type:     schema.String,
				Desc:     "Short step title.",
				Required: true,
			},
			"question": {
				Type:     schema.String,
				Desc:     "Specific research question for this step.",
				Required: true,
			},
			"search_queries":   stringArray("Initial web search queries for this step.", true),
			"research_axes":    stringArray("Angles that researchers should investigate.", false),
			"success_criteria": stringArray("Criteria for considering the step complete.", true),
		},
	}

	return &schema.ToolInfo{
		Name: "plan",
		Desc: "Create or update a research plan. The steps field must be an array of ResearchStep objects, not strings.",
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"steps": {
				Type:     schema.Array,
				ElemInfo: step,
				Desc:     "Ordered research steps to execute.",
				Required: true,
			},
		}),
	}
}

func assistantContent(event *adk.AgentEvent) (string, error) {
	if event.Output == nil || event.Output.MessageOutput == nil {
		return "", nil
	}
	output := event.Output.MessageOutput
	msg, err := output.GetMessage()
	if err != nil {
		return "", err
	}
	if msg == nil {
		return "", nil
	}
	if output.Role != "" && output.Role != schema.Assistant && msg.Role != schema.Assistant {
		return "", nil
	}
	return strings.TrimSpace(msg.Content), nil
}

func applyRunnerContent(result *ResearchResult, content string) (string, bool) {
	if plan, ok := parseResearchPlan(content); ok {
		result.Plan = plan
		return "", false
	}
	if step, ok := parseStepExecution(content); ok {
		result.ExecutedSteps = append(result.ExecutedSteps, step)
		result.Sources = search.Deduplicate(append(result.Sources, step.Sources...))
		return "", false
	}
	if response, ok := parsePlanExecuteResponse(content); ok {
		return response, true
	}
	return "", false
}

func finalizeRunnerAnswer(result *ResearchResult, finalAnswer string, sawFinalResponse bool) error {
	finalAnswer = strings.TrimSpace(finalAnswer)
	if !sawFinalResponse || finalAnswer == "" {
		err := fmt.Errorf("final response not received before plan-execute loop ended; max iterations may be exhausted")
		result.Error = &RunError{Stage: "finalize", Message: err.Error()}
		return err
	}

	result.Answer.Markdown = finalAnswer
	result.Answer.Summary = finalAnswer
	return nil
}

func parseResearchPlan(content string) (ResearchPlan, bool) {
	var plan ResearchPlan
	if err := json.Unmarshal([]byte(content), &plan); err != nil {
		return ResearchPlan{}, false
	}
	if len(plan.Steps) == 0 {
		return ResearchPlan{}, false
	}
	return plan, true
}

func parseStepExecution(content string) (StepExecution, bool) {
	var step StepExecution
	if err := json.Unmarshal([]byte(content), &step); err != nil {
		return StepExecution{}, false
	}
	if strings.TrimSpace(step.Step.Question) == "" && strings.TrimSpace(step.Step.Title) == "" {
		return StepExecution{}, false
	}
	return step, true
}

func parsePlanExecuteResponse(content string) (string, bool) {
	var response planexecute.Response
	if err := json.Unmarshal([]byte(content), &response); err != nil {
		return "", false
	}
	response.Response = strings.TrimSpace(response.Response)
	if response.Response == "" {
		return "", false
	}
	return response.Response, true
}
