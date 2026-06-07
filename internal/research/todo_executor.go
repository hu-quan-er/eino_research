package research

import (
	"context"
	"fmt"
	"strings"

	"github.com/cloudwego/eino/components/model"
	"github.com/hu-quan-er/eino_research/internal/search"
)

// defaultTodoExecutor 是默认的单 todo 执行器实现。
//
// 它从 Runner 中抽出，使 Runner 只保留 workflow 编排职责，而单个 todo 的派发、工具构建、
// 并行研究、synthesis 和 bounded gap retry 成为可独立阅读和测试的单元。它只持有执行
// 一个 todo 真正需要的依赖，而不是整份 RunnerConfig。
type defaultTodoExecutor struct {
	// model 是 researcher 和 synthesizer 共享的 tool-calling chat model。
	model model.ToolCallingChatModel
	// searchProvider 是 web_search 工具实际调用的搜索 provider。
	searchProvider search.Provider
	// dispatcher 把 todo 拆成角色化 researcher job；为空时使用 RuleBasedTodoDispatcher。
	dispatcher TodoDispatcher
	// maxSearchesPerStep 限制单个 todo 内 web_search/web_fetch 调用次数。
	maxSearchesPerStep int
	// resultsPerSearch 限制每次 web_search 返回结果数。
	resultsPerSearch int
	// maxResearchersPerTodo 限制单个 todo 派生的 researcher 数。
	maxResearchersPerTodo int
	// maxTodoResearchIterations 限制单个 todo 因 gap retry 的最大轮数。
	maxTodoResearchIterations int
}

// newDefaultTodoExecutor 从 RunnerConfig 中取出执行单个 todo 所需的依赖。
//
// 预算默认值由 NewRunner 统一兜底，因此这里直接复制即可，不再二次填充默认常量。
func newDefaultTodoExecutor(cfg RunnerConfig) *defaultTodoExecutor {
	return &defaultTodoExecutor{
		model:                     cfg.Model,
		searchProvider:            cfg.SearchProvider,
		dispatcher:                cfg.TodoDispatcher,
		maxSearchesPerStep:        cfg.MaxSearchesPerStep,
		resultsPerSearch:          cfg.ResultsPerSearch,
		maxResearchersPerTodo:     cfg.MaxResearchersPerTodo,
		maxTodoResearchIterations: cfg.MaxTodoResearchIterations,
	}
}

// execute 是单个 todo 的默认执行逻辑，签名与 TodoExecutor 函数类型一致，可作为 method
// value 直接注入 TodoScheduler。
//
// 逻辑顺序为：派发 researcher jobs、构建 web_search/web_fetch 工具、并行执行 researcher、
// synthesis、bounded gap retry，最后把 StepExecution 转回 TodoExecution。
func (e *defaultTodoExecutor) execute(ctx context.Context, in TodoExecutorInput) (TodoExecution, error) {
	// todo.started/completed/failed 由 TodoScheduler 统一发出（覆盖所有 executor 实现）；
	// execute 只负责发出执行器内部的 dispatch/researcher/synthesis 事件。
	bus := eventBusFromContext(ctx)
	runID := runIDFromContext(ctx)

	maxSearches := e.maxSearchesPerStep
	resultsPerSearch := e.resultsPerSearch

	dispatcher := e.dispatcher
	if dispatcher == nil {
		dispatcher = RuleBasedTodoDispatcher{MaxResearchers: e.maxResearchersPerTodo}
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
	toolEmit := ToolEmitFunc(func(ctx context.Context, ev Event) {
		ev.RunID = runID
		ev.TodoID = in.Todo.ID
		bus.Emit(ctx, ev)
	})
	searchTool, err := NewWebSearchTool(e.searchProvider, SearchLimits{
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

	researchers, err := buildTodoResearchers(ctx, e.model, jobs, searchTool, fetchTool)
	if err != nil {
		return TodoExecution{}, err
	}

	step := todoToResearchStep(in.Todo, in.Plan)
	stepExecutor := NewParallelStepExecutor(researchers, NewAgentSynthesizer(e.model))
	// bounded loop 会在结果存在确定性 gap 时把上一轮执行结果作为 prior context 继续尝试。
	execution, err := runTodoResearchLoop(ctx, TodoResearchLoopInput{
		Plan:                 in.Plan,
		Todo:                 in.Todo,
		DependencyExecutions: in.DependencyExecutions,
		MaxAttempts:          e.maxTodoResearchIterations,
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
