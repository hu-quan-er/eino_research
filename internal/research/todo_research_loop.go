package research

import (
	"context"
	"fmt"
	"strings"
)

// StepExecuteFunc 是 todo research loop 每一轮实际执行 step 的函数。
type StepExecuteFunc func(context.Context, StepExecutionInput) (StepExecution, error)

// TodoResearchLoopInput 描述一个 todo 内部 bounded research loop 的输入。
type TodoResearchLoopInput struct {
	Plan                 ResearchTodoPlan
	Todo                 ResearchTodo
	DependencyExecutions []TodoExecution
	MaxIterations        int
	ExecuteStep          StepExecuteFunc
}

// runTodoResearchLoop 对单个 todo 做有限轮深挖。
//
// 每轮执行后会用确定性规则检查 gap；如果仍有 gap 且没到上限，会把上一轮结果作为 prior
// executed step 传给下一轮，促使 researcher 针对缺口继续补证据。
func runTodoResearchLoop(ctx context.Context, in TodoResearchLoopInput) (StepExecution, error) {
	if in.ExecuteStep == nil {
		return StepExecution{}, fmt.Errorf("execute step function is required")
	}
	maxIterations := in.MaxIterations
	if maxIterations <= 0 {
		maxIterations = 1
	}

	baseSteps := dependencyExecutionsAsSteps(in.DependencyExecutions)
	attempts := make([]StepExecution, 0, maxIterations)
	step := todoToResearchStep(in.Todo)
	var last StepExecution

	for attempt := 1; attempt <= maxIterations; attempt++ {
		if err := ctx.Err(); err != nil {
			return StepExecution{}, err
		}

		executedSteps := append([]StepExecution{}, baseSteps...)
		executedSteps = append(executedSteps, attempts...)
		execution, err := in.ExecuteStep(ctx, StepExecutionInput{
			Question:      in.Plan.Objective,
			Step:          step,
			ExecutedSteps: executedSteps,
		})
		if err != nil {
			return StepExecution{}, err
		}

		execution = normalizeStepExecutionSources(execution)
		gaps := todoResearchGaps(in.Plan, in.Todo, execution)
		last = execution
		if len(gaps) == 0 {
			return execution, nil
		}

		last.Gaps = mergeGapMessages(last.Gaps, gaps)
		attempts = append(attempts, last)
	}

	return last, nil
}

// todoResearchGaps 使用确定性规则识别 todo 结果是否还缺少基本研究要素。
//
// 这里不调用模型 judge，目的是保持预算可控、测试稳定；后续可在外层增加可选模型 judge。
func todoResearchGaps(plan ResearchTodoPlan, todo ResearchTodo, execution StepExecution) []string {
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
