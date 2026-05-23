package research

import (
	"context"
	"fmt"
	"strings"
)

type StepExecuteFunc func(context.Context, StepExecutionInput) (StepExecution, error)

type TodoResearchLoopInput struct {
	Plan                 ResearchTodoPlan
	Todo                 ResearchTodo
	DependencyExecutions []TodoExecution
	MaxIterations        int
	ExecuteStep          StepExecuteFunc
}

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

func countResearchFindings(results []ResearcherResult) int {
	count := 0
	for _, result := range results {
		count += len(result.Findings)
	}
	return count
}

func mergeGapMessages(existing, next []string) []string {
	merged := make([]string, 0, len(existing)+len(next))
	merged = append(merged, existing...)
	merged = append(merged, next...)
	return dedupeStrings(merged)
}

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
