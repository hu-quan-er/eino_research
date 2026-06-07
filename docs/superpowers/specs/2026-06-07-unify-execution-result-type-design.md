# 统一执行结果类型：消除 StepExecution 设计

日期：2026-06-07

> 本文是架构债清理 #2 的详细设计。目标是收敛 `StepExecution` / `TodoExecution` 双层执行结果模型，让 `TodoExecution` 成为唯一的执行结果类型。采用 **A-conservative** 方案：统一结果类型、删除 `stepExecutionToTodoExecution` 转换，同时**严格保持 researcher / synthesizer 的 prompt 字节级不变**。

## 背景与动机

当前一个 todo 的执行结果在两套结构间往返：

- `StepExecution`（result.go）：step 级组件（`ParallelStepExecutor` / `AgentSynthesizer` / `runTodoResearchLoop`）的产出，字段为 `Step / ResearcherResults / Summary / Gaps / Sources / Documents`。
- `TodoExecution`（todo_execution.go）：调度层结果，字段为 `Todo / Status / ResearcherResults / Summary / Findings / Gaps / Sources / Documents / Error`。

两者字段高度重合，靠两个转换函数桥接：

- `dependencyExecutionsAsSteps([]TodoExecution) []StepExecution`：把依赖结果投影成 prior context。
- `stepExecutionToTodoExecution(todo, StepExecution) TodoExecution`：把 step 级结果转回 todo 结果。

读代码时需要同时记住两套近义词汇，是当前最大的可读性税。

## 已确认的层次事实（设计约束来源）

1. **`ResearchStep` 不是冗余**：它是从 `ResearchTodo` 派生、喂给 researcher prompt 的精简简报（经 `ExpandTodoSearchQueries` 扩展的 queries + success_criteria）。**保留不动。**
2. **step 级组件不知道 `ResearchTodo`**：`ParallelStepExecutor` / `AgentSynthesizer` 只拿到 `ResearchStep`。因此它们产出的 `TodoExecution` 只能填充"研究产出"字段，`Todo / Status / Findings` 由编排层 `defaultTodoExecutor` 补齐。这是**渐进填充**模式。
3. **prior context 会进入 prompt**：依赖结果 + 前几轮尝试会被 `json.Marshal` 进 researcher 和 synthesizer 的 prompt。统一类型若直接改用 `[]TodoExecution`，会改变 prompt 内容（A-clean）。本设计选择 **A-conservative**：保留一个精简投影 DTO 专供 prompt 使用，保证 prompt 字节不变。

## 决策摘要（A-conservative）

- **`TodoExecution` 成为唯一执行结果类型**，删除 `StepExecution` 类型。
- **新增 `priorResearchView`**：prior-context 投影 DTO，JSON 形状与原 `StepExecution` **完全一致**（`step / researcher_results / summary / gaps / sources / documents`），仅用于喂给 prompt。
- **删除 `stepExecutionToTodoExecution`**：其逻辑（normalize + flatten findings + enrich + 置 Status/Todo + summary 兜底）并入 `defaultTodoExecutor.execute` 的循环后 finalize 段。
- **`dependencyExecutionsAsSteps` 重构**为产出 `[]priorResearchView`（更名见下）。
- step 级组件（`Synthesizer` / `ParallelStepExecutor` / `StepExecuteFunc` / `runTodoResearchLoop`）返回类型 `StepExecution → TodoExecution`。
- `normalizeStepExecutionSources` → `normalizeTodoExecutionSources`，操作同样字段。

## 不破坏的不变量

- **researcher / synthesizer 的 prompt 字节级不变**：包括 `AgentResearcher.Research` 的 `Question/Step/Prior executed steps JSON/Assigned focus` 模板、`AgentSynthesizer.Synthesize` 的 system prompt（仍保留 "StepExecution" 措辞，见"显式不做"）与 marshaled 内容。
- `Runner.Plan / Execute / Run` 签名不变。
- `ResearchStep`、`StepExecutionInput`、`SynthesisInput.Step`、`ExpandTodoSearchQueries`、`FirstStepPrompt()` 不变。
- 最终 `TodoExecution` 的字段语义与取值不变（`Findings` 仍是 flatten + enrich 后的结果，`Status` 仍为 `TodoDone`，summary 兜底仍回退到 todo.Title）。
- 现有所有测试在按类型改名后保持通过；断言逻辑不变。

## 组件设计

### 1. 新增 `priorResearchView`（result.go）

替换原 `StepExecution` 在 prior-context 中的角色。**JSON tag 必须与原 `StepExecution` 逐字一致**，否则改变 prompt 字节。

```go
// priorResearchView 是 prior-context 喂给 researcher / synthesizer prompt 的精简投影。
//
// 它故意保持与历史 StepExecution 相同的 JSON 形状（step/researcher_results/summary/
// gaps/sources/documents），以确保统一结果类型后 prompt 内容字节级不变。
type priorResearchView struct {
	Step              ResearchStep       `json:"step"`
	ResearcherResults []ResearcherResult `json:"researcher_results"`
	Summary           string             `json:"summary"`
	Gaps              []string           `json:"gaps,omitempty"`
	Sources           []search.Source    `json:"sources"`
	Documents         []SourceDocument   `json:"documents,omitempty"`
}
```

### 2. 删除 `StepExecution`（result.go）

整段删除 `type StepExecution struct { ... }` 及其注释。

### 3. step 级组件改返回 `TodoExecution`

`executor.go`：

- `Synthesizer.Synthesize(...) (TodoExecution, error)`
- `ParallelStepExecutor.synthesizer` 字段注释更新
- `ExecuteStep(...) (TodoExecution, error)`，全部 `return StepExecution{}` → `return TodoExecution{}`
- `ResearcherInput.ExecutedSteps` / `SynthesisInput.ExecutedSteps` / `StepExecutionInput.ExecutedSteps` 类型 `[]StepExecution → []priorResearchView`

`researchers.go` `AgentSynthesizer.Synthesize`：

- 返回 `TodoExecution`。
- 非 JSON 兜底：`return TodoExecution{ResearcherResults: normalizedResults, Summary: content, Sources: researcherSources}`（不再设 `Step`）。
- 删除 `out.Step = in.Step` 回填逻辑（`TodoExecution` 无 `Step` 字段；模型返回的 `step` 字段在 unmarshal 到 `TodoExecution` 时自然被忽略）。
- `len(out.ResearcherResults) == 0` 补齐、`out.Sources = mergeSources(...)` 逻辑不变。
- **system prompt 文本不变**（仍说 "into one StepExecution ... fields step, researcher_results, ..."）。模型仍返回这 6 字段 JSON，unmarshal 进 `TodoExecution` 时 `step` 被丢弃、其余按 json tag 映射，`Todo/Status/Findings/Error` 保持零值。

`normalizeStepExecutionSources` → `normalizeTodoExecutionSources(TodoExecution) TodoExecution`（函数体不变，只换类型；它只读写 `ResearcherResults/Sources/Documents`，不碰 `Todo/Status/Findings`）。

### 4. `runTodoResearchLoop`（todo_research_loop.go）

- 返回 `TodoExecution`；`attempts []TodoExecution`；`var last TodoExecution`。
- prior-context 改为 `[]priorResearchView`：
  - `baseViews := dependencyResearchViews(in.DependencyExecutions)`（原 `dependencyExecutionsAsSteps` 更名，产出 `[]priorResearchView`，**不含 Documents**，与现状一致）。
  - 每轮把已完成 attempt 投影为 `priorResearchView`：`attemptResearchView(attempt, step)`，其中 `step` 是循环自己计算的 `todoToResearchStep(in.Todo)`（**不带 plan**，保持现状）；该投影**含 Documents**（与现状一致——当前 attempts 是 normalize 后含 documents 的 StepExecution）。
  - `executedViews := append(append([]priorResearchView{}, baseViews...), attemptViews...)`，传入 `StepExecutionInput.ExecutedSteps`。
- 每轮 `execution = normalizeTodoExecutionSources(execution)`。
- `todoResearchGaps(plan, todo, execution TodoExecution)`：签名换类型，函数体不变（只读 `Gaps/Summary/ResearcherResults/Sources`）。
- 返回 `last`（`TodoExecution`，此时 `Todo/Status/Findings` 仍为零值，由 finalize 补齐）。

> 关键 prompt 字节不变性：`baseViews` 不含 documents、`attemptViews` 含 documents，与当前 `dependencyExecutionsAsSteps`（无 documents）+ 循环内 attempts（normalize 后有 documents）的 marshaled 形状逐字一致。

### 5. `dependencyExecutionsAsSteps` 更名 + 新增 attempt 投影（todo_dispatcher.go）

```go
// dependencyResearchViews 把已完成依赖投影为 prior-context view。
// 故意不带 Documents，保持与历史 prompt 字节一致。
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

（这两个 helper 放在 `todo_dispatcher.go` 还是 `todo_research_loop.go` 由实现时就近决定；语义上属于 prior-context 投影。）

### 6. 删除 `stepExecutionToTodoExecution`，逻辑并入 finalize（todo_dispatcher.go → todo_executor.go）

新增 `finalizeTodoExecution`（或内联到 `defaultTodoExecutor.execute`）：

```go
// finalizeTodoExecution 把循环产出的研究结果补齐为完整 TodoExecution。
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

`defaultTodoExecutor.execute` 末尾：`return finalizeTodoExecution(in.Todo, execution), nil`（替换原 `stepExecutionToTodoExecution`）。`ExecuteStep` 闭包返回 `TodoExecution`；其内的 fetched documents 合并逻辑不变。

> 注意：原 `stepExecutionToTodoExecution` 会再做一次 `normalizeStepExecutionSources`。循环每轮已 normalize，finalize 再 normalize 一次保持与现状等价（normalize 幂等，不改变结果）。

### 7. 调用方收尾

- `todo_executor.go`：`StepExecuteFunc` 闭包签名 `(StepExecutionInput) (TodoExecution, error)`；`return StepExecution{}` → `return TodoExecution{}`；末尾 finalize；注释里 "把 StepExecution 转回 TodoExecution" 更新。
- `result.go`：删 `StepExecution`，加 `priorResearchView`。
- doc/注释中残留 "StepExecution" 描述按语义更新（synthesizer system prompt 除外，见不变量）。

## 验证策略（TDD / 回归）

纯重构，以现有测试为安全网，按类型改名更新后断言不变：

- `executor_test.go`（8 处）、`todo_research_loop_test.go`（7 处）、`evidence_test.go`（8 处）、`todo_dispatcher_test.go`（2 处）中直接构造/断言 `StepExecution` 的地方，改为 `TodoExecution` 或 `priorResearchView`（prior-context 构造处）。
- 新增/保留断言：`runTodoResearchLoop` 与 `defaultTodoExecutor.execute` 的最终 `TodoExecution` 的 `Findings/Status/Summary/Sources/Documents` 取值与重构前一致。
- **prompt 字节不变性测试**：新增一个针对 `priorResearchView` 的序列化测试，断言 dependency view（无 documents）与 attempt view（有 documents）marshal 出的 JSON 形状符合预期，锁住 prompt 形状。
- `go test ./...`、`go test -race ./internal/research/`、`go vet ./...`、`gofmt -l` 全绿。
- 无 model key，端到端 live 烟测不在范围内（与既往一致）。

## 显式不做（范围控制 / YAGNI）

- **A-clean（prior context 直接用 `[]TodoExecution`、删投影 DTO）**：会改 prompt 内容，单列为后续"prompt 增强"议题。
- **synthesizer system prompt 重新措辞**（去掉 "StepExecution" 字样）：属 prompt 调优，会改模型行为，单列。
- **移除 `ResearchStep`**：它是合理的 researcher 简报抽象，保留。
- **`StepExecutionInput` 更名**：保留现名，避免无谓 ripple。
- **包分层 / 子包拆分（#3）**：独立议题。

## 风险与权衡

- **prior-context 投影 DTO 仍在**：A-conservative 的代价是保留一个 `priorResearchView`，但它职责单一、命名明确（"prior-context 投影"），不再是含糊的"第二个执行结果类型"。净可读性提升明确。
- **渐进填充的 TodoExecution**：step 级组件返回 `Todo/Status/Findings` 为零值的 `TodoExecution`，存在"未完全填充"的中间态。通过 finalize 单点补齐 + 注释说明消除歧义。
- **字节不变性靠 helper 的 Documents 取舍维持**：dependency view 无 documents、attempt view 有 documents 是为对齐现状，实现时必须严格遵守，故加序列化测试锁定。
