package research

import (
	"context"
	"fmt"
	"strings"
)

// TodoResearchRole 描述一个 todo 内部派生出的研究角色。
type TodoResearchRole struct {
	// ID 是角色稳定标识，会作为 AgentResearcher 的 role/name。
	ID string
	// Name 是面向人类阅读的角色名称。
	Name string
	// Focus 是该角色在 prompt 中的研究重点。
	Focus string
}

// TodoResearchBudget 是派发给单个 todo 的工具和 token 预算。
//
// 当前主要使用 MaxSearches/MaxFetches，MaxTokens 预留给后续模型调用预算控制。
type TodoResearchBudget struct {
	// MaxSearches 是该 todo 内允许的最大搜索次数。
	MaxSearches int
	// MaxFetches 是该 todo 内允许的最大页面读取次数。
	MaxFetches int
	// MaxTokens 预留给未来 token 预算控制。
	MaxTokens int
}

// TodoResearchContext 是 researcher job 需要理解当前 todo 的上下文。
type TodoResearchContext struct {
	// Objective 是全局研究目标。
	Objective string
	// Todo 是当前 job 所属的 todo。
	Todo ResearchTodo
	// DependencyExecutions 是已完成依赖的结果。
	DependencyExecutions []TodoExecution
}

// TodoResearchJob 是 TodoDispatcher 的输出，也是 buildTodoResearchers 的输入。
type TodoResearchJob struct {
	// TodoID 是当前 job 所属 todo 的 id。
	TodoID string
	// RoleID 是 researcher 的稳定角色 ID。
	RoleID string
	// Name 是人类可读角色名。
	Name string
	// Focus 是传给 researcher 的研究方向。
	Focus string
	// Context 是 researcher 需要理解当前 todo 的上下文。
	Context TodoResearchContext
	// Budget 是该 job 共享的 todo 级工具预算。
	Budget TodoResearchBudget
}

// TodoDispatchInput 是派发层决策 researcher jobs 的输入。
type TodoDispatchInput struct {
	// Plan 是完整 todo plan，派发规则会读取 objective 和 section 信息。
	Plan ResearchTodoPlan
	// Todo 是当前待派发的 runnable todo。
	Todo ResearchTodo
	// DependencyExecutions 是当前 todo 已完成依赖的结果。
	DependencyExecutions []TodoExecution
	// Budget 是执行器分配给当前 todo 的工具预算。
	Budget TodoResearchBudget
}

// TodoDispatcher 把一个 runnable todo 转换为一组角色化 researcher jobs。
type TodoDispatcher interface {
	Dispatch(ctx context.Context, in TodoDispatchInput) ([]TodoResearchJob, error)
}

// RuleBasedTodoDispatcher 使用确定性规则派发 researcher。
//
// 这样 planner 只需关注 todo 拆解，角色 fan-out 由代码控制，便于测试、限流和兜底。
type RuleBasedTodoDispatcher struct {
	// MaxResearchers 是最多派发的角色数量；<=0 表示不截断。
	MaxResearchers int
}

// Dispatch 根据 todo 类型和关键词派生 researcher jobs，并应用 MaxResearchers 上限。
func (d RuleBasedTodoDispatcher) Dispatch(ctx context.Context, in TodoDispatchInput) ([]TodoResearchJob, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(in.Todo.ID) == "" {
		return nil, fmt.Errorf("todo id is required")
	}

	roles := DeriveTodoResearchRoles(in.Todo, in.Plan)
	limit := d.MaxResearchers
	if limit <= 0 {
		limit = len(roles)
	}
	if limit > len(roles) {
		limit = len(roles)
	}

	jobs := make([]TodoResearchJob, 0, limit)
	for _, role := range roles[:limit] {
		jobs = append(jobs, TodoResearchJob{
			TodoID: strings.TrimSpace(in.Todo.ID),
			RoleID: role.ID,
			Name:   role.Name,
			Focus:  role.Focus,
			Context: TodoResearchContext{
				Objective:            in.Plan.Objective,
				Todo:                 in.Todo,
				DependencyExecutions: in.DependencyExecutions,
			},
			Budget: in.Budget,
		})
	}
	return jobs, nil
}

// DeriveTodoResearchRoles 根据 todo 内容确定需要哪些研究视角。
//
// synthesis todo 会走综合/查漏角色；普通 todo 至少包含 background/evidence/counterpoint，
// 时效性 todo 会额外加入 freshness 角色。
func DeriveTodoResearchRoles(todo ResearchTodo, plan ResearchTodoPlan) []TodoResearchRole {
	if isSynthesisTodo(todo) {
		return []TodoResearchRole{
			{
				ID:    "synthesis_researcher",
				Name:  "Synthesis Researcher",
				Focus: "synthesize completed dependency findings, identify the strongest conclusion, and preserve uncertainty",
			},
			{
				ID:    "gap_checker",
				Name:  "Gap Checker",
				Focus: "check unresolved questions, missing evidence, weak assumptions, and limitations before the final todo answer",
			},
		}
	}

	roles := []TodoResearchRole{
		{
			ID:    "background_researcher",
			Name:  "Background Researcher",
			Focus: "definitions, context, timeline, prerequisites, and key concepts for this todo",
		},
		{
			ID:    "evidence_researcher",
			Name:  "Evidence Researcher",
			Focus: "authoritative evidence, examples, implementation details, and source-backed facts for this todo",
		},
	}
	if todoNeedsFreshness(todo, plan) {
		roles = append(roles, TodoResearchRole{
			ID:    "freshness_researcher",
			Name:  "Freshness Researcher",
			Focus: "current information, latest versions, recent changes, dates, and time-sensitive claims",
		})
	}
	roles = append(roles, TodoResearchRole{
		ID:    "counterpoint_researcher",
		Name:  "Counterpoint Researcher",
		Focus: "counterexamples, risks, limitations, conflicting evidence, and dissenting views for this todo",
	})
	return roles
}

// todoNeedsFreshness 用关键词判断 todo 是否需要 freshness_researcher。
//
// 这是确定性启发式，后续如果引入模型 judge/dispatcher，也应保留该规则作为 fallback。
func todoNeedsFreshness(todo ResearchTodo, plan ResearchTodoPlan) bool {
	text := normalizeLintText(strings.Join([]string{
		plan.Objective,
		todo.Title,
		todo.Question,
		strings.Join(todo.SearchQueries, " "),
	}, " "))
	for _, keyword := range []string{
		"2026",
		"current",
		"latest",
		"market",
		"modern",
		"new",
		"news",
		"recent",
		"today",
		"version",
	} {
		if strings.Contains(text, keyword) {
			return true
		}
	}
	return false
}

// todoToResearchStep 把当前主流程的 ResearchTodo 转换为可复用执行器需要的 ResearchStep。
func todoToResearchStep(todo ResearchTodo) ResearchStep {
	title := strings.TrimSpace(todo.Title)
	if title == "" {
		title = strings.TrimSpace(todo.ID)
	}
	return ResearchStep{
		ID:              strings.TrimSpace(todo.ID),
		Title:           title,
		Question:        strings.TrimSpace(todo.Question),
		SearchQueries:   nonEmptyStrings(todo.SearchQueries),
		SuccessCriteria: nonEmptyStrings(todo.AcceptanceCriteria),
	}
}

// dependencyExecutionsAsSteps 将已完成依赖转换为 prior executed steps，供当前 todo researcher
// 读取上下文。
func dependencyExecutionsAsSteps(executions []TodoExecution) []StepExecution {
	steps := make([]StepExecution, 0, len(executions))
	for _, execution := range executions {
		steps = append(steps, StepExecution{
			Step:              todoToResearchStep(execution.Todo),
			ResearcherResults: execution.ResearcherResults,
			Summary:           execution.Summary,
			Gaps:              execution.Gaps,
			Sources:           execution.Sources,
		})
	}
	return steps
}

// stepExecutionToTodoExecution 把并行 researcher + synthesis 的结果转换回 todo 结果。
//
// 转换时会再次运行 evidence 归一化，确保最终 TodoExecution.Findings 已带可渲染的证据引用。
func stepExecutionToTodoExecution(todo ResearchTodo, step StepExecution) TodoExecution {
	step = normalizeStepExecutionSources(step)
	findings := make([]Finding, 0)
	for _, result := range step.ResearcherResults {
		findings = append(findings, result.Findings...)
	}
	findings = enrichFindingsEvidence(findings, step.Documents)

	summary := strings.TrimSpace(step.Summary)
	if summary == "" {
		summary = strings.TrimSpace(todo.Title)
	}

	return TodoExecution{
		Todo:              todo,
		Status:            TodoDone,
		ResearcherResults: step.ResearcherResults,
		Summary:           summary,
		Findings:          findings,
		Gaps:              step.Gaps,
		Sources:           step.Sources,
		Documents:         step.Documents,
	}
}

// nonEmptyStrings 清理字符串数组，保留非空项且保持原顺序。
func nonEmptyStrings(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			out = append(out, value)
		}
	}
	return out
}
