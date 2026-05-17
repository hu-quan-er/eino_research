# Auto Todo Plan Design

Date: 2026-05-18

## Goal

Add an automatic plan + todo workflow to the research CLI. The model should first generate a reviewable research plan with executable todos. The CLI should ask for confirmation by default, then execute todos as the real unit of work and produce a final report grouped by research sections.

This design prioritizes research quality over implementation cost. A todo plan becomes a first-class model rather than a secondary breakdown of existing steps.

## Non-Goals

- Web UI.
- Persistent storage or resumable runs.
- Human editing of the generated plan inside an interactive editor.
- Full task queue infrastructure.
- Citation verification beyond the existing source collection and normalization.
- Arbitrary user-defined researcher roles in the first implementation.

## Architecture

The current project uses `ResearchPlan` as the plan-execute unit, where each step is executed by three parallel researchers. The new architecture promotes `ResearchTodoPlan` to the primary plan model:

```text
ResearchTodoPlan
  -> Sections: report and display grouping
  -> Todos: actual execution units
  -> Dependencies: todo-to-todo prerequisites
```

Sections organize the final report. Todos drive execution. Dependencies decide which todos are runnable, blocked, or waiting.

The runtime flow becomes:

```text
CLI
  -> Config Loader
  -> Model Factory
  -> Search Provider
  -> Runner.Plan(question)
  -> Plan preview and confirmation
  -> Runner.Execute(question, plan)
      -> Todo scheduler
      -> Per-todo parallel researchers
      -> Todo synthesis
      -> Section aggregation
      -> Final answer synthesis
  -> Markdown / JSON renderer
```

The three existing researcher perspectives remain useful and should continue to run inside each todo:

- `background_researcher`
- `evidence_researcher`
- `counterpoint_researcher`

## Data Model

### Plan

```go
type ResearchTodoPlan struct {
    Objective string            `json:"objective"`
    Sections  []ResearchSection `json:"sections"`
    Todos     []ResearchTodo    `json:"todos"`
}

type ResearchSection struct {
    ID          string `json:"id"`
    Title       string `json:"title"`
    Description string `json:"description,omitempty"`
}

type ResearchTodo struct {
    ID                 string   `json:"id"`
    SectionID          string   `json:"section_id"`
    Title              string   `json:"title"`
    Question           string   `json:"question"`
    SearchQueries      []string `json:"search_queries,omitempty"`
    AcceptanceCriteria []string `json:"acceptance_criteria"`
    DependsOn          []string `json:"depends_on,omitempty"`
}
```

Validation rules:

- `Objective` is required.
- Section IDs are required and unique.
- Todo IDs are required and unique.
- Each todo must reference an existing `section_id`.
- Each todo must have `title`, `question`, and at least one `acceptance_criteria` item.
- `search_queries` may be empty for synthesis-only todos.
- Each `depends_on` entry must reference an existing todo.
- The dependency graph must be acyclic.

### Execution

```go
type TodoStatus string

const (
    TodoPending TodoStatus = "pending"
    TodoRunning TodoStatus = "running"
    TodoDone    TodoStatus = "done"
    TodoFailed  TodoStatus = "failed"
    TodoBlocked TodoStatus = "blocked"
    TodoSkipped TodoStatus = "skipped"
)

type TodoExecution struct {
    Todo              ResearchTodo       `json:"todo"`
    Status            TodoStatus         `json:"status"`
    ResearcherResults []ResearcherResult `json:"researcher_results"`
    Summary           string             `json:"summary"`
    Findings          []Finding          `json:"findings,omitempty"`
    Gaps              []string           `json:"gaps,omitempty"`
    Sources           []search.Source    `json:"sources"`
    Error             string             `json:"error,omitempty"`
}

type SectionExecution struct {
    Section ResearchSection `json:"section"`
    Todos   []TodoExecution `json:"todos"`
    Summary string          `json:"summary"`
}
```

`ResearchResult` should evolve to store both the raw todo execution list and the grouped section view:

```go
type ResearchResult struct {
    Question          string             `json:"question"`
    Answer            Answer             `json:"answer"`
    Plan              ResearchTodoPlan   `json:"plan"`
    SectionExecutions []SectionExecution `json:"section_executions"`
    TodoExecutions    []TodoExecution    `json:"todo_executions"`
    Sources           []search.Source    `json:"sources"`
    Metadata          Metadata           `json:"metadata"`
    Error             *RunError          `json:"error,omitempty"`
}
```

## CLI Behavior

Default behavior is review-before-execute:

```bash
research "research question"
```

The CLI should:

1. Generate a `ResearchTodoPlan`.
2. Print a readable plan preview to stderr.
3. Ask `Continue and execute this plan? [y/N]`.
4. Execute only after confirmation.
5. Print final Markdown or JSON to stdout.

New flags:

```text
--yes              Skip confirmation and execute the generated plan.
--plan-only        Generate and print the plan without executing.
--plan-json        With --plan-only, print ResearchTodoPlan JSON instead of text preview.
--max-parallel     Maximum runnable todos to execute concurrently.
```

Existing flags keep their meanings:

```text
--json             Print full ResearchResult JSON.
--provider         Select search provider.
--max-iterations   Bound planner/replanner loops.
--verbose          Print progress to stderr.
```

If stdin is not interactive and neither `--yes` nor `--plan-only` is set, the CLI should fail with a clear error telling the caller to pass one of those flags.

## Planning

`Runner` should expose explicit phases:

```go
func (r *Runner) Plan(ctx context.Context, question string) (ResearchTodoPlan, error)
func (r *Runner) Execute(ctx context.Context, question string, plan ResearchTodoPlan) (ResearchResult, error)
func (r *Runner) Run(ctx context.Context, question string) (ResearchResult, error)
```

`Run` remains a non-interactive convenience path that performs `Plan` and then `Execute` without prompting. The CLI should call `Plan` and `Execute` separately when it needs to insert the default confirmation prompt between those phases.

The planner prompt should instruct the model to create:

- 3 to 6 sections for report structure.
- A bounded todo list, initially 4 to 10 todos.
- Clear dependencies only when needed.
- Search queries for evidence-gathering todos.
- Empty search queries for synthesis-only todos.
- Acceptance criteria that can be checked from each todo result.

## Todo Scheduling

The scheduler owns status transitions:

```text
pending -> running -> done
pending -> running -> failed
pending -> blocked
pending -> skipped
```

Scheduling loop:

1. Mark all todos `pending`.
2. Find todos whose dependencies are all `done`.
3. Run up to `--max-parallel` runnable todos.
4. For each completed todo, store a `TodoExecution`.
5. If a todo fails, mark direct and transitive dependents `blocked` unless replanning creates an alternate path.
6. Finish when no runnable todo remains.
7. Aggregate todo results into sections and synthesize the final answer.

Each todo gets an independent `web_search` tool instance so search limits and source ID prefixes are per todo.

## Per-Todo Research

The current per-step execution pattern should move to per-todo execution:

```text
ResearchTodo
  -> background_researcher
  -> evidence_researcher
  -> counterpoint_researcher
  -> todo synthesizer
  -> TodoExecution
```

Researcher prompts should include:

- Original user question.
- Full todo plan summary.
- Current todo.
- Completed dependency todo results.
- Assigned focus.

The synthesizer should return a `TodoExecution`. If it returns non-JSON content, the system should preserve that content as the todo summary and keep normalized researcher sources.

## Replanning

First implementation should trigger replanning only after todo failure creates blocked work.

Replanner input:

- Original question.
- Current `ResearchTodoPlan`.
- Completed todo executions.
- Failed todo and error.
- Blocked todos.

Replanner output should be a constrained patch, not an unrestricted replacement:

```go
type ResearchTodoPlanPatch struct {
    AddTodos    []ResearchTodo `json:"add_todos,omitempty"`
    SkipTodos   []string       `json:"skip_todos,omitempty"`
    UpdateDeps  []TodoDepsPatch `json:"update_deps,omitempty"`
    Explanation string         `json:"explanation,omitempty"`
}

type TodoDepsPatch struct {
    TodoID    string   `json:"todo_id"`
    DependsOn []string `json:"depends_on"`
}
```

Patch rules:

- Completed todos cannot be modified.
- Existing todo IDs cannot be reused.
- New todos must reference existing or newly added sections.
- Dependency updates cannot create cycles.
- Skipping a todo requires an explanation in the patch.

If replanning fails, the runner should preserve failed and blocked statuses and produce a partial result when possible.

## Rendering

Markdown output should keep the final answer first, include a compact section-grouped execution summary, then list sources:

```text
## Execution Summary

### Background and Definitions
- done todo_1: Clarify core terms
- done todo_2: Establish timeline

### Evidence and Cases
- failed todo_3: Collect recent evidence
- blocked todo_4: Compare alternatives
```

JSON output should include the full `ResearchResult`, including plan, todo executions, section executions, sources, metadata, and errors.

`--plan-only` text output should print sections and todos with dependencies in a human-reviewable format.

## Error Handling

- Planner validation failure is a planning error and should stop before confirmation.
- User declining confirmation exits without error and without executing.
- Todo failure records a failed `TodoExecution`.
- Dependent pending todos become blocked unless replanning resolves them.
- Independent runnable todos continue after unrelated failures.
- If no todo succeeds, return a run error.
- If some todos succeed and some fail or block, return a partial result with error stage `partial_failure`.
- JSON output should include partial results on failure.

## Testing

Unit tests should cover:

- `ResearchTodoPlan.Validate()`:
  - missing objective
  - duplicate section IDs
  - duplicate todo IDs
  - missing section reference
  - missing acceptance criteria
  - unknown dependency
  - cyclic dependency
- Scheduler:
  - dependency ordering
  - max parallel limit
  - independent branches continue after failure
  - dependent todos become blocked
  - successful replanner patch unblocks work
- Runner:
  - `Plan` returns a validated plan
  - `Execute` aggregates todo results by section
  - `Run` preserves metadata and errors
- CLI:
  - default confirmation prompt
  - decline exits without execution
  - `--yes` skips confirmation
  - `--plan-only` does not execute
  - non-interactive execution requires `--yes` or `--plan-only`
- Rendering:
  - Markdown includes final answer, sources, and execution summary
  - JSON includes full plan and todo statuses
- Search/tool:
  - search limits are per todo
  - source IDs are prefixed per todo and deduplicated globally

## Migration Strategy

The implementation can keep compatibility helpers while moving to the new model:

1. Add `ResearchTodoPlan` and related validation without removing `ResearchPlan`.
2. Add planner support for `ResearchTodoPlan`.
3. Add `TodoScheduler` and per-todo executor.
4. Update result and render models.
5. Update CLI confirmation flow.
6. Retire or adapt old `ResearchPlan` tests once the todo plan path is complete.

The old step-based executor should not remain the primary path after migration. It may be kept temporarily only to reduce transition risk during implementation.
