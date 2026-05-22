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
	MaxParallelTodos   int
	TodoExecutor       TodoExecutor
	TodoReplanner      TodoReplanner
}

type Runner struct {
	cfg RunnerConfig
}

const researchTodoPlanToolName = "create_research_todo_plan"

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
	if cfg.MaxParallelTodos <= 0 {
		cfg.MaxParallelTodos = 1
	}

	return &Runner{cfg: cfg}, nil
}

func (r *Runner) Plan(ctx context.Context, question string) (ResearchTodoPlan, error) {
	if strings.TrimSpace(question) == "" {
		return ResearchTodoPlan{}, fmt.Errorf("question is required")
	}

	if plan, err := r.planWithToolCall(ctx, question); err == nil {
		return plan, nil
	}
	if err := ctx.Err(); err != nil {
		return ResearchTodoPlan{}, err
	}

	return r.planWithTextRepair(ctx, question)
}

func (r *Runner) planWithToolCall(ctx context.Context, question string) (ResearchTodoPlan, error) {
	toolModel, err := r.cfg.Model.WithTools([]*schema.ToolInfo{researchTodoPlanToolInfo()})
	if err != nil {
		return ResearchTodoPlan{}, fmt.Errorf("bind planner tool: %w", err)
	}

	resp, err := toolModel.Generate(
		ctx,
		plannerMessages(question, "", nil),
		model.WithToolChoice(schema.ToolChoiceForced, researchTodoPlanToolName),
	)
	if err != nil {
		return ResearchTodoPlan{}, err
	}
	return parseResearchTodoPlanToolCall(resp)
}

func (r *Runner) planWithTextRepair(ctx context.Context, question string) (ResearchTodoPlan, error) {
	const maxPlannerAttempts = 3
	var lastOutput string
	var lastErr error

	for attempt := 1; attempt <= maxPlannerAttempts; attempt++ {
		messages := plannerMessages(question, lastOutput, lastErr)
		resp, err := r.cfg.Model.Generate(ctx, messages)
		if err != nil {
			return ResearchTodoPlan{}, err
		}
		if resp == nil {
			lastOutput = ""
			lastErr = fmt.Errorf("planner model response is nil")
			continue
		}

		lastOutput = resp.Content
		plan, err := parseResearchTodoPlan(resp.Content)
		if err == nil {
			return plan, nil
		}
		lastErr = err
	}

	return ResearchTodoPlan{}, fmt.Errorf("planner output invalid after %d attempts: %w", maxPlannerAttempts, lastErr)
}

func plannerMessages(question, previousOutput string, previousErr error) []*schema.Message {
	system := schema.SystemMessage(`Create a ResearchTodoPlan JSON object. Return only JSON with objective, sections, and todos. Use 3 to 6 sections, 4 to 10 todos, dependencies only when needed, search_queries for evidence-gathering todos, and acceptance_criteria for each todo.`)
	if previousErr == nil {
		return []*schema.Message{
			system,
			schema.UserMessage(question),
		}
	}

	return []*schema.Message{
		system,
		schema.UserMessage(fmt.Sprintf(`Original research question:
%s

Your previous planner output was invalid.

Validation or parsing error:
%s

Previous output:
%s

Return only a corrected ResearchTodoPlan JSON object. Do not include markdown, explanation, or extra text.`, question, previousErr.Error(), previousOutput)),
	}
}

func (r *Runner) Execute(ctx context.Context, question string, plan ResearchTodoPlan) (result ResearchResult, err error) {
	started := time.Now()
	result = ResearchResult{
		Question: question,
		Plan:     plan,
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
	if err := plan.Validate(); err != nil {
		result.Error = &RunError{Stage: "plan", Message: err.Error()}
		return result, err
	}

	executor := r.cfg.TodoExecutor
	if executor == nil {
		executor = r.executeTodo
	}
	scheduler, err := NewTodoScheduler(TodoSchedulerConfig{
		MaxParallel: r.cfg.MaxParallelTodos,
		Executor:    executor,
		Replanner:   r.cfg.TodoReplanner,
	})
	if err != nil {
		result.Error = &RunError{Stage: "scheduler", Message: err.Error()}
		return result, err
	}

	todoExecutions, err := scheduler.Run(ctx, plan)
	if err != nil {
		result.Error = &RunError{Stage: "execute", Message: err.Error()}
		return result, err
	}
	result.TodoExecutions = todoExecutions
	result.SectionExecutions = groupTodoExecutionsBySection(plan, todoExecutions)
	result.Sources = collectTodoExecutionSources(todoExecutions)
	result.Answer.Summary = fmt.Sprintf("Completed %d todo(s).", countTodoStatus(todoExecutions, TodoDone))
	result.Answer.Markdown = result.Answer.Summary

	return result, nil
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

func (r *Runner) executeTodo(ctx context.Context, in TodoExecutorInput) (TodoExecution, error) {
	sources := make([]search.Source, 0)
	maxSearches := r.cfg.MaxSearchesPerStep
	if maxSearches <= 0 {
		maxSearches = 6
	}
	resultsPerSearch := r.cfg.ResultsPerSearch
	if resultsPerSearch <= 0 {
		resultsPerSearch = 5
	}

	nextSource := 1
	for i, query := range in.Todo.SearchQueries {
		if i >= maxSearches {
			break
		}
		query = strings.TrimSpace(query)
		if query == "" {
			continue
		}
		results, err := r.cfg.SearchProvider.Search(ctx, query, resultsPerSearch)
		if err != nil {
			return TodoExecution{}, err
		}
		for _, source := range results {
			source.ID = fmt.Sprintf("%s_src_%d", in.Todo.ID, nextSource)
			nextSource++
			sources = append(sources, source)
		}
	}

	return TodoExecution{
		Todo:    in.Todo,
		Status:  TodoDone,
		Summary: in.Todo.Title,
		Sources: search.DeduplicateStable(sources),
	}, nil
}

func groupTodoExecutionsBySection(plan ResearchTodoPlan, executions []TodoExecution) []SectionExecution {
	byTodoID := make(map[string]TodoExecution, len(executions))
	for _, execution := range executions {
		byTodoID[execution.Todo.ID] = execution
	}

	sections := make([]SectionExecution, 0, len(plan.Sections))
	for _, section := range plan.Sections {
		sectionExecution := SectionExecution{
			Section: section,
		}
		for _, todo := range plan.Todos {
			if todo.SectionID != section.ID {
				continue
			}
			execution, ok := byTodoID[todo.ID]
			if !ok {
				continue
			}
			sectionExecution.Todos = append(sectionExecution.Todos, execution)
		}
		sectionExecution.Summary = summarizeSectionTodos(sectionExecution.Todos)
		sections = append(sections, sectionExecution)
	}
	return sections
}

func summarizeSectionTodos(executions []TodoExecution) string {
	summaries := make([]string, 0, len(executions))
	for _, execution := range executions {
		if summary := strings.TrimSpace(execution.Summary); summary != "" {
			summaries = append(summaries, summary)
		}
	}
	return strings.Join(summaries, "\n")
}

func collectTodoExecutionSources(executions []TodoExecution) []search.Source {
	sources := make([]search.Source, 0)
	for _, execution := range executions {
		sources = append(sources, execution.Sources...)
	}
	return search.DeduplicateStable(sources)
}

func countTodoStatus(executions []TodoExecution, status TodoStatus) int {
	count := 0
	for _, execution := range executions {
		if execution.Status == status {
			count++
		}
	}
	return count
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

func researchTodoPlanToolInfo() *schema.ToolInfo {
	stringArray := func(desc string, required bool) *schema.ParameterInfo {
		return &schema.ParameterInfo{
			Type:     schema.Array,
			ElemInfo: &schema.ParameterInfo{Type: schema.String},
			Desc:     desc,
			Required: required,
		}
	}
	section := &schema.ParameterInfo{
		Type: schema.Object,
		SubParams: map[string]*schema.ParameterInfo{
			"id": {
				Type:     schema.String,
				Desc:     "Stable section id such as background or evidence.",
				Required: true,
			},
			"title": {
				Type:     schema.String,
				Desc:     "Short section title.",
				Required: true,
			},
			"description": {
				Type: schema.String,
				Desc: "Optional section description.",
			},
		},
	}
	todo := &schema.ParameterInfo{
		Type: schema.Object,
		SubParams: map[string]*schema.ParameterInfo{
			"id": {
				Type:     schema.String,
				Desc:     "Stable todo id such as todo_1.",
				Required: true,
			},
			"section_id": {
				Type:     schema.String,
				Desc:     "ID of the section this todo belongs to.",
				Required: true,
			},
			"title": {
				Type:     schema.String,
				Desc:     "Short todo title.",
				Required: true,
			},
			"question": {
				Type:     schema.String,
				Desc:     "Specific research question to answer for this todo.",
				Required: true,
			},
			"search_queries":      stringArray("Initial web search queries for evidence-gathering todos.", false),
			"acceptance_criteria": stringArray("Concrete criteria for considering this todo complete.", true),
			"depends_on":          stringArray("Todo ids that must complete before this todo starts.", false),
		},
	}

	return &schema.ToolInfo{
		Name: researchTodoPlanToolName,
		Desc: "Create a ResearchTodoPlan. Use 3 to 6 sections and 4 to 10 todos. Todos must reference existing sections, include acceptance criteria, and use dependencies only when needed.",
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"objective": {
				Type:     schema.String,
				Desc:     "Research objective derived from the user's question.",
				Required: true,
			},
			"sections": {
				Type:     schema.Array,
				ElemInfo: section,
				Desc:     "Research sections that organize the todo plan.",
				Required: true,
			},
			"todos": {
				Type:     schema.Array,
				ElemInfo: todo,
				Desc:     "Executable research todos.",
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
		result.LegacyPlan = &plan
		return "", false
	}
	if step, ok := parseStepExecution(content); ok {
		step = normalizeStepExecutionSources(step)
		result.ExecutedSteps = append(result.ExecutedSteps, step)
		result.Sources = search.DeduplicateStable(append(result.Sources, step.Sources...))
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
	if err := plan.Validate(); err != nil {
		return ResearchPlan{}, false
	}
	return plan, true
}

func parseResearchTodoPlan(content string) (ResearchTodoPlan, error) {
	var plan ResearchTodoPlan
	if err := json.Unmarshal([]byte(content), &plan); err != nil {
		return ResearchTodoPlan{}, fmt.Errorf("invalid ResearchTodoPlan JSON: %w", err)
	}
	if err := plan.Validate(); err != nil {
		return ResearchTodoPlan{}, fmt.Errorf("invalid ResearchTodoPlan: %w", err)
	}
	return plan, nil
}

func parseResearchTodoPlanToolCall(msg *schema.Message) (ResearchTodoPlan, error) {
	if msg == nil {
		return ResearchTodoPlan{}, fmt.Errorf("planner model response is nil")
	}
	for _, toolCall := range msg.ToolCalls {
		if toolCall.Function.Name != researchTodoPlanToolName {
			continue
		}
		plan, err := parseResearchTodoPlan(toolCall.Function.Arguments)
		if err != nil {
			return ResearchTodoPlan{}, fmt.Errorf("planner tool call %s returned invalid arguments: %w", researchTodoPlanToolName, err)
		}
		return plan, nil
	}
	return ResearchTodoPlan{}, fmt.Errorf("planner did not call %s", researchTodoPlanToolName)
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
