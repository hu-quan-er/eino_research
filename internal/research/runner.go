package research

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/hu-quan-er/eino_research/internal/search"
)

// RunnerConfig 汇总 Runner 需要的模型、搜索 provider、预算和可替换执行组件。
//
// TodoExecutor/TodoReplanner/TodoDispatcher 主要用于测试注入和后续策略替换；生产路径在
// 未传入时会使用 Runner 的默认实现。
type RunnerConfig struct {
	// Model 是所有 planner/researcher/synthesizer 共享的 tool-calling chat model。
	Model model.ToolCallingChatModel
	// SearchProvider 是 web_search 工具实际调用的搜索 provider。
	SearchProvider search.Provider
	// ModelName 记录到 Metadata 中，便于结果审计。
	ModelName string
	// SearchProviderName 记录到 Metadata 中，便于结果审计。
	SearchProviderName string
	// MaxSearchesPerStep 限制每个 step/todo 内 web_search 调用次数。
	MaxSearchesPerStep int
	// ResultsPerSearch 限制每次 web_search 返回结果数。
	ResultsPerSearch int
	// MaxParallelTodos 限制 TodoScheduler 同时运行的 todo 数。
	MaxParallelTodos int
	// MaxResearchersPerTodo 限制单个 todo 派生的 researcher 数。
	MaxResearchersPerTodo int
	// MaxTodoResearchIterations 限制单个 todo 因 gap retry 的最大轮数。
	MaxTodoResearchIterations int
	// TodoExecutor 可替换默认 todo 执行器，主要用于测试或策略注入。
	TodoExecutor TodoExecutor
	// TodoReplanner 可在 todo 失败后返回 plan patch。
	TodoReplanner TodoReplanner
	// TodoDispatcher 可替换默认规则派发器。
	TodoDispatcher TodoDispatcher
	// FinalSynthesizer 可替换最终全局综合器；未传入时使用 AgentFinalSynthesizer。
	FinalSynthesizer FinalSynthesizer
	// EvidenceBinder 可替换最终答案证据绑定器；未传入时使用 RuleBasedEvidenceBinder。
	EvidenceBinder EvidenceBinder
	// ClaimVerifier 可替换最终 claim 校验器；未传入时使用 RuleBasedClaimVerifier。
	ClaimVerifier ClaimVerifier
	// SectionSynthesizer 可替换 section 级归纳器；未传入时使用 AgentSectionSynthesizer。
	SectionSynthesizer SectionSynthesizer
	// Events 是可选的外部事件总线。无论是否传入，Runner 都会在 Execute 内部额外挂上
	// TraceStore 和 BudgetMeter 以填充 Metadata.Trace/Budget；传入时事件也会 fan-out 给它。
	Events *EventBus
}

// Runner 是 research workflow 的门面。
//
// 推荐路径是先调用 Plan 得到可确认的 ResearchTodoPlan，再调用 Execute 执行；Run 是这两个
// 阶段的便捷组合，适合不需要人工确认 plan 的调用方。
type Runner struct {
	// cfg 保存 workflow 依赖和预算；Runner 方法不会修改调用方传入的原始配置。
	cfg RunnerConfig
}

// researchTodoPlanToolName 是 planner 强制调用的 tool 名称，必须与 researchTodoPlanToolInfo 保持一致。
const researchTodoPlanToolName = "create_research_todo_plan"

// NewRunner 校验必需依赖并填充预算默认值。
func NewRunner(cfg RunnerConfig) (*Runner, error) {
	if isNilDependency(cfg.Model) {
		return nil, fmt.Errorf("model is required")
	}
	if isNilDependency(cfg.SearchProvider) {
		return nil, fmt.Errorf("search provider is required")
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

	runID := runIDFromContext(ctx)
	if runID == "" {
		runID = generateRunID()
	}
	r.cfg.Events.Emit(ctx, Event{Kind: EventPlanStarted, RunID: runID})
	defer r.cfg.Events.Emit(ctx, Event{Kind: EventPlanCompleted, RunID: runID})

	// 首选 tool-call，因为 schema 约束能显著减少 planner 输出格式漂移。
	if plan, previousOutput, previousErr := r.planWithToolCall(ctx, question); previousErr == nil {
		return plan, nil
	} else if strings.TrimSpace(previousOutput) != "" {
		// tool-call 返回了 arguments 但未通过校验时，把原始 arguments 和错误反馈给 repair prompt。
		if err := ctx.Err(); err != nil {
			return ResearchTodoPlan{}, err
		}
		return r.planWithTextRepair(ctx, question, previousOutput, previousErr)
	}
	if err := ctx.Err(); err != nil {
		return ResearchTodoPlan{}, err
	}

	// 如果 tool-call 不可用或模型完全没调用工具，则退回纯文本 JSON repair 路径。
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
		// 保存本轮错误，下一轮 prompt 会要求模型针对该错误修复输出。
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
			Model:                     r.cfg.ModelName,
			SearchProvider:            r.cfg.SearchProviderName,
			MaxTodoResearchIterations: r.cfg.MaxTodoResearchIterations,
			StartedAt:                 started.Format(time.RFC3339),
		},
	}

	// 事件总线：复用 caller 传入的 bus（如有），并始终额外挂上内置 TraceStore/BudgetMeter，
	// 保证 Metadata.Trace/Budget 总有值，同时把事件 fan-out 给外部 bus。
	bus := r.cfg.Events
	if bus == nil {
		bus = &EventBus{}
	}
	trace := NewTraceStore(0)
	budget := NewBudgetMeter()
	bus.Add(trace)
	bus.Add(budget)

	runID := runIDFromContext(ctx)
	if runID == "" {
		runID = generateRunID()
	}
	// 把 bus 和 runID 通过 ctx 下传给 scheduler/executor/tool，避免改动它们的签名。
	ctx = withEventBus(ctx, bus)
	ctx = WithRunID(ctx, runID)

	defer func() {
		completed := time.Now()
		result.Metadata.CompletedAt = completed.Format(time.RFC3339)
		result.Metadata.DurationMS = completed.Sub(started).Milliseconds()
		result.Metadata.Trace = trace.Snapshot()
		snap := budget.Snapshot()
		result.Metadata.Budget = &snap
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
	if err := validateResearchTodoPlanQuality(plan); err != nil {
		result.Error = &RunError{Stage: "plan", Message: err.Error()}
		return result, err
	}

	executor := r.cfg.TodoExecutor
	if executor == nil {
		executor = r.executeTodo
	}
	// Scheduler 只负责依赖图调度，具体 todo 内部如何派发 researcher 由 executor 决定。
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
	// 执行完成后再按原 plan 顺序重组 section，避免并发完成顺序影响最终报告结构。
	result.SectionExecutions = groupTodoExecutionsBySection(plan, todoExecutions)
	result.Sources = collectTodoExecutionSources(todoExecutions)
	result.Documents = collectTodoExecutionDocuments(todoExecutions)

	// section 级归纳：压缩 Final 输入，并为每个 section 提供独立失败隔离。
	sectionExecutions, sectionAnswers := r.synthesizeSections(ctx, question, plan, result.SectionExecutions)
	result.SectionExecutions = sectionExecutions

	// 最终输出走三段式后处理：
	// 1. FinalSynthesizer 负责跨 todo 的完整回答；
	// 2. EvidenceBinder 把回答里的关键 claim 重新绑定到 source/chunk；
	// 3. ClaimVerifier 根据绑定结果移除或降级无证据 claim。
	bus.Emit(ctx, Event{Kind: EventFinalStarted, RunID: runID})
	result.Answer = r.synthesizeFinalAnswer(ctx, FinalSynthesisInput{
		Question:          question,
		Plan:              plan,
		SectionExecutions: result.SectionExecutions,
		TodoExecutions:    result.TodoExecutions,
		Sources:           result.Sources,
		Documents:         result.Documents,
		SectionAnswers:    sectionAnswers,
	})
	bus.Emit(ctx, Event{Kind: EventFinalCompleted, RunID: runID})

	result.Answer = r.bindFinalEvidence(ctx, EvidenceBindingInput{
		Question:       question,
		Answer:         result.Answer,
		Sources:        result.Sources,
		Documents:      result.Documents,
		TodoExecutions: result.TodoExecutions,
	})
	bus.Emit(ctx, Event{Kind: EventEvidenceBound, RunID: runID})

	result.Answer = r.verifyFinalClaims(ctx, ClaimVerificationInput{
		Question: question,
		Answer:   result.Answer,
	})
	bus.Emit(ctx, Event{Kind: EventClaimsVerified, RunID: runID})

	return result, nil
}

// Run 执行当前 todo-plan 主流程。
//
// 它等价于 Plan + Execute。CLI 为了让用户确认 plan 仍显式分两步调用；库调用方如果不需要
// 人工确认，可以直接使用 Run。
func (r *Runner) Run(ctx context.Context, question string) (result ResearchResult, err error) {
	if strings.TrimSpace(question) == "" {
		err := fmt.Errorf("question is required")
		result.Error = &RunError{Stage: "input", Message: err.Error()}
		return result, err
	}

	plan, err := r.Plan(ctx, question)
	if err != nil {
		result.Question = question
		result.Error = &RunError{Stage: "plan", Message: err.Error()}
		return result, err
	}
	return r.Execute(ctx, question, plan)
}

// executeTodo 是单个 todo 的默认执行器。
//
// 逻辑顺序为：派发 researcher jobs、构建 web_search/web_fetch 工具、并行执行 researcher、
// synthesis、bounded gap retry，最后把 StepExecution 转回 TodoExecution。
func (r *Runner) executeTodo(ctx context.Context, in TodoExecutorInput) (TodoExecution, error) {
	// todo.started/completed/failed 由 TodoScheduler 统一发出（覆盖所有 executor 实现）；
	// executeTodo 只负责发出执行器内部的 dispatch/researcher/synthesis 事件。
	bus := eventBusFromContext(ctx)
	runID := runIDFromContext(ctx)

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
	// 派发层把一个 todo 拆成多个角色化 researcher job，但仍共享同一组工具预算。
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
	bus.Emit(ctx, Event{Kind: EventTodoDispatched, RunID: runID, TodoID: in.Todo.ID, Message: fmt.Sprintf("%d researcher jobs", len(jobs))})

	// 工具调用事件通过 bus 转发，使 BudgetMeter 能统计 web_search/web_fetch 次数。
	toolEmit := ToolEmitFunc(func(ctx context.Context, e Event) {
		e.RunID = runID
		e.TodoID = in.Todo.ID
		bus.Emit(ctx, e)
	})
	searchTool, err := NewWebSearchTool(r.cfg.SearchProvider, SearchLimits{
		MaxSearchesPerStep: maxSearches,
		ResultsPerSearch:   resultsPerSearch,
		SourceIDPrefix:     sourceIDPrefix(in.Todo.ID),
	}, WithToolEmit(toolEmit))
	if err != nil {
		return TodoExecution{}, fmt.Errorf("new web search tool: %w", err)
	}
	// web_fetch 成功读取的页面先记录在 store 中，step 执行结束后统一转成 SourceDocument。
	fetchedPages := NewFetchedPageStore()
	fetchTool, err := NewWebFetchTool(HTTPPageFetcher{}, FetchLimits{
		MaxFetchesPerStep: maxSearches,
		MaxContentChars:   4000,
		Recorder:          fetchedPages,
	}, WithToolEmit(toolEmit))
	if err != nil {
		return TodoExecution{}, fmt.Errorf("new web fetch tool: %w", err)
	}

	researchers, err := buildTodoResearchers(ctx, r.cfg, jobs, searchTool, fetchTool)
	if err != nil {
		return TodoExecution{}, err
	}

	step := todoToResearchStep(in.Todo, in.Plan)
	stepExecutor := NewParallelStepExecutor(researchers, NewAgentSynthesizer(r.cfg.Model))
	// bounded loop 会在结果存在确定性 gap 时把上一轮执行结果作为 prior context 继续尝试。
	execution, err := runTodoResearchLoop(ctx, TodoResearchLoopInput{
		Plan:                 in.Plan,
		Todo:                 in.Todo,
		DependencyExecutions: in.DependencyExecutions,
		MaxAttempts:          r.cfg.MaxTodoResearchIterations,
		ExecuteStep: func(ctx context.Context, input StepExecutionInput) (StepExecution, error) {
			for _, job := range jobs {
				bus.Emit(ctx, Event{Kind: EventResearcherStarted, RunID: runID, TodoID: in.Todo.ID, Role: job.RoleID})
			}
			if strings.TrimSpace(input.Step.ID) == "" {
				input.Step = step
			}
			execution, err := stepExecutor.ExecuteStep(ctx, input)
			if err != nil {
				return StepExecution{}, err
			}
			for _, res := range execution.ResearcherResults {
				bus.Emit(ctx, Event{Kind: EventResearcherCompleted, RunID: runID, TodoID: in.Todo.ID, Role: res.Role})
			}
			bus.Emit(ctx, Event{Kind: EventSynthesisCompleted, RunID: runID, TodoID: in.Todo.ID})
			// fetched 正文比搜索 snippet 更适合做 evidence quote，因此作为 primary document 合并。
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
	// section/todo schema 与 ResearchTodoPlan JSON 字段保持一致；tool schema 只描述形状，
	// 更强的依赖图和质量规则由 parseResearchTodoPlan 后续执行。
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

// synthesizeSections 对每个 section 调用 SectionSynthesizer（v1 串行）。
//
// 成功时用 SectionAnswer 回填 SectionExecution.Summary/KeyFindings/Limitations；失败或空
// answer 时走确定性兜底（聚合该 section 下 todo 的 findings/gaps）。兜底也产出 SectionAnswer，
// 因此返回的 []SectionAnswer 始终覆盖全部 section，供 Final 走紧凑路径。
func (r *Runner) synthesizeSections(ctx context.Context, question string, plan ResearchTodoPlan, sections []SectionExecution) ([]SectionExecution, []SectionAnswer) {
	synthesizer := r.cfg.SectionSynthesizer
	if synthesizer == nil {
		synthesizer = NewAgentSectionSynthesizer(r.cfg.Model)
	}

	bus := eventBusFromContext(ctx)
	runID := runIDFromContext(ctx)

	out := make([]SectionExecution, len(sections))
	answers := make([]SectionAnswer, 0, len(sections))
	for i, section := range sections {
		bus.Emit(ctx, Event{Kind: EventSectionStarted, RunID: runID, TodoID: section.Section.ID})
		ans, err := synthesizer.SynthesizeSection(ctx, SectionSynthesisInput{
			Question:  question,
			Objective: plan.Objective,
			Section:   section.Section,
			Todos:     section.Todos,
			Documents: collectSectionDocuments(section),
		})
		fellBack := false
		if err != nil || isEmptySectionAnswer(ans) {
			ans = fallbackSectionAnswer(section)
			fellBack = true
		}
		section.Summary = ans.Summary
		section.KeyFindings = ans.KeyFindings
		section.Limitations = ans.Limitations
		out[i] = section
		answers = append(answers, ans)
		msg := ""
		if fellBack {
			msg = "section synthesis used deterministic fallback"
		}
		bus.Emit(ctx, Event{Kind: EventSectionCompleted, RunID: runID, TodoID: section.Section.ID, Message: msg})
	}
	return out, answers
}

// fallbackSectionAnswer 在模型合成失败时基于 todo 结果构造确定性 SectionAnswer。
func fallbackSectionAnswer(section SectionExecution) SectionAnswer {
	summary := summarizeSectionTodos(section.Todos)
	return SectionAnswer{
		SectionID:   section.Section.ID,
		Title:       sectionTitleOrID(section.Section),
		Summary:     summary,
		KeyFindings: collectTopFindingClaims(section.Todos, 5),
		Limitations: collectFinalLimitations(section.Todos),
	}
}

// collectSectionDocuments 合并该 section 下所有 todo 的 documents 并去重。
func collectSectionDocuments(section SectionExecution) []SourceDocument {
	documents := make([]SourceDocument, 0)
	for _, todo := range section.Todos {
		documents = append(documents, todo.Documents...)
	}
	return dedupeSourceDocuments(documents)
}
