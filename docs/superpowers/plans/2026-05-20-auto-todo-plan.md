# Auto Todo Plan Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Move the research CLI toward the 2026-05-18 automatic reviewable todo-plan workflow while preserving the existing step-based runner during migration.

**Architecture:** Add the new todo-plan model beside the existing `ResearchPlan`, then introduce execution types, scheduler, planner/execute phases, rendering, and CLI confirmation in small tested slices. The old step-based `planexecute` path remains available until the todo-plan path can replace it as the primary flow.

**Tech Stack:** Go, Eino ADK, standard `testing`, existing `internal/research`, `internal/render`, `cmd/research`, and `internal/search` packages.

---

## File Structure

- `internal/research/todo_plan.go`: new `ResearchTodoPlan`, `ResearchSection`, `ResearchTodo`, validation, and dependency graph helpers.
- `internal/research/todo_plan_test.go`: validation tests for required fields, duplicate IDs, section references, unknown dependencies, and cycles.
- `internal/research/todo_execution.go`: `TodoStatus`, `TodoExecution`, `SectionExecution`, and scheduler-facing execution helpers.
- `internal/research/todo_scheduler.go`: dependency-aware todo scheduler with max parallelism and blocked dependent handling.
- `internal/research/todo_scheduler_test.go`: scheduler ordering, max parallelism, failure, blocking, and replanner-patch tests.
- `internal/research/runner.go`: add `Plan`, `Execute`, and keep `Run` as Plan+Execute convenience once todo planning is wired.
- `internal/research/result.go`: migrate `ResearchResult` to todo-plan fields while preserving old fields only as temporary compatibility if needed.
- `internal/render/markdown.go`: include section-grouped execution summary before sources.
- `cmd/research/main.go`: add `--yes`, `--plan-only`, `--plan-json`, `--max-parallel`, non-interactive guard, and confirmation prompt.

## Task 1: Todo Plan Data Model

**Files:**
- Create: `internal/research/todo_plan.go`
- Create: `internal/research/todo_plan_test.go`

- [ ] **Step 1: Write failing validation tests**

Add tests covering a valid plan, missing objective, duplicate section IDs, duplicate todo IDs, missing section references, missing acceptance criteria, unknown dependencies, and cyclic dependencies.

- [ ] **Step 2: Run tests to verify RED**

Run: `go test ./internal/research -run 'TestResearchTodoPlan' -count=1`

Expected: FAIL because `ResearchTodoPlan` and related types are not defined.

- [ ] **Step 3: Implement minimal model and validation**

Create the three model types and a `Validate() error` method. Keep this file independent from the existing `ResearchPlan` implementation.

- [ ] **Step 4: Run tests to verify GREEN**

Run: `go test ./internal/research -run 'TestResearchTodoPlan' -count=1`

Expected: PASS.

## Task 2: Todo Execution Types

**Files:**
- Create: `internal/research/todo_execution.go`
- Modify: `internal/research/result.go`
- Test: `internal/research/todo_execution_test.go`

- [ ] **Step 1: Write failing result shape tests**

Add tests that construct `TodoExecution`, `SectionExecution`, and a todo-based `ResearchResult`, then JSON round-trip them and assert `plan`, `section_executions`, and `todo_executions` fields exist.

- [ ] **Step 2: Run tests to verify RED**

Run: `go test ./internal/research -run 'TestTodoExecution|TestResearchResultTodo' -count=1`

Expected: FAIL because execution types and result fields are missing.

- [ ] **Step 3: Add execution types and result fields**

Add todo execution types. Preserve old fields temporarily only if existing tests still rely on them.

- [ ] **Step 4: Run package tests**

Run: `go test ./internal/research -count=1`

Expected: PASS.

## Task 3: Dependency Scheduler

**Files:**
- Create: `internal/research/todo_scheduler.go`
- Create: `internal/research/todo_scheduler_test.go`

- [ ] **Step 1: Write failing scheduler tests**

Cover dependency ordering, max parallel limit, independent branch continuation after one failure, dependent todo blocking, and successful replanner patch unblocking work.

- [ ] **Step 2: Run tests to verify RED**

Run: `go test ./internal/research -run 'TestTodoScheduler' -count=1`

Expected: FAIL because `TodoScheduler` is not defined.

- [ ] **Step 3: Implement scheduler core**

Implement a small scheduler that accepts a validated `ResearchTodoPlan`, a max parallel limit, and an executor function. It records one `TodoExecution` per terminal todo state.

- [ ] **Step 4: Run scheduler tests**

Run: `go test ./internal/research -run 'TestTodoScheduler' -count=1`

Expected: PASS.

## Task 4: Runner Plan and Execute Phases

**Files:**
- Modify: `internal/research/runner.go`
- Modify: `internal/research/researchers.go`
- Modify: `internal/research/executor.go`
- Test: `internal/research/runner_test.go`

- [ ] **Step 1: Write failing runner phase tests**

Cover `Plan` returning a validated `ResearchTodoPlan`, `Execute` aggregating todo results by section, and `Run` preserving metadata and errors.

- [ ] **Step 2: Run tests to verify RED**

Run: `go test ./internal/research -run 'TestRunner.*Plan|TestRunner.*Execute|TestRunner.*Run' -count=1`

Expected: FAIL because explicit phases are missing.

- [ ] **Step 3: Implement phase API**

Add `Plan`, `Execute`, and refactor `Run` into `Plan` then `Execute`. Keep old step-based internals available behind compatibility helpers during transition.

- [ ] **Step 4: Run runner tests**

Run: `go test ./internal/research -count=1`

Expected: PASS.

## Task 5: Rendering and CLI Review Flow

**Files:**
- Modify: `internal/render/markdown.go`
- Modify: `internal/render/render_test.go`
- Modify: `cmd/research/main.go`
- Modify: `cmd/research/main_test.go`

- [ ] **Step 1: Write failing rendering and CLI tests**

Cover execution summary rendering, JSON todo status fields, default confirmation prompt, declined confirmation exit, `--yes`, `--plan-only`, and non-interactive guard.

- [ ] **Step 2: Run tests to verify RED**

Run: `go test ./internal/render ./cmd/research -count=1`

Expected: FAIL because the new rendering and flags are missing.

- [ ] **Step 3: Implement rendering and CLI flow**

Add plan preview printing, confirmation prompt, `--yes`, `--plan-only`, `--plan-json`, `--max-parallel`, and stdin interactivity handling.

- [ ] **Step 4: Run full tests**

Run: `go test ./... -count=1`

Expected: PASS.

## Self-Review

- Spec coverage: this plan covers the 2026-05-18 migration strategy in order: model, execution/result shape, scheduler, runner phases, rendering, and CLI confirmation.
- Scope: replanner patch behavior is included at scheduler-test level first; model-backed replanner prompts can be expanded after the scheduler contract is stable.
- Compatibility: the old `ResearchPlan` path remains until the todo-plan runner is proven by tests.
