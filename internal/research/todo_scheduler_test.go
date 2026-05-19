package research

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestTodoSchedulerRunsDependenciesBeforeDependents(t *testing.T) {
	plan := validTodoPlan()
	var order []string
	scheduler, err := NewTodoScheduler(TodoSchedulerConfig{
		MaxParallel: 2,
		Executor: func(_ context.Context, in TodoExecutorInput) (TodoExecution, error) {
			order = append(order, in.Todo.ID)
			if in.Todo.ID == "todo_evidence" && len(in.DependencyExecutions) != 1 {
				t.Fatalf("todo_evidence dependency executions = %d, want 1", len(in.DependencyExecutions))
			}
			return TodoExecution{Todo: in.Todo, Status: TodoDone}, nil
		},
	})
	if err != nil {
		t.Fatalf("NewTodoScheduler() error = %v", err)
	}

	executions, err := scheduler.Run(context.Background(), plan)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	assertTodoStatus(t, executions, "todo_background", TodoDone)
	assertTodoStatus(t, executions, "todo_evidence", TodoDone)
	assertTodoStatus(t, executions, "todo_synthesis", TodoDone)
	assertOrderBefore(t, order, "todo_background", "todo_evidence")
	assertOrderBefore(t, order, "todo_evidence", "todo_synthesis")
}

func TestTodoSchedulerHonorsMaxParallel(t *testing.T) {
	plan := ResearchTodoPlan{
		Objective: "Run independent todos.",
		Sections: []ResearchSection{{
			ID:    "section",
			Title: "Section",
		}},
		Todos: []ResearchTodo{
			schedulerTodo("todo_1", "section"),
			schedulerTodo("todo_2", "section"),
			schedulerTodo("todo_3", "section"),
		},
	}
	var mu sync.Mutex
	active := 0
	maxActive := 0

	scheduler, err := NewTodoScheduler(TodoSchedulerConfig{
		MaxParallel: 1,
		Executor: func(_ context.Context, in TodoExecutorInput) (TodoExecution, error) {
			mu.Lock()
			active++
			if active > maxActive {
				maxActive = active
			}
			mu.Unlock()

			time.Sleep(5 * time.Millisecond)

			mu.Lock()
			active--
			mu.Unlock()
			return TodoExecution{Todo: in.Todo, Status: TodoDone}, nil
		},
	})
	if err != nil {
		t.Fatalf("NewTodoScheduler() error = %v", err)
	}

	if _, err := scheduler.Run(context.Background(), plan); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if maxActive > 1 {
		t.Fatalf("max active executions = %d, want <= 1", maxActive)
	}
}

func TestTodoSchedulerContinuesIndependentBranchesAfterFailure(t *testing.T) {
	plan := ResearchTodoPlan{
		Objective: "Continue independent work.",
		Sections: []ResearchSection{{
			ID:    "section",
			Title: "Section",
		}},
		Todos: []ResearchTodo{
			schedulerTodo("todo_fail", "section"),
			schedulerTodo("todo_independent", "section"),
			{
				ID:                 "todo_dependent",
				SectionID:          "section",
				Title:              "Dependent",
				Question:           "Depends on failed todo.",
				AcceptanceCriteria: []string{"Reports dependency outcome."},
				DependsOn:          []string{"todo_fail"},
			},
		},
	}
	scheduler, err := NewTodoScheduler(TodoSchedulerConfig{
		MaxParallel: 2,
		Executor: func(_ context.Context, in TodoExecutorInput) (TodoExecution, error) {
			if in.Todo.ID == "todo_fail" {
				return TodoExecution{}, errors.New("research failed")
			}
			return TodoExecution{Todo: in.Todo, Status: TodoDone}, nil
		},
	})
	if err != nil {
		t.Fatalf("NewTodoScheduler() error = %v", err)
	}

	executions, err := scheduler.Run(context.Background(), plan)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	assertTodoStatus(t, executions, "todo_fail", TodoFailed)
	assertTodoStatus(t, executions, "todo_independent", TodoDone)
	assertTodoStatus(t, executions, "todo_dependent", TodoBlocked)
}

func TestTodoSchedulerReplannerPatchUnblocksWork(t *testing.T) {
	plan := ResearchTodoPlan{
		Objective: "Recover after failure.",
		Sections: []ResearchSection{{
			ID:    "section",
			Title: "Section",
		}},
		Todos: []ResearchTodo{
			schedulerTodo("todo_fail", "section"),
			{
				ID:                 "todo_dependent",
				SectionID:          "section",
				Title:              "Dependent",
				Question:           "Can this continue after replanning?",
				AcceptanceCriteria: []string{"Runs after dependency patch."},
				DependsOn:          []string{"todo_fail"},
			},
		},
	}
	replanned := false

	scheduler, err := NewTodoScheduler(TodoSchedulerConfig{
		MaxParallel: 1,
		Executor: func(_ context.Context, in TodoExecutorInput) (TodoExecution, error) {
			if in.Todo.ID == "todo_fail" {
				return TodoExecution{}, errors.New("research failed")
			}
			if in.Todo.ID == "todo_dependent" && len(in.DependencyExecutions) != 0 {
				t.Fatalf("todo_dependent dependency executions = %d, want 0 after dependency patch", len(in.DependencyExecutions))
			}
			return TodoExecution{Todo: in.Todo, Status: TodoDone}, nil
		},
		Replanner: func(_ context.Context, in TodoReplannerInput) (ResearchTodoPlanPatch, error) {
			replanned = true
			if in.Failed.Todo.ID != "todo_fail" {
				t.Fatalf("Failed.Todo.ID = %q, want todo_fail", in.Failed.Todo.ID)
			}
			return ResearchTodoPlanPatch{
				UpdateDeps: []TodoDepsPatch{{
					TodoID:    "todo_dependent",
					DependsOn: nil,
				}},
				Explanation: "Continue dependent work without the failed prerequisite.",
			}, nil
		},
	})
	if err != nil {
		t.Fatalf("NewTodoScheduler() error = %v", err)
	}

	executions, err := scheduler.Run(context.Background(), plan)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	if !replanned {
		t.Fatal("replanner was not called")
	}
	assertTodoStatus(t, executions, "todo_fail", TodoFailed)
	assertTodoStatus(t, executions, "todo_dependent", TodoDone)
}

func schedulerTodo(id, sectionID string) ResearchTodo {
	return ResearchTodo{
		ID:                 id,
		SectionID:          sectionID,
		Title:              id,
		Question:           id + "?",
		AcceptanceCriteria: []string{"Done."},
	}
}

func assertTodoStatus(t *testing.T, executions []TodoExecution, todoID string, want TodoStatus) {
	t.Helper()
	for _, execution := range executions {
		if execution.Todo.ID == todoID {
			if execution.Status != want {
				t.Fatalf("todo %s status = %q, want %q", todoID, execution.Status, want)
			}
			return
		}
	}
	t.Fatalf("todo %s not found in executions", todoID)
}

func assertOrderBefore(t *testing.T, order []string, before, after string) {
	t.Helper()
	beforeIndex := -1
	afterIndex := -1
	for i, todoID := range order {
		if todoID == before {
			beforeIndex = i
		}
		if todoID == after {
			afterIndex = i
		}
	}
	if beforeIndex == -1 || afterIndex == -1 {
		t.Fatalf("order %v missing %s or %s", order, before, after)
	}
	if beforeIndex >= afterIndex {
		t.Fatalf("order %v: %s should run before %s", order, before, after)
	}
}
