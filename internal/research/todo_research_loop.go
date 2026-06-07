package research

import (
	"context"
	"fmt"
	"strings"
)

// StepExecuteFunc 是 todo research loop 每一轮实际执行 step 的函数。
type StepExecuteFunc func(context.Context, StepExecutionInput) (TodoExecution, error)

// TodoResearchLoopInput 描述一个 todo 内部 bounded research loop 的输入。
type TodoResearchLoopInput struct {
	// Plan 提供全局 objective 和 lint/gap 判断所需上下文。
	Plan ResearchTodoPlan
	// Todo 是当前需要深挖的任务。
	Todo ResearchTodo
	// DependencyExecutions 是当前 todo 的已完成依赖。
	DependencyExecutions []TodoExecution
	// MaxAttempts 是最多深挖轮数，<=0 时按 1 轮处理。
	MaxAttempts int
	// ExecuteStep 是每一轮真正执行 researcher+synthesis 的函数。
	ExecuteStep StepExecuteFunc
}

// runTodoResearchLoop 对单个 todo 做有限轮深挖。
//
// 每轮执行后会用确定性规则检查 gap；如果仍有 gap 且没到上限，会把上一轮结果作为 prior
// executed step 传给下一轮，促使 researcher 针对缺口继续补证据。
func runTodoResearchLoop(ctx context.Context, in TodoResearchLoopInput) (TodoExecution, error) {
	if in.ExecuteStep == nil {
		return TodoExecution{}, fmt.Errorf("execute step function is required")
	}
	maxAttempts := in.MaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = 1
	}

	baseViews := dependencyResearchViews(in.DependencyExecutions)
	attempts := make([]TodoExecution, 0, maxAttempts)
	step := todoToResearchStep(in.Todo)
	var last TodoExecution

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return TodoExecution{}, err
		}
		if attempt > 1 {
			// 第 1 轮是常规执行；只有进入第 2 轮起才算 gap 驱动的 retry。
			eventBusFromContext(ctx).Emit(ctx, Event{
				Kind:    EventGapRetry,
				RunID:   runIDFromContext(ctx),
				TodoID:  in.Todo.ID,
				Attempt: attempt,
			})
		}

		// 每一轮都把“依赖结果 + 前几轮尝试结果”作为上下文，帮助模型针对 gap 补充研究。
		executedViews := append([]priorResearchView{}, baseViews...)
		for _, prev := range attempts {
			executedViews = append(executedViews, attemptResearchView(prev, step))
		}
		execution, err := in.ExecuteStep(ctx, StepExecutionInput{
			Question:      in.Plan.Objective,
			Step:          step,
			ExecutedSteps: executedViews,
		})
		if err != nil {
			return TodoExecution{}, err
		}

		execution = normalizeTodoExecutionSources(execution)
		gaps := todoResearchGaps(in.Plan, in.Todo, execution)
		last = execution
		if len(gaps) == 0 {
			return execution, nil
		}

		// 保留 gap 信息进入下一轮；如果已经到上限，也会把最后一轮 gap 返回给调用方。
		last.Gaps = mergeGapMessages(last.Gaps, gaps)
		attempts = append(attempts, last)
	}

	return last, nil
}

// todoResearchGaps 使用确定性规则识别 todo 结果是否还缺少基本研究要素。
//
// 这里不调用模型 judge，目的是保持预算可控、测试稳定；后续可在外层增加可选模型 judge。
func todoResearchGaps(plan ResearchTodoPlan, todo ResearchTodo, execution TodoExecution) []string {
	gaps := make([]string, 0)
	gaps = append(gaps, nonEmptyStrings(execution.Gaps)...)

	if strings.TrimSpace(execution.Summary) == "" {
		gaps = append(gaps, "todo synthesis summary is empty")
	}
	if len(execution.ResearcherResults) == 0 {
		gaps = append(gaps, "no researcher results returned")
	}
	if countResearchFindings(execution.ResearcherResults) == 0 && len(execution.Sources) == 0 {
		gaps = append(gaps, "no findings or sources returned")
	}
	// 对证据型 todo，没有 sources 基本意味着最终报告无法给出可核验 citation。
	if requiresSearchQueries(plan, todo) && len(execution.Sources) == 0 {
		gaps = append(gaps, "evidence-oriented todo returned no sources")
	}

	return dedupeStrings(gaps)
}

// countResearchFindings 统计所有 researcher 返回的 finding 数量。
func countResearchFindings(results []ResearcherResult) int {
	count := 0
	for _, result := range results {
		count += len(result.Findings)
	}
	return count
}

// mergeGapMessages 合并多轮 gap 信息并去重。
func mergeGapMessages(existing, next []string) []string {
	merged := make([]string, 0, len(existing)+len(next))
	merged = append(merged, existing...)
	merged = append(merged, next...)
	return dedupeStrings(merged)
}

// dedupeStrings 对字符串切片去空白、去重并保持第一次出现的顺序。
func dedupeStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}
