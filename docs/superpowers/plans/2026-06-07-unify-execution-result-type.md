# Unify Execution Result Type Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让 `TodoExecution` 成为唯一的执行结果类型，删除 `StepExecution` 及其转换函数，同时保持 researcher / synthesizer 的 prompt 字节级不变。

**Architecture:** 新增 prompt-only 投影 DTO `priorResearchView`（JSON 形状与原 `StepExecution` 逐字一致），把 step 级组件（`Synthesizer` / `ParallelStepExecutor` / `runTodoResearchLoop`）的产出统一为 `TodoExecution`（渐进填充：`Todo/Status/Findings` 由编排层 finalize 补齐）。Go 按包整体编译（含 `_test.go`），因此核心类型替换必须作为一次连贯改动落地。

**Tech Stack:** Go、`encoding/json`、`cloudwego/eino`、现有测试套件作回归安全网。

参考 spec：`docs/superpowers/specs/2026-06-07-unify-execution-result-type-design.md`

---

## File Structure

**Modify:**
- `internal/research/result.go` — 删除 `StepExecution`，新增 `priorResearchView`
- `internal/research/executor.go` — `Synthesizer` 接口、`ParallelStepExecutor`、`ResearcherInput/SynthesisInput/StepExecutionInput.ExecutedSteps` 类型、返回类型
- `internal/research/researchers.go` — `AgentSynthesizer.Synthesize` 返回类型与 body、`normalizeStepExecutionSources → normalizeTodoExecutionSources`
- `internal/research/todo_research_loop.go` — `runTodoResearchLoop` / `StepExecuteFunc` / `todoResearchGaps` 类型、prior-context view 构建
- `internal/research/todo_dispatcher.go` — `dependencyExecutionsAsSteps → dependencyResearchViews`、新增 `attemptResearchView`、`stepExecutionToTodoExecution → finalizeTodoExecution`
- `internal/research/todo_executor.go` — 闭包返回类型、末尾改调用 `finalizeTodoExecution`

**Test:**
- `internal/research/prior_research_view_test.go` — 新建，锁定 `priorResearchView` JSON 形状
- `internal/research/executor_test.go`、`todo_research_loop_test.go`、`evidence_test.go`、`todo_dispatcher_test.go` — 类型改名更新

---

## Task 1: 新增 priorResearchView 投影类型

**Files:**
- Modify: `internal/research/result.go`
- Test: `internal/research/prior_research_view_test.go`

- [ ] **Step 1: 写锁定 JSON 形状的失败测试**

Create `internal/research/prior_research_view_test.go`:

```go
package research

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/hu-quan-er/eino_research/internal/search"
)

func TestPriorResearchViewSerializationShape(t *testing.T) {
	// dependency 风格：无 documents → 该 key 必须被 omitempty 省略
	depView := priorResearchView{
		Step:    ResearchStep{ID: "todo_1", Question: "Q?"},
		Summary: "dep summary",
		Sources: []search.Source{{ID: "src_1"}},
	}
	data, err := json.Marshal(depView)
	if err != nil {
		t.Fatalf("marshal dep view: %v", err)
	}
	s := string(data)
	for _, want := range []string{`"step"`, `"researcher_results"`, `"summary"`, `"sources"`} {
		if !strings.Contains(s, want) {
			t.Errorf("dep view missing %s: %s", want, s)
		}
	}
	if strings.Contains(s, `"documents"`) {
		t.Errorf("dep view must omit documents when empty: %s", s)
	}

	// attempt 风格：有 documents → 该 key 必须出现
	attemptView := priorResearchView{
		Step:      ResearchStep{ID: "todo_1", Question: "Q?"},
		Summary:   "attempt summary",
		Sources:   []search.Source{{ID: "src_1"}},
		Documents: []SourceDocument{{ID: "src_1_doc", SourceID: "src_1", URL: "https://example.com"}},
	}
	data, err = json.Marshal(attemptView)
	if err != nil {
		t.Fatalf("marshal attempt view: %v", err)
	}
	if !strings.Contains(string(data), `"documents"`) {
		t.Errorf("attempt view should include documents: %s", data)
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/research/ -run TestPriorResearchViewSerializationShape -v`
Expected: 编译错误，`priorResearchView` 未定义。

- [ ] **Step 3: 在 result.go 新增 priorResearchView**

在 `internal/research/result.go` 中 `StepExecution` 类型定义之后（暂与其并存）追加：

```go
// priorResearchView 是 prior-context 喂给 researcher / synthesizer prompt 的精简投影。
//
// 它故意保持与历史 StepExecution 相同的 JSON 形状（step / researcher_results / summary /
// gaps / sources / documents），以确保统一执行结果类型后 prompt 内容字节级不变。
type priorResearchView struct {
	// Step 是该 prior 单元对应的 researcher 简报。
	Step ResearchStep `json:"step"`
	// ResearcherResults 是该 prior 单元的 researcher 原始输出。
	ResearcherResults []ResearcherResult `json:"researcher_results"`
	// Summary 是该 prior 单元的综合摘要。
	Summary string `json:"summary"`
	// Gaps 是该 prior 单元仍未解决的问题。
	Gaps []string `json:"gaps,omitempty"`
	// Sources 是该 prior 单元的来源列表。
	Sources []search.Source `json:"sources"`
	// Documents 是该 prior 单元的可引用正文（dependency 投影不含，attempt 投影含）。
	Documents []SourceDocument `json:"documents,omitempty"`
}
```

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/research/ -run TestPriorResearchViewSerializationShape -v`
Expected: PASS。

- [ ] **Step 5: 全量回归（确认未破坏现有编译/测试）**

Run: `go test ./...`
Expected: 全部 ok（`priorResearchView` 暂未被使用，Go 允许未使用类型）。

- [ ] **Step 6: 提交**

```bash
git add internal/research/result.go internal/research/prior_research_view_test.go
git commit -m "feat: add priorResearchView projection type for prior-context prompts"
```

---

## Task 2: 统一执行结果类型为 TodoExecution

> **重要：** 本任务是一次连贯的类型替换。Step 1–9 修改生产代码与测试，期间包内**不会编译通过**；只在 Step 10 做一次整体验证。请按顺序完成全部编辑后再运行测试。

**Files:**
- Modify: `internal/research/result.go`、`executor.go`、`researchers.go`、`todo_research_loop.go`、`todo_dispatcher.go`、`todo_executor.go`
- Test: `internal/research/executor_test.go`、`todo_research_loop_test.go`、`evidence_test.go`、`todo_dispatcher_test.go`

- [ ] **Step 1: 删除 result.go 中的 StepExecution**

删除 `internal/research/result.go` 中整段 `StepExecution` 定义及其注释：

```go
// StepExecution 表示一个 ResearchStep 被多个 researcher 执行并综合后的结果。
//
// 它是 todo 内部 bounded loop 的中间结构，后续会被转换为 TodoExecution。
type StepExecution struct {
	// Step 是本轮执行的研究步骤或由 todo 转换来的步骤。
	Step ResearchStep `json:"step"`
	// ResearcherResults 保留每个 researcher 的原始输出。
	ResearcherResults []ResearcherResult `json:"researcher_results"`
	// Summary 是 synthesizer 对本 step 的综合结论。
	Summary string `json:"summary"`
	// Gaps 记录仍未解决的问题或证据缺口，bounded loop 会用它判断是否继续深挖。
	Gaps []string `json:"gaps,omitempty"`
	// Sources 是本 step 综合后的来源列表。
	Sources []search.Source `json:"sources"`
	// Documents 是本 step 可引用的文本证据。
	Documents []SourceDocument `json:"documents,omitempty"`
}
```

（`priorResearchView` 保留。）

- [ ] **Step 2: 修改 executor.go 的接口与类型**

(a) `Synthesizer` 接口（约 line 29-32）：

```go
// Synthesizer 负责把多个 researcher 的结果综合为一个 TodoExecution。
type Synthesizer interface {
	Synthesize(ctx context.Context, in SynthesisInput) (TodoExecution, error)
}
```

(b) `ResearcherInput.ExecutedSteps`、`SynthesisInput.ExecutedSteps`、`StepExecutionInput.ExecutedSteps` 三处字段类型 `[]StepExecution` → `[]priorResearchView`（仅改类型，注释保留）。

(c) `ParallelStepExecutor` 的 synthesizer 字段注释（约 line 73）：

```go
	// synthesizer 负责把 researchers 的输出合并为 TodoExecution。
```

(d) `ExecuteStep` 方法签名与全部错误返回：

```go
func (e *ParallelStepExecutor) ExecuteStep(ctx context.Context, in StepExecutionInput) (TodoExecution, error) {
```

把方法体内所有 `return StepExecution{}, ...` 改为 `return TodoExecution{}, ...`（共 4 处：nil executor、nil synthesizer、nil researcher、allResearchersFailed）。最后的 `return e.synthesizer.Synthesize(ctx, SynthesisInput{...})` 不变（现在返回 `TodoExecution`）。

- [ ] **Step 3: 修改 researchers.go 的 Synthesize 与 normalize**

(a) `AgentSynthesizer.Synthesize`（约 line 164-224）：

- 注释（line 164、177）"合并为 StepExecution" / "回退为一个最小 StepExecution" → "合并为 TodoExecution" / "回退为一个最小 TodoExecution"。
- 签名：`func (s *AgentSynthesizer) Synthesize(ctx context.Context, in SynthesisInput) (TodoExecution, error)`。
- 所有 `return StepExecution{}, ...`（model nil、marshal err、generate err、resp nil）→ `return TodoExecution{}, ...`。
- 非 JSON 兜底块改为：

```go
	var out TodoExecution
	if err := json.Unmarshal([]byte(content), &out); err != nil {
		// synthesizer 偶发返回非 JSON 时，保留原文作为 summary，并继续向上游传递 researcher 证据。
		return TodoExecution{
			ResearcherResults: normalizedResults,
			Summary:           content,
			Sources:           researcherSources,
		}, nil
	}
```

- 删除以下回填块（TodoExecution 无 Step 字段；模型返回的 `step` 在 unmarshal 时自然被忽略）：

```go
	if strings.TrimSpace(out.Step.Question) == "" && strings.TrimSpace(out.Step.Title) == "" {
		// 模型可能省略 step 字段；保留输入 step 让后续 todo 转换仍能定位来源任务。
		out.Step = in.Step
	}
```

- 其余逻辑（`len(out.ResearcherResults) == 0` 补齐、`out.Sources = mergeSources(...)`、后续 return）保持不变。
- **system prompt 文本保持不变**（仍为 `You synthesize parallel researcher outputs into one StepExecution. ...`）。

(b) `normalizeStepExecutionSources` → `normalizeTodoExecutionSources`（约 line 228-247），仅改函数名与类型，body 不变：

```go
// normalizeTodoExecutionSources 是 todo 结果进入上层前的统一证据归一化入口。
//
// 它会合并 researcher sources、生成 documents、重写 finding source IDs，并为缺失的
// evidence_refs 自动补齐 quote/chunk。
func normalizeTodoExecutionSources(execution TodoExecution) TodoExecution {
	results, researcherSources := normalizeResearcherSources(execution.ResearcherResults)
	sources := mergeSources(researcherSources, execution.Sources)
	documents := mergeSourceDocuments(execution.Documents, buildSourceDocuments(sources, defaultSourceChunkChars))
	results = enrichResearcherEvidence(results, documents)
	execution.ResearcherResults = results
	execution.Sources = sources
	execution.Documents = documents
	return execution
}
```

- [ ] **Step 4: 修改 todo_research_loop.go**

(a) `StepExecuteFunc`：

```go
// StepExecuteFunc 是 todo research loop 每一轮实际执行 step 的函数。
type StepExecuteFunc func(context.Context, StepExecutionInput) (TodoExecution, error)
```

(b) `runTodoResearchLoop` 重写返回类型与 prior-context 构建：

```go
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

		// 每一轮都把"依赖结果 + 前几轮尝试结果"作为上下文，帮助模型针对 gap 补充研究。
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
```

(c) `todoResearchGaps` 签名类型 `execution StepExecution` → `execution TodoExecution`（body 不变）：

```go
func todoResearchGaps(plan ResearchTodoPlan, todo ResearchTodo, execution TodoExecution) []string {
```

> 说明：`attemptResearchView(prev, step)` 用循环计算的 `step`（不带 plan）填充 view 的 `Step`，与重构前 `last.Step`（synthesizer 回填的同一个 `step`）在常规路径下逐字一致。`TodoExecution` 不再携带 `Step`，因此模型在极端情况下虚构的 step 不会再进入 prior-context——这是更规范的行为，且仅影响"模型返回偏离 step"的边角场景。

- [ ] **Step 5: 修改 todo_dispatcher.go —— 投影 helper**

把 `dependencyExecutionsAsSteps`（约 line 367-387）替换为 `dependencyResearchViews` 并新增 `attemptResearchView`：

```go
// dependencyResearchViews 把已完成依赖投影为 prior-context view，供当前 todo researcher
// 读取上下文。故意不带 Documents，保持与历史 prompt 字节一致。
func dependencyResearchViews(executions []TodoExecution) []priorResearchView {
	views := make([]priorResearchView, 0, len(executions))
	for _, execution := range executions {
		views = append(views, priorResearchView{
			Step:              todoToResearchStep(execution.Todo),
			ResearcherResults: execution.ResearcherResults,
			Summary:           execution.Summary,
			Gaps:              execution.Gaps,
			Sources:           execution.Sources,
		})
	}
	return views
}

// attemptResearchView 把循环内一轮 attempt 投影为 prior-context view（含 documents）。
func attemptResearchView(execution TodoExecution, step ResearchStep) priorResearchView {
	return priorResearchView{
		Step:              step,
		ResearcherResults: execution.ResearcherResults,
		Summary:           execution.Summary,
		Gaps:              execution.Gaps,
		Sources:           execution.Sources,
		Documents:         execution.Documents,
	}
}
```

- [ ] **Step 6: 修改 todo_dispatcher.go —— finalize 替换转换**

把 `stepExecutionToTodoExecution`（约 line 384-415）替换为 `finalizeTodoExecution`：

```go
// finalizeTodoExecution 把循环产出的研究结果补齐为完整 TodoExecution。
//
// 它再次运行 evidence 归一化（normalize 幂等），flatten + enrich findings，并补齐
// Todo / Status / summary 兜底，使最终 TodoExecution.Findings 已带可渲染的证据引用。
func finalizeTodoExecution(todo ResearchTodo, execution TodoExecution) TodoExecution {
	execution = normalizeTodoExecutionSources(execution)
	findings := make([]Finding, 0)
	for _, result := range execution.ResearcherResults {
		findings = append(findings, result.Findings...)
	}
	execution.Findings = enrichFindingsEvidence(findings, execution.Documents)

	summary := strings.TrimSpace(execution.Summary)
	if summary == "" {
		summary = strings.TrimSpace(todo.Title)
	}
	execution.Summary = summary
	execution.Todo = todo
	execution.Status = TodoDone
	return execution
}
```

- [ ] **Step 7: 修改 todo_executor.go**

(a) 文档注释（约 line 53）"最后把 StepExecution 转回 TodoExecution" → "最后 finalize 为完整 TodoExecution"。

(b) `ExecuteStep` 闭包签名与错误返回：

```go
		ExecuteStep: func(ctx context.Context, input StepExecutionInput) (TodoExecution, error) {
```

把闭包内 `return StepExecution{}, err` → `return TodoExecution{}, err`。闭包内 `execution.Documents = mergeSourceDocuments(...)` 与 `return execution, nil` 不变（`execution` 现在是 `TodoExecution`，仍有 `Documents` / `Sources` 字段）。

(c) 函数末尾：

```go
	return finalizeTodoExecution(in.Todo, execution), nil
```

- [ ] **Step 8: 更新 executor_test.go 与 todo_research_loop_test.go**

(a) `executor_test.go` 的 `fakeSynthesizer`：

```go
func (s fakeSynthesizer) Synthesize(ctx context.Context, in SynthesisInput) (TodoExecution, error) {
	return TodoExecution{
		ResearcherResults: in.Results,
		Summary:           "combined",
		Sources:           []search.Source{{ID: "src_1", Title: "combined", URL: "https://example.com/combined"}},
	}, nil
}
```

（删除 `Step: in.Step`；其余测试体不变，`StepExecutionInput{...}` 输入类型保持。）

(b) `todo_research_loop_test.go` 三处 `ExecuteStep` 闭包：签名 `(StepExecutionInput) (TodoExecution, error)`，返回 `TodoExecution{...}` 并删除 `Step: in.Step` 字段。例如第一处（line 25-55 区域）：

```go
		ExecuteStep: func(_ context.Context, in StepExecutionInput) (TodoExecution, error) {
			calls++
			if calls == 1 {
				if len(in.ExecutedSteps) != 1 {
					t.Fatalf("first iteration executed steps = %d, want dependency context only", len(in.ExecutedSteps))
				}
				return TodoExecution{
					Summary: "partial",
					Gaps:    []string{"missing source-backed evidence"},
				}, nil
			}
			if len(in.ExecutedSteps) != 2 {
				t.Fatalf("second iteration executed steps = %d, want dependency plus first attempt", len(in.ExecutedSteps))
			}
			return TodoExecution{
				Summary: "complete",
				ResearcherResults: []ResearcherResult{{
					Role: "evidence_researcher",
					Findings: []Finding{{
						Claim:     "source-backed claim",
						SourceIDs: []string{"src_1"},
					}},
				}},
				Sources: []search.Source{{
					ID:    "src_1",
					Title: "Evidence",
					URL:   "https://example.com/evidence",
				}},
			}, nil
```

其余两处闭包（line 81、109 区域）同样把 `func(...) (StepExecution, error)` → `(TodoExecution, error)`、`return StepExecution{Step: in.Step, ...}` → `return TodoExecution{...}`（删除 `Step` 字段，保留其它字段与断言）。

- [ ] **Step 9: 更新 evidence_test.go 与 todo_dispatcher_test.go**

(a) `evidence_test.go`：
- `TestNormalizeStepExecutionPrefersFetchedDocumentsOverSnippets` → 函数改名 `TestNormalizeTodoExecutionPrefersFetchedDocumentsOverSnippets`；`step := StepExecution{ Step: ResearchStep{...}, ... }` → `execution := TodoExecution{ ... }`（删除 `Step` 字段）；`normalized := normalizeStepExecutionSources(step)` → `normalized := normalizeTodoExecutionSources(execution)`；断言不变。
- `TestNormalizeStepExecutionAddsEvidenceRefs` → 改名 `TestNormalizeTodoExecutionAddsEvidenceRefs`；同样把 `StepExecution{Step: ...}` → `TodoExecution{...}`、`normalizeStepExecutionSources` → `normalizeTodoExecutionSources`。
- `TestStepExecutionToTodoExecutionPreservesDocuments` → 改名 `TestFinalizeTodoExecutionPreservesDocuments`；`step := StepExecution{ Step: todoToResearchStep(todo), Summary: "combined", ResearcherResults: ... }` → `execution := TodoExecution{ Summary: "combined", ResearcherResults: ... }`（删除 `Step`）；`execution := stepExecutionToTodoExecution(todo, step)` → `result := finalizeTodoExecution(todo, execution)`，并把后续断言里的 `execution.` 改为 `result.`。

(b) `todo_dispatcher_test.go` 的 `TestStepExecutionToTodoExecutionPreservesFanoutResults` → 改名 `TestFinalizeTodoExecutionPreservesFanoutResults`；`step := StepExecution{ Step: todoToResearchStep(todo), ... }` → `execution := TodoExecution{ ... }`（删除 `Step`）；`execution := stepExecutionToTodoExecution(todo, step)` → `result := finalizeTodoExecution(todo, execution)`，后续断言 `execution.` → `result.`。

- [ ] **Step 10: 整体验证**

Run:
```
go build ./...
go test ./...
go test -race ./internal/research/
go vet ./...
gofmt -l internal cmd
```
Expected: build OK；测试全绿；race 干净；vet 干净；`gofmt -l` 无输出。

如有 `StepExecution` 残留编译错误，逐个按上述映射修正：结果类型 → `TodoExecution`，prior-context 类型 → `priorResearchView`，转换函数 → `finalizeTodoExecution`，归一化函数 → `normalizeTodoExecutionSources`。

- [ ] **Step 11: 确认无残留**

Run: `grep -rn "StepExecution\b" internal/research --include='*.go' | grep -v "StepExecutionInput"`
Expected: 无输出（除 `StepExecutionInput` 外，`StepExecution` 已全部消除）。

- [ ] **Step 12: 提交**

```bash
git add internal/research/result.go internal/research/executor.go internal/research/researchers.go internal/research/todo_research_loop.go internal/research/todo_dispatcher.go internal/research/todo_executor.go internal/research/executor_test.go internal/research/todo_research_loop_test.go internal/research/evidence_test.go internal/research/todo_dispatcher_test.go
git commit -m "refactor: unify execution result type on TodoExecution, drop StepExecution"
```

---

## Task 3: 更新文档

**Files:**
- Modify: `README.md`

- [ ] **Step 1: 更新 README 数据流措辞**

在 `README.md` 数据流第 4 节"单个 Todo 的执行"的流程图中，把 `StepExecution` 节点改为 `TodoExecution`（统一执行结果类型后不再有独立 StepExecution 中间结构）。具体把片段：

```text
AgentSynthesizer
  |
  v
StepExecution
  |
  v
gap check
```

改为：

```text
AgentSynthesizer
  |
  v
TodoExecution（研究产出，Todo/Status/Findings 由 finalize 补齐）
  |
  v
gap check
```

并在该节末尾或第 8 节"证据归一化"处，把出现的 `normalizeStepExecutionSources` 描述更新为 `normalizeTodoExecutionSources`（若 README 有提及）。

- [ ] **Step 2: 验证 + 提交**

Run: `go test ./...`
Expected: 全绿（文档改动不影响测试）。

```bash
git add README.md
git commit -m "docs: update data flow to reflect unified TodoExecution result type"
```

---

## Self-Review 摘要

- **Spec 覆盖**：
  - 新增 `priorResearchView`（spec §组件设计 1）→ Task 1
  - 删除 `StepExecution`（§2）→ Task 2 Step 1
  - step 级组件改返回 `TodoExecution`（§3）→ Task 2 Step 2-3
  - `runTodoResearchLoop` / prior-context view（§4）→ Task 2 Step 4
  - 投影 helper 更名 + attempt 投影（§5）→ Task 2 Step 5
  - `finalizeTodoExecution` 替换转换（§6）→ Task 2 Step 6
  - 调用方收尾（§7）→ Task 2 Step 7
  - 测试更新（§验证策略）→ Task 2 Step 8-9
  - prompt 字节不变性锁定测试 → Task 1 Step 1
  - 文档 → Task 3
- **占位扫描**：无 TBD/TODO；每个改代码 step 给出完整代码或精确映射规则与命令。
- **类型一致性**：`priorResearchView` / `normalizeTodoExecutionSources` / `dependencyResearchViews` / `attemptResearchView` / `finalizeTodoExecution` / `StepExecuteFunc(... TodoExecution)` 在各 step 间签名一致；复用既有 helper（`todoToResearchStep`、`enrichFindingsEvidence`、`normalizeResearcherSources`、`mergeSources`、`mergeSourceDocuments`、`buildSourceDocuments`、`mergeGapMessages`）均已存在。
- **不变量**：synthesizer system prompt 文本保持不变（§不破坏的不变量）；`ResearchStep` / `StepExecutionInput` / `FirstStepPrompt` 保留。
- **显式不做**：A-clean、synthesizer prompt 重措辞、移除 `ResearchStep`、包分层——均未出现在任务中。
