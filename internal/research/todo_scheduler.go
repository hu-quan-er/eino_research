package research

import (
	"context"
	"fmt"
	"strings"
	"sync"
)

// TodoExecutorInput 是调度器调用单个 todo executor 时传入的上下文。
type TodoExecutorInput struct {
	// Plan 是完整 todo plan，执行器可用它理解全局目标和依赖。
	Plan ResearchTodoPlan
	// Todo 是当前需要执行的 todo。
	Todo ResearchTodo
	// DependencyExecutions 是当前 todo 依赖的已完成结果。
	DependencyExecutions []TodoExecution
}

// TodoExecutor 执行一个已满足依赖的 todo。
type TodoExecutor func(context.Context, TodoExecutorInput) (TodoExecution, error)

// TodoReplannerInput 是 todo 失败后传给 replanner 的上下文。
type TodoReplannerInput struct {
	// Plan 是当前仍在使用的 plan。
	Plan ResearchTodoPlan
	// Completed 是按 plan 顺序排列的已完成或终态 todo。
	Completed []TodoExecution
	// Failed 是触发 replanner 的失败 todo。
	Failed TodoExecution
	// Blocked 是因依赖失败而 blocked 的 todo，当前预留给后续 replanner 使用。
	Blocked []TodoExecution
}

// TodoReplanner 可以在 todo 失败后返回 plan patch，用于跳过、补充或调整后续 todo。
type TodoReplanner func(context.Context, TodoReplannerInput) (ResearchTodoPlanPatch, error)

// TodoSchedulerConfig 控制 todo 调度并发度和可选 replanner。
type TodoSchedulerConfig struct {
	// MaxParallel 是同时执行的 runnable todo 上限。
	MaxParallel int
	// Executor 是实际执行单个 todo 的函数。
	Executor TodoExecutor
	// Replanner 是可选失败恢复策略。
	Replanner TodoReplanner
}

// TodoScheduler 按 ResearchTodoPlan 的依赖图执行 todo。
//
// 它保证依赖先于被依赖 todo 执行；独立分支可以并发；失败依赖会让下游 todo 进入 blocked。
type TodoScheduler struct {
	maxParallel int
	executor    TodoExecutor
	replanner   TodoReplanner
}

// NewTodoScheduler 创建调度器，并为 MaxParallel 填充默认值。
func NewTodoScheduler(cfg TodoSchedulerConfig) (*TodoScheduler, error) {
	if cfg.Executor == nil {
		return nil, fmt.Errorf("todo executor is required")
	}
	if cfg.MaxParallel <= 0 {
		cfg.MaxParallel = 1
	}
	return &TodoScheduler{maxParallel: cfg.MaxParallel, executor: cfg.Executor, replanner: cfg.Replanner}, nil
}

// Run 执行整个 todo plan。
//
// 返回结果按实际完成顺序追加；报告层会再按 plan 顺序重新分组展示。
func (s *TodoScheduler) Run(ctx context.Context, plan ResearchTodoPlan) ([]TodoExecution, error) {
	if s == nil {
		return nil, fmt.Errorf("todo scheduler is nil")
	}
	if s.executor == nil {
		return nil, fmt.Errorf("todo executor is required")
	}
	if err := plan.Validate(); err != nil {
		return nil, err
	}

	pending := make(map[string]ResearchTodo, len(plan.Todos))
	for _, todo := range plan.Todos {
		pending[strings.TrimSpace(todo.ID)] = todo
	}

	// completed 既保存成功结果，也保存 failed/blocked/skipped 等终态，方便依赖判断。
	completed := make(map[string]TodoExecution, len(plan.Todos))
	executions := make([]TodoExecution, 0, len(plan.Todos))
	for len(pending) > 0 {
		if err := ctx.Err(); err != nil {
			return executions, err
		}

		// 先传播终态失败依赖，避免下游 todo 永远留在 pending 中。
		blocked := blockTodosWithTerminalDependencies(plan, pending, completed)
		for _, execution := range blocked {
			completed[execution.Todo.ID] = execution
			executions = append(executions, execution)
		}
		if len(pending) == 0 {
			break
		}

		// 每轮只取当前依赖已满足的一批 todo，并受 maxParallel 控制。
		runnable := runnableTodos(plan, pending, completed, s.maxParallel)
		if len(runnable) == 0 {
			// 走到这里说明依赖图当前无法继续推进，剩余 todo 统一标记 blocked。
			for _, todo := range remainingTodosInPlanOrder(plan, pending) {
				delete(pending, todo.ID)
				execution := TodoExecution{
					Todo:   todo,
					Status: TodoBlocked,
					Error:  "todo dependencies did not complete",
				}
				completed[todo.ID] = execution
				executions = append(executions, execution)
			}
			break
		}

		// runnable 从 pending 删除后再执行，避免并发批次内重复调度。
		for _, todo := range runnable {
			delete(pending, todo.ID)
		}

		batch := s.runBatch(ctx, plan, runnable, completed)
		for _, execution := range batch {
			completed[execution.Todo.ID] = execution
			executions = append(executions, execution)
			if execution.Status == TodoFailed {
				// 失败后允许 replanner 修补未完成部分；非法 patch 会被忽略并保持原 plan。
				plan = s.replanAfterFailure(ctx, plan, pending, completed, execution)
			}
		}
	}

	return executions, nil
}

// runBatch 并发执行一批当前 runnable 的 todos。
func (s *TodoScheduler) runBatch(ctx context.Context, plan ResearchTodoPlan, todos []ResearchTodo, completed map[string]TodoExecution) []TodoExecution {
	results := make([]TodoExecution, len(todos))
	var wg sync.WaitGroup

	for i, todo := range todos {
		wg.Add(1)
		go func(idx int, current ResearchTodo) {
			defer wg.Done()

			// 每个 todo 只看到自己直接依赖的 execution，避免 prompt 被无关分支污染。
			execution, err := s.executor(ctx, TodoExecutorInput{
				Plan:                 plan,
				Todo:                 current,
				DependencyExecutions: dependencyExecutions(current, completed),
			})
			if err != nil {
				results[idx] = TodoExecution{
					Todo:   current,
					Status: TodoFailed,
					Error:  err.Error(),
				}
				return
			}
			if strings.TrimSpace(execution.Todo.ID) == "" {
				execution.Todo = current
			}
			if execution.Status == "" {
				execution.Status = TodoDone
			}
			results[idx] = execution
		}(i, todo)
	}

	wg.Wait()
	return results
}

// replanAfterFailure 在 todo 失败后尝试应用 replanner patch。
//
// replanner 失败或 patch 非法时会保留原 plan，避免错误恢复逻辑进一步破坏执行状态。
func (s *TodoScheduler) replanAfterFailure(ctx context.Context, plan ResearchTodoPlan, pending map[string]ResearchTodo, completed map[string]TodoExecution, failed TodoExecution) ResearchTodoPlan {
	if s.replanner == nil {
		return plan
	}

	patch, err := s.replanner(ctx, TodoReplannerInput{
		Plan:      plan,
		Completed: completedExecutionsInPlanOrder(plan, completed),
		Failed:    failed,
	})
	if err != nil {
		return plan
	}

	patched, err := applyResearchTodoPlanPatch(plan, patch, completed)
	if err != nil {
		return plan
	}

	syncPendingWithPlan(pending, patched, completed)
	return patched
}

// runnableTodos 按 plan 顺序选择依赖已完成的 pending todo，并受 limit 限制。
func runnableTodos(plan ResearchTodoPlan, pending map[string]ResearchTodo, completed map[string]TodoExecution, limit int) []ResearchTodo {
	out := make([]ResearchTodo, 0, limit)
	for _, todo := range plan.Todos {
		id := strings.TrimSpace(todo.ID)
		pendingTodo, ok := pending[id]
		if !ok {
			continue
		}
		if todoDependenciesDone(pendingTodo, completed) {
			out = append(out, pendingTodo)
			if len(out) == limit {
				return out
			}
		}
	}
	return out
}

// blockTodosWithTerminalDependencies 递归标记依赖失败/跳过/blocked 的下游 todo。
func blockTodosWithTerminalDependencies(plan ResearchTodoPlan, pending map[string]ResearchTodo, completed map[string]TodoExecution) []TodoExecution {
	var blocked []TodoExecution
	for {
		changed := false
		for _, todo := range plan.Todos {
			id := strings.TrimSpace(todo.ID)
			pendingTodo, ok := pending[id]
			if !ok {
				continue
			}
			if todoHasTerminalFailedDependency(pendingTodo, completed) {
				delete(pending, id)
				execution := TodoExecution{
					Todo:   pendingTodo,
					Status: TodoBlocked,
					Error:  "todo blocked by failed dependency",
				}
				completed[id] = execution
				blocked = append(blocked, execution)
				changed = true
			}
		}
		if !changed {
			return blocked
		}
	}
}

// todoDependenciesDone 判断 todo 的所有依赖是否都已成功完成。
func todoDependenciesDone(todo ResearchTodo, completed map[string]TodoExecution) bool {
	for _, dep := range todo.DependsOn {
		execution, ok := completed[strings.TrimSpace(dep)]
		if !ok || execution.Status != TodoDone {
			return false
		}
	}
	return true
}

// todoHasTerminalFailedDependency 判断 todo 是否存在不可恢复的失败依赖。
func todoHasTerminalFailedDependency(todo ResearchTodo, completed map[string]TodoExecution) bool {
	for _, dep := range todo.DependsOn {
		execution, ok := completed[strings.TrimSpace(dep)]
		if !ok {
			continue
		}
		switch execution.Status {
		case TodoFailed, TodoBlocked, TodoSkipped:
			return true
		}
	}
	return false
}

// dependencyExecutions 按 todo.DependsOn 顺序取出已完成的依赖结果。
func dependencyExecutions(todo ResearchTodo, completed map[string]TodoExecution) []TodoExecution {
	out := make([]TodoExecution, 0, len(todo.DependsOn))
	for _, dep := range todo.DependsOn {
		if execution, ok := completed[strings.TrimSpace(dep)]; ok {
			out = append(out, execution)
		}
	}
	return out
}

// remainingTodosInPlanOrder 按 plan 顺序返回仍在 pending 中的 todo。
func remainingTodosInPlanOrder(plan ResearchTodoPlan, pending map[string]ResearchTodo) []ResearchTodo {
	out := make([]ResearchTodo, 0, len(pending))
	for _, todo := range plan.Todos {
		if pendingTodo, ok := pending[strings.TrimSpace(todo.ID)]; ok {
			out = append(out, pendingTodo)
		}
	}
	return out
}

// completedExecutionsInPlanOrder 按 plan 顺序返回 completed execution，供 replanner 观察当前进展。
func completedExecutionsInPlanOrder(plan ResearchTodoPlan, completed map[string]TodoExecution) []TodoExecution {
	out := make([]TodoExecution, 0, len(completed))
	for _, todo := range plan.Todos {
		if execution, ok := completed[strings.TrimSpace(todo.ID)]; ok {
			out = append(out, execution)
		}
	}
	return out
}

// syncPendingWithPlan 在 replanner patch 后同步 pending 集合。
//
// 已完成 todo 不会重新进入 pending；新增 todo 会被加入，已跳过或被删除的 todo 会移除。
func syncPendingWithPlan(pending map[string]ResearchTodo, plan ResearchTodoPlan, completed map[string]TodoExecution) {
	planTodos := make(map[string]ResearchTodo, len(plan.Todos))
	for _, todo := range plan.Todos {
		planTodos[strings.TrimSpace(todo.ID)] = todo
	}

	for todoID := range pending {
		todo, ok := planTodos[todoID]
		if !ok {
			delete(pending, todoID)
			continue
		}
		pending[todoID] = todo
	}

	for todoID, todo := range planTodos {
		if _, ok := completed[todoID]; ok {
			continue
		}
		if _, ok := pending[todoID]; ok {
			continue
		}
		pending[todoID] = todo
	}
}
