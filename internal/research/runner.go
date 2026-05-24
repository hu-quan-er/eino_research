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

// RunnerConfig 汇总 Runner 需要的模型、搜索 provider、预算和可替换执行组件。
//
// TodoExecutor/TodoReplanner/TodoDispatcher 主要用于测试注入和后续策略替换；生产路径在
// 未传入时会使用 Runner 的默认实现。
type RunnerConfig struct {
	Model                     model.ToolCallingChatModel
	SearchProvider            search.Provider
	ModelName                 string
	SearchProviderName        string
	MaxIterations             int
	MaxSearchesPerStep        int
	ResultsPerSearch          int
	MaxParallelTodos          int
	MaxResearchersPerTodo     int
	MaxTodoResearchIterations int
	TodoExecutor              TodoExecutor
	TodoReplanner             TodoReplanner
	TodoDispatcher            TodoDispatcher
}

// Runner 是 research workflow 的门面。
//
// 推荐路径是先调用 Plan 得到可确认的 ResearchTodoPlan，再调用 Execute 执行；Run 保留给
// legacy Eino planexecute 流程。
type Runner struct {
	cfg RunnerConfig
}

const researchTodoPlanToolName = "create_research_todo_plan"

// NewRunner 校验必需依赖并填充预算默认值。
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
	if cfg.MaxResearchersPerTodo <= 0 {
		cfg.MaxResearchersPerTodo = 3
	}
	if cfg.MaxTodoResearchIterations <= 0 {
		cfg.MaxTodoResearchIterations = 2
	}

	return &Runner{cfg: cfg}, nil
}

// Plan 负责把用户问题转换为 ResearchTodoPlan。
//
// 它优先使用强制 tool-call 方式约束模型输出；如果 tool-call 不可用或返回非法参数，则带着
// 上一次输出和错误信息进入文本 JSON repair 流程。
func (r *Runner) Plan(ctx context.Context, question string) (ResearchTodoPlan, error) {
	if strings.TrimSpace(question) == "" {
		return ResearchTodoPlan{}, fmt.Errorf("question is required")
	}

	if plan, previousOutput, previousErr := r.planWithToolCall(ctx, question); previousErr == nil {
		return plan, nil
	} else if strings.TrimSpace(previousOutput) != "" {
		if err := ctx.Err(); err != nil {
			return ResearchTodoPlan{}, err
		}
		return r.planWithTextRepair(ctx, question, previousOutput, previousErr)
	}
	if err := ctx.Err(); err != nil {
		return ResearchTodoPlan{}, err
	}

	return r.planWithTextRepair(ctx, question, "", nil)
}

// planWithToolCall 通过 ToolChoiceForced 要求模型调用 create_research_todo_plan。
//
// 返回值中的 string 是原始 tool arguments；当校验失败时，外层会把它作为 repair 输入。
func (r *Runner) planWithToolCall(ctx context.Context, question string) (ResearchTodoPlan, string, error) {
	toolModel, err := r.cfg.Model.WithTools([]*schema.ToolInfo{researchTodoPlanToolInfo()})
	if err != nil {
		return ResearchTodoPlan{}, "", fmt.Errorf("bind planner tool: %w", err)
	}

	resp, err := toolModel.Generate(
		ctx,
		plannerMessages(question, "", nil),
		model.WithToolChoice(schema.ToolChoiceForced, researchTodoPlanToolName),
	)
	if err != nil {
		return ResearchTodoPlan{}, "", err
	}
	return parseResearchTodoPlanToolCall(resp)
}

// planWithTextRepair 是 planner 的兜底路径。
//
// 每轮都会把上一轮的解析/校验/lint 错误发回模型，要求其只返回修正后的
// ResearchTodoPlan JSON。
func (r *Runner) planWithTextRepair(ctx context.Context, question, previousOutput string, previousErr error) (ResearchTodoPlan, error) {
	const maxPlannerAttempts = 3
	lastOutput := previousOutput
	lastErr := previousErr

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

// plannerMessages 构造 planner 和 repair 共用的提示词。
//
// 当 previousErr 不为空时，prompt 会包含上一次原始输出和错误详情，便于模型做定向修复。
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

Validation, linting, or parsing error:
%s

Previous output:
%s

Return only a corrected ResearchTodoPlan JSON object. Do not include markdown, explanation, or extra text.`, question, previousErr.Error(), previousOutput)),
	}
}

// Execute 执行已经确认过的 ResearchTodoPlan。
//
// 该方法是当前主路径：先由 TodoScheduler 按依赖顺序调度 todo，再聚合 section、source、
// document 和 summary，最终返回完整 ResearchResult。
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
	result.Documents = collectTodoExecutionDocuments(todoExecutions)
	result.Answer.Summary = fmt.Sprintf("Completed %d todo(s).", countTodoStatus(todoExecutions, TodoDone))
	result.Answer.Markdown = result.Answer.Summary

	return result, nil
}

// Run 执行 legacy Eino planexecute 流程。
//
// 当前 CLI 已转向 Plan + Execute；保留 Run 是为了兼容早期测试和对比 Eino 原生
// planexecute 行为。
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

// buildAgent 组装 legacy planexecute agent。
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

// executeTodo 是单个 todo 的默认执行器。
//
// 逻辑顺序为：派发 researcher jobs、构建 web_search/web_fetch 工具、并行执行 researcher、
// synthesis、bounded gap retry，最后把 StepExecution 转回 TodoExecution。
func (r *Runner) executeTodo(ctx context.Context, in TodoExecutorInput) (TodoExecution, error) {
	maxSearches := r.cfg.MaxSearchesPerStep
	if maxSearches <= 0 {
		maxSearches = 6
	}
	resultsPerSearch := r.cfg.ResultsPerSearch
	if resultsPerSearch <= 0 {
		resultsPerSearch = 5
	}

	dispatcher := r.cfg.TodoDispatcher
	if dispatcher == nil {
		dispatcher = RuleBasedTodoDispatcher{MaxResearchers: r.cfg.MaxResearchersPerTodo}
	}
	jobs, err := dispatcher.Dispatch(ctx, TodoDispatchInput{
		Plan:                 in.Plan,
		Todo:                 in.Todo,
		DependencyExecutions: in.DependencyExecutions,
		Budget: TodoResearchBudget{
			MaxSearches: maxSearches,
			MaxFetches:  maxSearches,
		},
	})
	if err != nil {
		return TodoExecution{}, err
	}
	if len(jobs) == 0 {
		return TodoExecution{}, fmt.Errorf("todo dispatcher returned no jobs for todo %s", in.Todo.ID)
	}

	searchTool, err := NewWebSearchTool(r.cfg.SearchProvider, SearchLimits{
		MaxSearchesPerStep: maxSearches,
		ResultsPerSearch:   resultsPerSearch,
		SourceIDPrefix:     sourceIDPrefix(in.Todo.ID),
	})
	if err != nil {
		return TodoExecution{}, fmt.Errorf("new web search tool: %w", err)
	}
	fetchedPages := NewFetchedPageStore()
	fetchTool, err := NewWebFetchTool(HTTPPageFetcher{}, FetchLimits{
		MaxFetchesPerStep: maxSearches,
		MaxContentChars:   4000,
		Recorder:          fetchedPages,
	})
	if err != nil {
		return TodoExecution{}, fmt.Errorf("new web fetch tool: %w", err)
	}

	researchers, err := buildTodoResearchers(ctx, r.cfg, jobs, searchTool, fetchTool)
	if err != nil {
		return TodoExecution{}, err
	}

	step := todoToResearchStep(in.Todo)
	stepExecutor := NewParallelStepExecutor(researchers, NewAgentSynthesizer(r.cfg.Model))
	execution, err := runTodoResearchLoop(ctx, TodoResearchLoopInput{
		Plan:                 in.Plan,
		Todo:                 in.Todo,
		DependencyExecutions: in.DependencyExecutions,
		MaxIterations:        r.cfg.MaxTodoResearchIterations,
		ExecuteStep: func(ctx context.Context, input StepExecutionInput) (StepExecution, error) {
			if strings.TrimSpace(input.Step.ID) == "" {
				input.Step = step
			}
			execution, err := stepExecutor.ExecuteStep(ctx, input)
			if err != nil {
				return StepExecution{}, err
			}
			execution.Documents = mergeSourceDocuments(
				buildFetchedPageDocuments(fetchedPages.Pages(), execution.Sources, defaultSourceChunkChars),
				execution.Documents,
			)
			return execution, nil
		},
	})
	if err != nil {
		return TodoExecution{}, err
	}

	return stepExecutionToTodoExecution(in.Todo, execution), nil
}

// groupTodoExecutionsBySection 按 plan 中的 section/todo 顺序重排执行结果，保证报告顺序
// 与用户确认的 plan 保持一致。
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

// summarizeSectionTodos 把同一 section 下的 todo summary 拼成 section summary。
func summarizeSectionTodos(executions []TodoExecution) string {
	summaries := make([]string, 0, len(executions))
	for _, execution := range executions {
		if summary := strings.TrimSpace(execution.Summary); summary != "" {
			summaries = append(summaries, summary)
		}
	}
	return strings.Join(summaries, "\n")
}

// collectTodoExecutionSources 汇总所有 todo sources，并按 URL 去重但尽量保留稳定 ID。
func collectTodoExecutionSources(executions []TodoExecution) []search.Source {
	sources := make([]search.Source, 0)
	for _, execution := range executions {
		sources = append(sources, execution.Sources...)
	}
	return search.DeduplicateStable(sources)
}

// collectTodoExecutionDocuments 汇总 todo 级 documents，供最终报告渲染 evidence quote。
func collectTodoExecutionDocuments(executions []TodoExecution) []SourceDocument {
	documents := make([]SourceDocument, 0)
	for _, execution := range executions {
		documents = append(documents, execution.Documents...)
	}
	return dedupeSourceDocuments(documents)
}

// countTodoStatus 统计指定状态的 todo 数，用于生成简要 summary。
func countTodoStatus(executions []TodoExecution, status TodoStatus) int {
	count := 0
	for _, execution := range executions {
		if execution.Status == status {
			count++
		}
	}
	return count
}

// genResearchPlannerInput 是 legacy planexecute planner 的输入构造函数。
//
// 这里要求模型调用 plan tool，并显式禁止 string steps。
func genResearchPlannerInput(_ context.Context, userInput []adk.Message) ([]adk.Message, error) {
	messages := []adk.Message{
		schema.SystemMessage(`Create a concise research plan. You must call the plan tool with {"steps":[ResearchStep,...]} where each step has id, title, question, search_queries, research_axes, and success_criteria. Do not use plain string steps.`),
	}
	messages = append(messages, userInput...)
	return messages, nil
}

// genResearchReplannerInput 是 legacy replanner 的输入构造函数。
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

// researchPlanToolInfo 定义 legacy ResearchPlan tool schema。
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

// researchTodoPlanToolInfo 定义当前主流程使用的 ResearchTodoPlan tool schema。
//
// schema 约束只能保证字段形状；更细的依赖、重复、质量问题仍由 Validate 和 linter 处理。
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

// assistantContent 从 ADK event 中提取 assistant 文本，过滤非 assistant 输出。
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

// applyRunnerContent 解析 legacy planexecute loop 中 assistant 可能返回的三类内容：
// plan、step execution、final response。
func applyRunnerContent(result *ResearchResult, content string) (string, bool) {
	if plan, ok := parseResearchPlan(content); ok {
		result.LegacyPlan = &plan
		return "", false
	}
	if step, ok := parseStepExecution(content); ok {
		step = normalizeStepExecutionSources(step)
		result.ExecutedSteps = append(result.ExecutedSteps, step)
		result.Sources = search.DeduplicateStable(append(result.Sources, step.Sources...))
		result.Documents = mergeSourceDocuments(result.Documents, step.Documents)
		return "", false
	}
	if response, ok := parsePlanExecuteResponse(content); ok {
		return response, true
	}
	return "", false
}

// finalizeRunnerAnswer 确认 legacy loop 收到了最终回答；没有最终回答时返回明确错误，而不是
// 静默输出半成品。
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

// parseResearchPlan 尝试把文本解析为 legacy ResearchPlan。
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

// parseResearchTodoPlan 解析并校验 planner 文本 JSON 输出。
//
// 它同时运行结构校验和质量 lint，是 planner 输出进入执行层前的主要防线。
func parseResearchTodoPlan(content string) (ResearchTodoPlan, error) {
	var plan ResearchTodoPlan
	if err := json.Unmarshal([]byte(content), &plan); err != nil {
		return ResearchTodoPlan{}, fmt.Errorf("invalid ResearchTodoPlan JSON: %w", err)
	}
	if err := plan.Validate(); err != nil {
		return ResearchTodoPlan{}, fmt.Errorf("invalid ResearchTodoPlan: %w", err)
	}
	if err := validateResearchTodoPlanQuality(plan); err != nil {
		return ResearchTodoPlan{}, fmt.Errorf("invalid ResearchTodoPlan quality: %w", err)
	}
	return plan, nil
}

// parseResearchTodoPlanToolCall 从模型 tool calls 中提取并校验 ResearchTodoPlan。
//
// 返回原始 arguments 是为了在失败时传给 repair prompt。
func parseResearchTodoPlanToolCall(msg *schema.Message) (ResearchTodoPlan, string, error) {
	if msg == nil {
		return ResearchTodoPlan{}, "", fmt.Errorf("planner model response is nil")
	}
	for _, toolCall := range msg.ToolCalls {
		if toolCall.Function.Name != researchTodoPlanToolName {
			continue
		}
		plan, err := parseResearchTodoPlan(toolCall.Function.Arguments)
		if err != nil {
			return ResearchTodoPlan{}, toolCall.Function.Arguments, fmt.Errorf("planner tool call %s returned invalid arguments: %w", researchTodoPlanToolName, err)
		}
		return plan, toolCall.Function.Arguments, nil
	}
	return ResearchTodoPlan{}, "", fmt.Errorf("planner did not call %s", researchTodoPlanToolName)
}

// parseStepExecution 尝试把 assistant 内容解析为 StepExecution。
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

// parsePlanExecuteResponse 解析 legacy planexecute 的最终 respond tool 输出。
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
