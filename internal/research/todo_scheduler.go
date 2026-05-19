package research

import (
	"context"
	"fmt"
	"strings"
	"sync"
)

type TodoExecutorInput struct {
	Plan                 ResearchTodoPlan
	Todo                 ResearchTodo
	DependencyExecutions []TodoExecution
}

type TodoExecutor func(context.Context, TodoExecutorInput) (TodoExecution, error)

type TodoReplannerInput struct {
	Plan      ResearchTodoPlan
	Completed []TodoExecution
	Failed    TodoExecution
	Blocked   []TodoExecution
}

type TodoReplanner func(context.Context, TodoReplannerInput) (ResearchTodoPlanPatch, error)

type TodoSchedulerConfig struct {
	MaxParallel int
	Executor    TodoExecutor
	Replanner   TodoReplanner
}

type TodoScheduler struct {
	maxParallel int
	executor    TodoExecutor
	replanner   TodoReplanner
}

func NewTodoScheduler(cfg TodoSchedulerConfig) (*TodoScheduler, error) {
	if cfg.Executor == nil {
		return nil, fmt.Errorf("todo executor is required")
	}
	if cfg.MaxParallel <= 0 {
		cfg.MaxParallel = 1
	}
	return &TodoScheduler{maxParallel: cfg.MaxParallel, executor: cfg.Executor, replanner: cfg.Replanner}, nil
}

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

	completed := make(map[string]TodoExecution, len(plan.Todos))
	executions := make([]TodoExecution, 0, len(plan.Todos))
	for len(pending) > 0 {
		if err := ctx.Err(); err != nil {
			return executions, err
		}

		blocked := blockTodosWithTerminalDependencies(plan, pending, completed)
		for _, execution := range blocked {
			completed[execution.Todo.ID] = execution
			executions = append(executions, execution)
		}
		if len(pending) == 0 {
			break
		}

		runnable := runnableTodos(plan, pending, completed, s.maxParallel)
		if len(runnable) == 0 {
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

		for _, todo := range runnable {
			delete(pending, todo.ID)
		}

		batch := s.runBatch(ctx, plan, runnable, completed)
		for _, execution := range batch {
			completed[execution.Todo.ID] = execution
			executions = append(executions, execution)
			if execution.Status == TodoFailed {
				plan = s.replanAfterFailure(ctx, plan, pending, completed, execution)
			}
		}
	}

	return executions, nil
}

func (s *TodoScheduler) runBatch(ctx context.Context, plan ResearchTodoPlan, todos []ResearchTodo, completed map[string]TodoExecution) []TodoExecution {
	results := make([]TodoExecution, len(todos))
	var wg sync.WaitGroup

	for i, todo := range todos {
		wg.Add(1)
		go func(idx int, current ResearchTodo) {
			defer wg.Done()

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

func todoDependenciesDone(todo ResearchTodo, completed map[string]TodoExecution) bool {
	for _, dep := range todo.DependsOn {
		execution, ok := completed[strings.TrimSpace(dep)]
		if !ok || execution.Status != TodoDone {
			return false
		}
	}
	return true
}

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

func dependencyExecutions(todo ResearchTodo, completed map[string]TodoExecution) []TodoExecution {
	out := make([]TodoExecution, 0, len(todo.DependsOn))
	for _, dep := range todo.DependsOn {
		if execution, ok := completed[strings.TrimSpace(dep)]; ok {
			out = append(out, execution)
		}
	}
	return out
}

func remainingTodosInPlanOrder(plan ResearchTodoPlan, pending map[string]ResearchTodo) []ResearchTodo {
	out := make([]ResearchTodo, 0, len(pending))
	for _, todo := range plan.Todos {
		if pendingTodo, ok := pending[strings.TrimSpace(todo.ID)]; ok {
			out = append(out, pendingTodo)
		}
	}
	return out
}

func completedExecutionsInPlanOrder(plan ResearchTodoPlan, completed map[string]TodoExecution) []TodoExecution {
	out := make([]TodoExecution, 0, len(completed))
	for _, todo := range plan.Todos {
		if execution, ok := completed[strings.TrimSpace(todo.ID)]; ok {
			out = append(out, execution)
		}
	}
	return out
}

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
