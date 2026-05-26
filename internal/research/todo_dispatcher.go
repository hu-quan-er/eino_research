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

	roles := deriveTodoResearchRoles(in.Todo, in.Plan, in.DependencyExecutions)
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
// synthesis todo 会走综合/查漏角色；普通 todo 会先保留基础研究角色，再按 todo 内容、
// acceptance criteria 和依赖结果加入更专门的 researcher。
func DeriveTodoResearchRoles(todo ResearchTodo, plan ResearchTodoPlan) []TodoResearchRole {
	return deriveTodoResearchRoles(todo, plan, nil)
}

func deriveTodoResearchRoles(todo ResearchTodo, plan ResearchTodoPlan, dependencies []TodoExecution) []TodoResearchRole {
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

	roles := make([]TodoResearchRole, 0, 6)
	if todoNeedsBackground(todo, plan, dependencies) {
		roles = appendTodoResearchRole(roles, TodoResearchRole{
			ID:    "background_researcher",
			Name:  "Background Researcher",
			Focus: "definitions, context, timeline, prerequisites, and key concepts for this todo",
		})
	}
	roles = appendTodoResearchRole(roles, TodoResearchRole{
		ID:    "evidence_researcher",
		Name:  "Evidence Researcher",
		Focus: "authoritative evidence, examples, implementation details, and source-backed facts for this todo",
	})
	if todoNeedsFreshness(todo, plan) {
		roles = appendTodoResearchRole(roles, TodoResearchRole{
			ID:    "freshness_researcher",
			Name:  "Freshness Researcher",
			Focus: "current information, latest versions, recent changes, dates, and time-sensitive claims",
		})
	}
	if todoNeedsImplementationFocus(todo, plan) {
		roles = appendTodoResearchRole(roles, TodoResearchRole{
			ID:    "implementation_researcher",
			Name:  "Implementation Researcher",
			Focus: "implementation details, APIs, repositories, configuration, integration constraints, and engineering feasibility",
		})
	}
	if todoNeedsComparisonFocus(todo, plan) {
		roles = appendTodoResearchRole(roles, TodoResearchRole{
			ID:    "comparison_researcher",
			Name:  "Comparison Researcher",
			Focus: "alternatives, tradeoffs, option comparison, decision criteria, and why one path is preferable",
		})
	}
	if todoNeedsQuantitativeFocus(todo, plan) {
		roles = appendTodoResearchRole(roles, TodoResearchRole{
			ID:    "quantitative_researcher",
			Name:  "Quantitative Researcher",
			Focus: "metrics, benchmarks, prices, performance data, adoption signals, and measurable evidence",
		})
	}
	if dependenciesHaveGaps(dependencies) {
		roles = appendTodoResearchRole(roles, TodoResearchRole{
			ID:    "gap_checker",
			Name:  "Gap Checker",
			Focus: "resolve gaps inherited from dependency todos before answering this todo",
		})
	}
	roles = appendTodoResearchRole(roles, TodoResearchRole{
		ID:    "counterpoint_researcher",
		Name:  "Counterpoint Researcher",
		Focus: "counterexamples, risks, limitations, conflicting evidence, and dissenting views for this todo",
	})
	return roles
}

func appendTodoResearchRole(roles []TodoResearchRole, role TodoResearchRole) []TodoResearchRole {
	for _, existing := range roles {
		if existing.ID == role.ID {
			return roles
		}
	}
	return append(roles, role)
}

func todoNeedsBackground(todo ResearchTodo, plan ResearchTodoPlan, dependencies []TodoExecution) bool {
	if len(dependencies) == 0 {
		return true
	}
	return containsAnyDispatchKeyword(todoDispatchText(todo, plan), []string{
		"background",
		"concept",
		"context",
		"definition",
		"overview",
		"背景",
		"定义",
		"概念",
		"上下文",
	})
}

// todoNeedsFreshness 用关键词判断 todo 是否需要 freshness_researcher。
//
// 这是确定性启发式，后续如果引入模型 judge/dispatcher，也应保留该规则作为 fallback。
func todoNeedsFreshness(todo ResearchTodo, plan ResearchTodoPlan) bool {
	return containsAnyDispatchKeyword(todoDispatchText(todo, plan), []string{
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
		"当前",
		"最新",
		"近期",
		"今天",
		"版本",
	})
}

func todoNeedsImplementationFocus(todo ResearchTodo, plan ResearchTodoPlan) bool {
	return containsAnyDispatchKeyword(todoDispatchText(todo, plan), []string{
		"api",
		"architecture",
		"code",
		"component",
		"config",
		"github",
		"implementation",
		"integration",
		"repository",
		"sdk",
		"workflow",
		"代码",
		"工程",
		"接口",
		"架构",
		"实现",
		"组件",
	})
}

func todoNeedsComparisonFocus(todo ResearchTodo, plan ResearchTodoPlan) bool {
	return containsAnyDispatchKeyword(todoDispatchText(todo, plan), []string{
		"alternative",
		"compare",
		"comparison",
		"difference",
		"option",
		"tradeoff",
		"versus",
		"vs",
		"差异",
		"对比",
		"方案",
		"权衡",
		"选型",
	})
}

func todoNeedsQuantitativeFocus(todo ResearchTodo, plan ResearchTodoPlan) bool {
	return containsAnyDispatchKeyword(todoDispatchText(todo, plan), []string{
		"adoption",
		"benchmark",
		"cost",
		"latency",
		"metric",
		"performance",
		"price",
		"pricing",
		"throughput",
		"成本",
		"价格",
		"基准",
		"性能",
		"数据",
		"指标",
	})
}

func dependenciesHaveGaps(dependencies []TodoExecution) bool {
	for _, dependency := range dependencies {
		if len(dependency.Gaps) > 0 || strings.TrimSpace(dependency.Error) != "" || dependency.Status != TodoDone {
			return true
		}
	}
	return false
}

func todoDispatchText(todo ResearchTodo, plan ResearchTodoPlan) string {
	return normalizeLintText(strings.Join([]string{
		plan.Objective,
		todo.Title,
		todo.Question,
		strings.Join(todo.SearchQueries, " "),
		strings.Join(todo.AcceptanceCriteria, " "),
	}, " "))
}

func containsAnyDispatchKeyword(text string, keywords []string) bool {
	for _, keyword := range keywords {
		if strings.Contains(text, normalizeLintText(keyword)) {
			return true
		}
	}
	return false
}

// todoToResearchStep 把当前主流程的 ResearchTodo 转换为可复用执行器需要的 ResearchStep。
func todoToResearchStep(todo ResearchTodo, plans ...ResearchTodoPlan) ResearchStep {
	title := strings.TrimSpace(todo.Title)
	if title == "" {
		title = strings.TrimSpace(todo.ID)
	}
	var plan ResearchTodoPlan
	if len(plans) > 0 {
		plan = plans[0]
	}
	return ResearchStep{
		ID:              strings.TrimSpace(todo.ID),
		Title:           title,
		Question:        strings.TrimSpace(todo.Question),
		SearchQueries:   ExpandTodoSearchQueries(todo, plan),
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
