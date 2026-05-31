# Phase 2 — SectionSynthesizer Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 在 Todo→Final 之间引入 section 级归纳层：每个 section 先产出结构化 `SectionAnswer`，使 `FinalSynthesizer` 输入从"全部 todo"压缩为"M 个 section answer"，并提供单 section 失败隔离。

**Architecture:** 新增 `SectionSynthesizer` 接口 + `AgentSectionSynthesizer` 默认实现，与现有 `FinalSynthesizer` 同构。`Runner.Execute` 在分组后、Final 前调用 `synthesizeSections`：逐 section（v1 串行）调用合成器，成功则用 `SectionAnswer` 回填 `SectionExecution`，失败则走确定性兜底（聚合 todo findings/gaps），兜底也产出 `SectionAnswer`，因此 `[]SectionAnswer` 恒非空。Final 上下文据此走紧凑路径。复用 Phase 1 EventBus 发 `section.started/completed`。

**Tech Stack:** Go、`cloudwego/eino`（`components/model`）、`encoding/json`、现有 EventBus。

参考 spec：`docs/superpowers/specs/2026-05-31-section-synthesizer-design.md`

---

## File Structure

**Create:**
- `internal/research/section_synthesizer.go` — `SectionSynthesizer`、`SectionSynthesisInput`、`SectionAnswer`、`AgentSectionSynthesizer`、解析/兜底/上下文 helper
- `internal/research/section_synthesizer_test.go` — 合成器与 orchestration 测试

**Modify:**
- `internal/research/todo_execution.go` — `SectionExecution` 增加 `KeyFindings` / `Limitations`
- `internal/research/runner.go` — `RunnerConfig.SectionSynthesizer`；`synthesizeSections` 方法；`Execute` 接线
- `internal/research/events.go` — 新增 `EventSectionStarted` / `EventSectionCompleted`
- `internal/research/event_sinks.go` — `BudgetMeter` 把 `section.completed` 计入 `ModelCalls`
- `internal/research/final_synthesizer.go` — `FinalSynthesisInput.SectionAnswers`；`buildFinalSynthesisContext` 紧凑分支；`finalSynthesisContext` 增加 `SectionAnswers`
- `internal/research/runner_test.go` — Execute 集成测试

---

## Task 1: SectionAnswer 类型与 AgentSectionSynthesizer

**Files:**
- Create: `internal/research/section_synthesizer.go`
- Test: `internal/research/section_synthesizer_test.go`

- [ ] **Step 1: 写失败测试**

```go
// internal/research/section_synthesizer_test.go
package research

import (
	"context"
	"testing"
)

func TestAgentSectionSynthesizerParsesValidJSON(t *testing.T) {
	model := &staticToolCallingModel{content: `{"section_id":"s1","title":"S1","summary":"section summary","key_findings":["f1 [src_1]"],"limitations":["lim1"]}`}
	synth := NewAgentSectionSynthesizer(model)
	ans, err := synth.SynthesizeSection(context.Background(), SectionSynthesisInput{
		Question: "Q?",
		Section:  ResearchSection{ID: "s1", Title: "S1"},
		Todos:    []TodoExecution{{Todo: ResearchTodo{ID: "t1"}, Status: TodoDone, Summary: "todo sum"}},
	})
	if err != nil {
		t.Fatalf("SynthesizeSection: %v", err)
	}
	if ans.Summary != "section summary" {
		t.Errorf("summary = %q, want %q", ans.Summary, "section summary")
	}
	if len(ans.KeyFindings) != 1 || ans.KeyFindings[0] != "f1 [src_1]" {
		t.Errorf("key_findings = %#v", ans.KeyFindings)
	}
	if len(ans.Limitations) != 1 {
		t.Errorf("limitations = %#v", ans.Limitations)
	}
}

func TestAgentSectionSynthesizerRejectsNonJSON(t *testing.T) {
	model := &staticToolCallingModel{content: "not json at all"}
	synth := NewAgentSectionSynthesizer(model)
	_, err := synth.SynthesizeSection(context.Background(), SectionSynthesisInput{
		Section: ResearchSection{ID: "s1"},
	})
	if err == nil {
		t.Fatal("want error on non-JSON section answer")
	}
}

func TestAgentSectionSynthesizerRejectsEmptyAnswer(t *testing.T) {
	model := &staticToolCallingModel{content: `{"section_id":"s1","title":"S1"}`}
	synth := NewAgentSectionSynthesizer(model)
	_, err := synth.SynthesizeSection(context.Background(), SectionSynthesisInput{
		Section: ResearchSection{ID: "s1"},
	})
	if err == nil {
		t.Fatal("want error on empty section answer (no summary/findings/limitations)")
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/research/ -run TestAgentSectionSynthesizer -v`
Expected: 编译错误，`NewAgentSectionSynthesizer` / `SectionSynthesisInput` / `SectionAnswer` 未定义。

- [ ] **Step 3: 写实现**

```go
// internal/research/section_synthesizer.go
package research

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

// SectionSynthesizer 把一个 section 下的 todo 结果归纳为结构化 SectionAnswer。
//
// 它位于 todo 调度与 FinalSynthesizer 之间，使 Final 输入从"全部 todo"压缩为
// "M 个 section answer"，并为每个 section 提供独立失败隔离。
type SectionSynthesizer interface {
	SynthesizeSection(ctx context.Context, in SectionSynthesisInput) (SectionAnswer, error)
}

// SectionSynthesisInput 是单个 section 归纳的输入。
type SectionSynthesisInput struct {
	// Question 是用户原始问题。
	Question string `json:"question"`
	// Objective 是 plan.Objective。
	Objective string `json:"objective"`
	// Section 是当前 section 定义。
	Section ResearchSection `json:"section"`
	// Todos 是该 section 下的 todo 执行结果。
	Todos []TodoExecution `json:"todos"`
	// Documents 是该 section 下各 todo Documents 去重合并后的可引用正文。
	Documents []SourceDocument `json:"documents,omitempty"`
}

// SectionAnswer 是单个 section 的结构化归纳产物。
type SectionAnswer struct {
	// SectionID 是对应 section 的稳定 ID。
	SectionID string `json:"section_id"`
	// Title 是 section 标题。
	Title string `json:"title"`
	// Summary 是该 section 的综合结论。
	Summary string `json:"summary"`
	// KeyFindings 是该 section 的核心发现，内联标注 source id，如 "X 支持 Y [todo_1_src_1]"。
	KeyFindings []string `json:"key_findings"`
	// Limitations 是该 section 的证据缺口或不确定性。
	Limitations []string `json:"limitations"`
}

// AgentSectionSynthesizer 使用模型把单个 section 归纳为 SectionAnswer。
type AgentSectionSynthesizer struct {
	model model.BaseChatModel
}

// NewAgentSectionSynthesizer 创建默认 section 合成器。
func NewAgentSectionSynthesizer(m model.BaseChatModel) *AgentSectionSynthesizer {
	return &AgentSectionSynthesizer{model: m}
}

// SynthesizeSection 调用模型归纳单个 section；输出非法或空 answer 时返回错误，
// 由上层 orchestration 走确定性兜底。
func (s *AgentSectionSynthesizer) SynthesizeSection(ctx context.Context, in SectionSynthesisInput) (SectionAnswer, error) {
	if s == nil || isNilDependency(s.model) {
		return SectionAnswer{}, fmt.Errorf("section synthesizer model is nil")
	}

	b, err := json.Marshal(buildSectionSynthesisContext(in))
	if err != nil {
		return SectionAnswer{}, fmt.Errorf("marshal section synthesis input: %w", err)
	}

	resp, err := s.model.Generate(ctx, []*schema.Message{
		schema.SystemMessage(`You are the section synthesis agent for a deep research workflow.

Summarize ONLY the provided section's todo results and documents. Write in the same language as the user's question. Do not invent facts. Cite source-backed claims inline with source IDs like [todo_1_src_1]. Move unsupported or weakly supported statements to limitations.

Return only one JSON object matching:
{
  "section_id": string,
  "title": string,
  "summary": string,
  "key_findings": [string],
  "limitations": [string]
}`),
		schema.UserMessage(string(b)),
	})
	if err != nil {
		return SectionAnswer{}, err
	}
	if resp == nil {
		return SectionAnswer{}, fmt.Errorf("section model response is nil")
	}

	answer, err := parseSectionAnswer(strings.TrimSpace(resp.Content))
	if err != nil {
		return SectionAnswer{}, err
	}
	answer.SectionID = in.Section.ID
	answer.Title = sectionTitleOrID(in.Section)
	return normalizeSectionAnswer(answer), nil
}

// parseSectionAnswer 解析模型返回的 SectionAnswer，并拒绝空 answer。
func parseSectionAnswer(content string) (SectionAnswer, error) {
	if content == "" {
		return SectionAnswer{}, fmt.Errorf("section answer output is empty")
	}
	var answer SectionAnswer
	if err := json.Unmarshal([]byte(content), &answer); err != nil {
		return SectionAnswer{}, fmt.Errorf("invalid SectionAnswer JSON: %w", err)
	}
	if isEmptySectionAnswer(answer) {
		return SectionAnswer{}, fmt.Errorf("SectionAnswer JSON contains no answer fields")
	}
	return answer, nil
}

// normalizeSectionAnswer 去除空白与空项，保证下游消费稳定。
func normalizeSectionAnswer(answer SectionAnswer) SectionAnswer {
	answer.Summary = strings.TrimSpace(answer.Summary)
	answer.KeyFindings = trimNonEmptyStrings(answer.KeyFindings)
	answer.Limitations = trimNonEmptyStrings(answer.Limitations)
	return answer
}

// isEmptySectionAnswer 判断 answer 是否没有任何可展示内容。
func isEmptySectionAnswer(answer SectionAnswer) bool {
	return strings.TrimSpace(answer.Summary) == "" &&
		len(trimNonEmptyStrings(answer.KeyFindings)) == 0 &&
		len(trimNonEmptyStrings(answer.Limitations)) == 0
}

// sectionTitleOrID 返回 section 标题，缺省时回退到 ID。
func sectionTitleOrID(section ResearchSection) string {
	if title := strings.TrimSpace(section.Title); title != "" {
		return title
	}
	if id := strings.TrimSpace(section.ID); id != "" {
		return id
	}
	return "Untitled Section"
}

// buildSectionSynthesisContext 压缩单 section 输入，只保留归纳所需的 todo 字段。
func buildSectionSynthesisContext(in SectionSynthesisInput) sectionSynthesisContext {
	todos := make([]sectionTodoContext, 0, len(in.Todos))
	for _, todo := range in.Todos {
		todos = append(todos, sectionTodoContext{
			ID:       todo.Todo.ID,
			Title:    todo.Todo.Title,
			Question: todo.Todo.Question,
			Status:   todo.Status,
			Summary:  todo.Summary,
			Findings: todo.Findings,
			Gaps:     todo.Gaps,
		})
	}
	return sectionSynthesisContext{
		Question:    in.Question,
		Objective:   in.Objective,
		SectionID:   in.Section.ID,
		Title:       in.Section.Title,
		Description: in.Section.Description,
		Todos:       todos,
		Documents:   in.Documents,
	}
}

type sectionSynthesisContext struct {
	Question    string               `json:"question"`
	Objective   string               `json:"objective"`
	SectionID   string               `json:"section_id"`
	Title       string               `json:"title"`
	Description string               `json:"description,omitempty"`
	Todos       []sectionTodoContext `json:"todos"`
	Documents   []SourceDocument     `json:"documents,omitempty"`
}

type sectionTodoContext struct {
	ID       string     `json:"id"`
	Title    string     `json:"title"`
	Question string     `json:"question"`
	Status   TodoStatus `json:"status"`
	Summary  string     `json:"summary"`
	Findings []Finding  `json:"findings,omitempty"`
	Gaps     []string   `json:"gaps,omitempty"`
}
```

注意：`ResearchSection.Description` 字段存在（见 `todo_plan.go`）；`isNilDependency` / `trimNonEmptyStrings` 已在包内定义，直接复用。

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/research/ -run TestAgentSectionSynthesizer -v`
Expected: PASS（三个测试）。

- [ ] **Step 5: 提交**

```bash
git add internal/research/section_synthesizer.go internal/research/section_synthesizer_test.go
git commit -m "feat: add SectionSynthesizer interface and AgentSectionSynthesizer"
```

---

## Task 2: SectionExecution 增加结构化字段

**Files:**
- Modify: `internal/research/todo_execution.go:49-56`
- Test: `internal/research/section_synthesizer_test.go`

- [ ] **Step 1: 写失败测试**

追加到 `internal/research/section_synthesizer_test.go`：

```go
import "encoding/json" // 合并到现有 import 块

func TestSectionExecutionSerializesStructuredFields(t *testing.T) {
	se := SectionExecution{
		Section: ResearchSection{ID: "s1", Title: "S1"},
		Summary: "sum",
	}
	data, err := json.Marshal(se)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(data), `"key_findings"`) {
		t.Errorf("key_findings must be omitempty when empty, got %s", data)
	}

	se.KeyFindings = []string{"kf"}
	se.Limitations = []string{"lim"}
	data, _ = json.Marshal(se)
	if !strings.Contains(string(data), `"key_findings"`) || !strings.Contains(string(data), `"limitations"`) {
		t.Errorf("structured fields should serialize when set, got %s", data)
	}
}
```

注意：`section_synthesizer_test.go` 顶部 import 需加入 `encoding/json` 和 `strings`（Task 1 已用 `context`/`testing`，这里合并，不要新增第二个 import 块）。

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/research/ -run TestSectionExecutionSerializes -v`
Expected: 编译错误，`SectionExecution` 没有 `KeyFindings` / `Limitations`。

- [ ] **Step 3: 修改 `internal/research/todo_execution.go`**

把 `SectionExecution` 改为：

```go
// SectionExecution 是报告层的分组结果，按原始 plan.sections 顺序聚合对应 todo。
type SectionExecution struct {
	// Section 是对应的 plan section。
	Section ResearchSection `json:"section"`
	// Todos 是该 section 下的 todo 执行结果。
	Todos []TodoExecution `json:"todos"`
	// Summary 是该 section 的综合摘要，由 SectionSynthesizer 产出（兜底时为 todo summary 拼接）。
	Summary string `json:"summary"`
	// KeyFindings 是该 section 的核心发现，来自 SectionAnswer。
	KeyFindings []string `json:"key_findings,omitempty"`
	// Limitations 是该 section 的证据缺口或不确定性，来自 SectionAnswer。
	Limitations []string `json:"limitations,omitempty"`
}
```

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/research/ -run TestSectionExecutionSerializes -v`
Expected: PASS。

- [ ] **Step 5: 提交**

```bash
git add internal/research/todo_execution.go internal/research/section_synthesizer_test.go
git commit -m "feat: add KeyFindings and Limitations to SectionExecution"
```

---

## Task 3: synthesizeSections orchestration + RunnerConfig 装配

**Files:**
- Modify: `internal/research/runner.go`（`RunnerConfig` 字段；新增 `synthesizeSections` 及 helper）
- Test: `internal/research/section_synthesizer_test.go`

- [ ] **Step 1: 写失败测试**

追加到 `internal/research/section_synthesizer_test.go`：

```go
import "errors" // 合并到现有 import 块

type fakeSectionSynthesizer struct {
	failSections map[string]bool
	calls        int
}

func (f *fakeSectionSynthesizer) SynthesizeSection(_ context.Context, in SectionSynthesisInput) (SectionAnswer, error) {
	f.calls++
	if f.failSections[in.Section.ID] {
		return SectionAnswer{}, errors.New("boom")
	}
	return SectionAnswer{
		SectionID:   in.Section.ID,
		Title:       in.Section.Title,
		Summary:     "model summary for " + in.Section.ID,
		KeyFindings: []string{"model finding"},
	}, nil
}

func TestSynthesizeSectionsIsolatesFailures(t *testing.T) {
	fake := &fakeSectionSynthesizer{failSections: map[string]bool{"s2": true}}
	runner, err := NewRunner(RunnerConfig{
		Model:              &staticToolCallingModel{content: "{}"},
		SearchProvider:     search.NewMockProvider(),
		SectionSynthesizer: fake,
	})
	if err != nil {
		t.Fatalf("NewRunner: %v", err)
	}
	sections := []SectionExecution{
		{Section: ResearchSection{ID: "s1", Title: "S1"}, Todos: []TodoExecution{{Todo: ResearchTodo{ID: "t1", Title: "T1"}, Status: TodoDone, Summary: "joined s1", Findings: []Finding{{Claim: "c1", SourceIDs: []string{"src_1"}}}}}},
		{Section: ResearchSection{ID: "s2", Title: "S2"}, Todos: []TodoExecution{{Todo: ResearchTodo{ID: "t2", Title: "T2"}, Status: TodoDone, Summary: "joined s2", Gaps: []string{"missing x"}}}},
	}
	out, answers := runner.synthesizeSections(context.Background(), "Q?", validTodoPlan(), sections)

	if fake.calls != 2 {
		t.Fatalf("synthesizer calls = %d, want 2", fake.calls)
	}
	if len(answers) != 2 {
		t.Fatalf("answers = %d, want 2", len(answers))
	}
	// s1 成功：使用模型 summary。
	if out[0].Summary != "model summary for s1" {
		t.Errorf("s1 summary = %q, want model summary", out[0].Summary)
	}
	if len(out[0].KeyFindings) == 0 {
		t.Errorf("s1 should carry model key findings")
	}
	// s2 失败：走确定性兜底，summary 来自 todo 拼接，不是模型输出。
	if out[1].Summary == "model summary for s2" {
		t.Errorf("s2 summary should be deterministic fallback, got model output")
	}
	if out[1].Summary != "joined s2" {
		t.Errorf("s2 fallback summary = %q, want %q", out[1].Summary, "joined s2")
	}
}

func TestSynthesizeSectionsDefaultsToAgentWhenNil(t *testing.T) {
	// SectionSynthesizer 为 nil 时使用模型默认实现；模型返回合法 SectionAnswer。
	runner, err := NewRunner(RunnerConfig{
		Model:          &staticToolCallingModel{content: `{"summary":"agent default summary","key_findings":["kf"]}`},
		SearchProvider: search.NewMockProvider(),
	})
	if err != nil {
		t.Fatalf("NewRunner: %v", err)
	}
	sections := []SectionExecution{
		{Section: ResearchSection{ID: "s1", Title: "S1"}, Todos: []TodoExecution{{Todo: ResearchTodo{ID: "t1"}, Status: TodoDone, Summary: "joined"}}},
	}
	out, answers := runner.synthesizeSections(context.Background(), "Q?", validTodoPlan(), sections)
	if len(answers) != 1 {
		t.Fatalf("answers = %d, want 1", len(answers))
	}
	if out[0].Summary != "agent default summary" {
		t.Errorf("summary = %q, want agent default", out[0].Summary)
	}
}
```

注意：`validTodoPlan()` 与 `staticToolCallingModel` 已在 `runner_test.go` 定义（同 package），`search` 已在 `runner_test.go` import；本测试文件需在 import 块加入 `errors` 与 `github.com/hu-quan-er/eino_research/internal/search`。

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/research/ -run TestSynthesizeSections -v`
Expected: 编译错误，`RunnerConfig` 无 `SectionSynthesizer`、`Runner` 无 `synthesizeSections`。

- [ ] **Step 3: 修改 `internal/research/runner.go`**

(a) 在 `RunnerConfig` 中 `ClaimVerifier` 字段之后追加：

```go
	// SectionSynthesizer 可替换 section 级归纳器；未传入时使用 AgentSectionSynthesizer。
	SectionSynthesizer SectionSynthesizer
```

(b) 在文件末尾追加 orchestration 与 helper：

```go
// synthesizeSections 对每个 section 调用 SectionSynthesizer（v1 串行）。
//
// 成功时用 SectionAnswer 回填 SectionExecution.Summary/KeyFindings/Limitations；失败或空
// answer 时走确定性兜底（聚合该 section 下 todo 的 findings/gaps）。兜底也产出 SectionAnswer，
// 因此返回的 []SectionAnswer 始终覆盖全部 section，供 Final 走紧凑路径。
func (r *Runner) synthesizeSections(ctx context.Context, question string, plan ResearchTodoPlan, sections []SectionExecution) ([]SectionExecution, []SectionAnswer) {
	synthesizer := r.cfg.SectionSynthesizer
	if synthesizer == nil {
		synthesizer = NewAgentSectionSynthesizer(r.cfg.Model)
	}

	out := make([]SectionExecution, len(sections))
	answers := make([]SectionAnswer, 0, len(sections))
	for i, section := range sections {
		ans, err := synthesizer.SynthesizeSection(ctx, SectionSynthesisInput{
			Question:  question,
			Objective: plan.Objective,
			Section:   section.Section,
			Todos:     section.Todos,
			Documents: collectSectionDocuments(section),
		})
		if err != nil || isEmptySectionAnswer(ans) {
			ans = fallbackSectionAnswer(section)
		}
		section.Summary = ans.Summary
		section.KeyFindings = ans.KeyFindings
		section.Limitations = ans.Limitations
		out[i] = section
		answers = append(answers, ans)
	}
	return out, answers
}

// fallbackSectionAnswer 在模型合成失败时基于 todo 结果构造确定性 SectionAnswer。
func fallbackSectionAnswer(section SectionExecution) SectionAnswer {
	summary := summarizeSectionTodos(section.Todos)
	return SectionAnswer{
		SectionID:   section.Section.ID,
		Title:       sectionTitleOrID(section.Section),
		Summary:     summary,
		KeyFindings: collectTopFindingClaims(section.Todos, 5),
		Limitations: collectFinalLimitations(section.Todos),
	}
}

// collectSectionDocuments 合并该 section 下所有 todo 的 documents 并去重。
func collectSectionDocuments(section SectionExecution) []SourceDocument {
	documents := make([]SourceDocument, 0)
	for _, todo := range section.Todos {
		documents = append(documents, todo.Documents...)
	}
	return dedupeSourceDocuments(documents)
}
```

注意：`summarizeSectionTodos`、`collectTopFindingClaims`、`collectFinalLimitations`、`dedupeSourceDocuments` 均已在包内定义，直接复用；`sectionTitleOrID` / `isEmptySectionAnswer` 来自 Task 1。

- [ ] **Step 4: 运行测试确认通过 + 全量回归**

Run:
```
go test ./internal/research/ -run TestSynthesizeSections -v
go test ./...
```
Expected: 新测试 PASS；现有测试不变。

- [ ] **Step 5: 提交**

```bash
git add internal/research/runner.go internal/research/section_synthesizer_test.go
git commit -m "feat: add synthesizeSections orchestration with per-section fallback"
```

---

## Task 4: section 事件 + BudgetMeter 计数

**Files:**
- Modify: `internal/research/events.go`（新增两个 EventKind）
- Modify: `internal/research/event_sinks.go`（BudgetMeter 计入 section.completed）
- Modify: `internal/research/runner.go`（`synthesizeSections` emit）
- Test: `internal/research/section_synthesizer_test.go`

- [ ] **Step 1: 写失败测试**

追加到 `internal/research/section_synthesizer_test.go`：

```go
func TestSynthesizeSectionsEmitsSectionEvents(t *testing.T) {
	rec := &recordingSink{}
	bus := &EventBus{}
	bus.Add(rec)
	fake := &fakeSectionSynthesizer{}
	runner, err := NewRunner(RunnerConfig{
		Model:              &staticToolCallingModel{content: "{}"},
		SearchProvider:     search.NewMockProvider(),
		SectionSynthesizer: fake,
	})
	if err != nil {
		t.Fatalf("NewRunner: %v", err)
	}
	ctx := withEventBus(context.Background(), bus)
	sections := []SectionExecution{
		{Section: ResearchSection{ID: "s1", Title: "S1"}, Todos: []TodoExecution{{Todo: ResearchTodo{ID: "t1"}, Status: TodoDone, Summary: "x"}}},
	}
	runner.synthesizeSections(ctx, "Q?", validTodoPlan(), sections)

	kinds := make(map[EventKind]int)
	for _, e := range rec.Snapshot() {
		kinds[e.Kind]++
	}
	if kinds[EventSectionStarted] == 0 || kinds[EventSectionCompleted] == 0 {
		t.Fatalf("missing section events; got %v", kinds)
	}
}

func TestBudgetMeterCountsSectionCompleted(t *testing.T) {
	meter := NewBudgetMeter()
	meter.Emit(context.Background(), Event{Kind: EventSectionCompleted})
	if meter.Snapshot().ModelCalls != 1 {
		t.Fatalf("ModelCalls = %d, want 1", meter.Snapshot().ModelCalls)
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/research/ -run "TestSynthesizeSectionsEmits|TestBudgetMeterCountsSection" -v`
Expected: 编译错误，`EventSectionStarted` / `EventSectionCompleted` 未定义。

- [ ] **Step 3: 修改 `internal/research/events.go`**

在 const 块中 `EventTraceTruncated` 之后追加两行（保持对齐）：

```go
	EventSectionStarted   EventKind = "section.started"
	EventSectionCompleted EventKind = "section.completed"
```

- [ ] **Step 4: 修改 `internal/research/event_sinks.go`**

把 `BudgetMeter.Emit` 的 ModelCalls case 改为包含 section.completed：

```go
	case EventSynthesisCompleted, EventFinalCompleted, EventPlanCompleted, EventSectionCompleted:
		m.report.ModelCalls++
```

- [ ] **Step 5: 修改 `internal/research/runner.go` 的 `synthesizeSections`**

在循环体内、调用 synthesizer 前后加 emit（section id 通过 TodoID 承载）：

```go
	bus := eventBusFromContext(ctx)
	runID := runIDFromContext(ctx)

	out := make([]SectionExecution, len(sections))
	answers := make([]SectionAnswer, 0, len(sections))
	for i, section := range sections {
		bus.Emit(ctx, Event{Kind: EventSectionStarted, RunID: runID, TodoID: section.Section.ID})
		ans, err := synthesizer.SynthesizeSection(ctx, SectionSynthesisInput{
			Question:  question,
			Objective: plan.Objective,
			Section:   section.Section,
			Todos:     section.Todos,
			Documents: collectSectionDocuments(section),
		})
		fellBack := false
		if err != nil || isEmptySectionAnswer(ans) {
			ans = fallbackSectionAnswer(section)
			fellBack = true
		}
		section.Summary = ans.Summary
		section.KeyFindings = ans.KeyFindings
		section.Limitations = ans.Limitations
		out[i] = section
		answers = append(answers, ans)
		msg := ""
		if fellBack {
			msg = "section synthesis used deterministic fallback"
		}
		bus.Emit(ctx, Event{Kind: EventSectionCompleted, RunID: runID, TodoID: section.Section.ID, Message: msg})
	}
	return out, answers
```

(把第 3 步 Task 3 中 `synthesizeSections` 的循环体替换为上面带 emit 的版本；`synthesizer` 选择逻辑保持不变。)

- [ ] **Step 6: 运行测试确认通过 + 全量回归**

Run:
```
go test ./internal/research/ -run "TestSynthesizeSectionsEmits|TestBudgetMeterCountsSection" -v
go test ./...
```
Expected: PASS；现有测试不变。

- [ ] **Step 7: 提交**

```bash
git add internal/research/events.go internal/research/event_sinks.go internal/research/runner.go internal/research/section_synthesizer_test.go
git commit -m "feat: emit section.started/completed events and count in budget"
```

---

## Task 5: FinalSynthesisInput.SectionAnswers + 紧凑上下文

**Files:**
- Modify: `internal/research/final_synthesizer.go`
- Test: `internal/research/section_synthesizer_test.go`

- [ ] **Step 1: 写失败测试**

追加到 `internal/research/section_synthesizer_test.go`：

```go
func TestBuildFinalSynthesisContextUsesSectionAnswersWhenPresent(t *testing.T) {
	in := FinalSynthesisInput{
		Question: "Q?",
		Plan:     ResearchTodoPlan{Objective: "obj"},
		SectionAnswers: []SectionAnswer{
			{SectionID: "s1", Title: "S1", Summary: "section sum", KeyFindings: []string{"kf [src_1]"}},
		},
		// 即便有 todo 执行结果，紧凑路径也不应把它们 dump 进上下文。
		SectionExecutions: []SectionExecution{
			{Section: ResearchSection{ID: "s1"}, Todos: []TodoExecution{{Todo: ResearchTodo{ID: "t1", Title: "verbose todo title"}}}},
		},
	}
	data, err := json.Marshal(buildFinalSynthesisContext(in))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	s := string(data)
	if !strings.Contains(s, `"section_answers"`) || !strings.Contains(s, "section sum") {
		t.Errorf("compact context should contain section_answers, got %s", s)
	}
	if strings.Contains(s, "verbose todo title") {
		t.Errorf("compact context must not dump full todos, got %s", s)
	}
}

func TestBuildFinalSynthesisContextFallsBackToTodosWhenNoSectionAnswers(t *testing.T) {
	in := FinalSynthesisInput{
		Question: "Q?",
		SectionExecutions: []SectionExecution{
			{Section: ResearchSection{ID: "s1", Title: "S1"}, Todos: []TodoExecution{{Todo: ResearchTodo{ID: "t1", Title: "verbose todo title"}}}},
		},
	}
	data, err := json.Marshal(buildFinalSynthesisContext(in))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(data), "verbose todo title") {
		t.Errorf("fallback context should contain todo dump, got %s", data)
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/research/ -run TestBuildFinalSynthesisContext -v`
Expected: 编译错误，`FinalSynthesisInput` 无 `SectionAnswers`。

- [ ] **Step 3: 修改 `internal/research/final_synthesizer.go`**

(a) `FinalSynthesisInput` 在 `Documents` 字段之后追加：

```go
	// SectionAnswers 是 section 级归纳产物；非空时 Final 上下文走紧凑路径，不再 dump 全部 todo。
	SectionAnswers []SectionAnswer `json:"section_answers,omitempty"`
```

(b) `finalSynthesisContext` 增加字段，并让 `Sections` 改为 omitempty：

```go
type finalSynthesisContext struct {
	Question       string                         `json:"question"`
	Objective      string                         `json:"objective"`
	SectionAnswers []SectionAnswer                `json:"section_answers,omitempty"`
	Sections       []finalSectionSynthesisContext `json:"sections,omitempty"`
	Sources        []search.Source                `json:"sources"`
	Documents      []SourceDocument               `json:"documents,omitempty"`
}
```

(c) 把 `buildFinalSynthesisContext` 改为：当 `SectionAnswers` 非空时只放 section answers，否则走原 todo-dump：

```go
// buildFinalSynthesisContext 压缩最终综合输入。
//
// 有 section answers 时走紧凑路径（只放 section answers + sources + documents）；否则回退到
// 把全部 todo 结果 dump 给模型的旧路径。
func buildFinalSynthesisContext(in FinalSynthesisInput) finalSynthesisContext {
	ctx := finalSynthesisContext{
		Question:  in.Question,
		Objective: in.Plan.Objective,
		Sources:   in.Sources,
		Documents: in.Documents,
	}
	if len(in.SectionAnswers) > 0 {
		ctx.SectionAnswers = in.SectionAnswers
		return ctx
	}

	sections := make([]finalSectionSynthesisContext, 0, len(in.SectionExecutions))
	for _, section := range in.SectionExecutions {
		todos := make([]finalTodoSynthesisContext, 0, len(section.Todos))
		for _, todo := range section.Todos {
			todos = append(todos, finalTodoSynthesisContext{
				ID:                 todo.Todo.ID,
				Title:              todo.Todo.Title,
				Question:           todo.Todo.Question,
				AcceptanceCriteria: todo.Todo.AcceptanceCriteria,
				Status:             todo.Status,
				Summary:            todo.Summary,
				Findings:           todo.Findings,
				Gaps:               todo.Gaps,
				Error:              todo.Error,
				ResearcherResults:  compactResearcherResults(todo.ResearcherResults),
			})
		}
		sections = append(sections, finalSectionSynthesisContext{
			ID:      section.Section.ID,
			Title:   section.Section.Title,
			Summary: section.Summary,
			Todos:   todos,
		})
	}
	ctx.Sections = sections
	return ctx
}
```

- [ ] **Step 4: 运行测试确认通过 + 全量回归**

Run:
```
go test ./internal/research/ -run TestBuildFinalSynthesisContext -v
go test ./...
```
Expected: PASS；现有 final synthesizer 测试不变。

- [ ] **Step 5: 提交**

```bash
git add internal/research/final_synthesizer.go internal/research/section_synthesizer_test.go
git commit -m "feat: feed FinalSynthesizer compact section answers when available"
```

---

## Task 6: 在 Runner.Execute 接线 + 集成测试

**Files:**
- Modify: `internal/research/runner.go`（`Execute` 调用 `synthesizeSections` 并传 `SectionAnswers`）
- Test: `internal/research/runner_test.go`

- [ ] **Step 1: 写失败测试**

追加到 `internal/research/runner_test.go`：

```go
func TestRunnerExecutePopulatesSectionAnswers(t *testing.T) {
	planJSON := `{
		"objective": "Test",
		"sections": [{"id": "s1", "title": "S1"}],
		"todos": [{"id": "t1", "section_id": "s1", "title": "T1", "question": "Q?",
		           "search_queries": ["q"], "acceptance_criteria": ["ac"]}]
	}`
	// 注入 section 合成器，返回可识别的 summary，断言它进入了 SectionExecution。
	fake := &fakeSectionSynthesizer{}
	model := &staticToolCallingModel{content: `{"summary":"final","key_findings":["kf"],"limitations":[]}`}
	runner, err := NewRunner(RunnerConfig{
		Model:              model,
		SearchProvider:     search.NewMockProvider(),
		SectionSynthesizer: fake,
		TodoExecutor: func(_ context.Context, in TodoExecutorInput) (TodoExecution, error) {
			return TodoExecution{Todo: in.Todo, Status: TodoDone, Summary: "todo done"}, nil
		},
	})
	if err != nil {
		t.Fatalf("NewRunner: %v", err)
	}
	var plan ResearchTodoPlan
	if err := json.Unmarshal([]byte(planJSON), &plan); err != nil {
		t.Fatalf("unmarshal plan: %v", err)
	}
	result, err := runner.Execute(context.Background(), "Q?", plan)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if fake.calls != 1 {
		t.Fatalf("section synthesizer calls = %d, want 1", fake.calls)
	}
	if len(result.SectionExecutions) != 1 {
		t.Fatalf("section executions = %d, want 1", len(result.SectionExecutions))
	}
	if result.SectionExecutions[0].Summary != "model summary for s1" {
		t.Errorf("section summary = %q, want model summary", result.SectionExecutions[0].Summary)
	}
	if len(result.SectionExecutions[0].KeyFindings) == 0 {
		t.Error("section should carry key findings")
	}
}
```

注意：`fakeSectionSynthesizer` 在 Task 3 已定义于 `section_synthesizer_test.go`（同 package，可直接用）。

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/research/ -run TestRunnerExecutePopulatesSectionAnswers -v`
Expected: FAIL —— `result.SectionExecutions[0].Summary` 仍是 todo 拼接（"todo done"），因为 Execute 尚未调用 synthesizeSections。

- [ ] **Step 3: 修改 `internal/research/runner.go` 的 `Execute`**

把现有：

```go
	result.TodoExecutions = todoExecutions
	// 执行完成后再按原 plan 顺序重组 section，避免并发完成顺序影响最终报告结构。
	result.SectionExecutions = groupTodoExecutionsBySection(plan, todoExecutions)
	result.Sources = collectTodoExecutionSources(todoExecutions)
	result.Documents = collectTodoExecutionDocuments(todoExecutions)
	bus.Emit(ctx, Event{Kind: EventFinalStarted, RunID: runID})
	result.Answer = r.synthesizeFinalAnswer(ctx, FinalSynthesisInput{
		Question:          question,
		Plan:              plan,
		SectionExecutions: result.SectionExecutions,
		TodoExecutions:    result.TodoExecutions,
		Sources:           result.Sources,
		Documents:         result.Documents,
	})
```

改为：

```go
	result.TodoExecutions = todoExecutions
	// 执行完成后再按原 plan 顺序重组 section，避免并发完成顺序影响最终报告结构。
	result.SectionExecutions = groupTodoExecutionsBySection(plan, todoExecutions)
	result.Sources = collectTodoExecutionSources(todoExecutions)
	result.Documents = collectTodoExecutionDocuments(todoExecutions)
	// section 级归纳：压缩 Final 输入，并为每个 section 提供独立失败隔离。
	sectionExecutions, sectionAnswers := r.synthesizeSections(ctx, question, plan, result.SectionExecutions)
	result.SectionExecutions = sectionExecutions
	bus.Emit(ctx, Event{Kind: EventFinalStarted, RunID: runID})
	result.Answer = r.synthesizeFinalAnswer(ctx, FinalSynthesisInput{
		Question:          question,
		Plan:              plan,
		SectionExecutions: result.SectionExecutions,
		TodoExecutions:    result.TodoExecutions,
		Sources:           result.Sources,
		Documents:         result.Documents,
		SectionAnswers:    sectionAnswers,
	})
```

- [ ] **Step 4: 运行测试确认通过 + 全量回归 + race**

Run:
```
go test ./internal/research/ -run TestRunnerExecutePopulatesSectionAnswers -v
go test ./...
go test -race ./internal/research/
```
Expected: 新测试 PASS；现有所有测试不变；race 干净。

- [ ] **Step 5: 提交**

```bash
git add internal/research/runner.go internal/research/runner_test.go
git commit -m "feat: run section synthesis before final synthesis in Execute"
```

---

## Task 7: README 文档更新

**Files:**
- Modify: `README.md`

- [ ] **Step 1: 在架构小节补充 section 层说明**

在 `README.md` 的 `## 当前架构` 流程图中 `TodoExecution[]` 与 `FinalSynthesizer` 之间插入一行 `SectionSynthesizer`，并在「模块职责」`internal/research` 段落的描述里，把 "todo synthesizer、final synthesizer" 之间补上 "section synthesizer（按 section 归纳，压缩 final 输入）"。

具体：把流程图片段

```text
TodoExecution[]
  |
  v
FinalSynthesizer
```

改为

```text
TodoExecution[]
  |
  v
SectionSynthesizer
  |
  v
FinalSynthesizer
```

并在数据流第 9 节 `Final Synthesis` 开头补一句：

```markdown
在最终综合之前，`SectionSynthesizer` 会先对每个 section 做归纳，输出 `SectionAnswer`（summary、key_findings、limitations）。FinalSynthesizer 默认消费这些紧凑的 section answer，而不是全部 todo 结果；当某个 section 归纳失败时会回退到该 section 的确定性兜底，不影响其他 section。
```

- [ ] **Step 2: 运行全量测试 + 手工烟测**

Run:
```
go test ./...
go run ./cmd/research --provider mock --yes --stream "Eino 适合构建 research agent 吗？" 2>events.log >/dev/null || true
grep -c '"kind":"section' events.log
rm -f events.log
```
Expected: 测试全绿；`grep -c` 返回 ≥1（出现 section.started/section.completed 事件）。

注意：该烟测需要配置真实模型 key 才能跑完整流程；无 key 时 section 事件可能不出现（plan 阶段即失败）。无 key 环境可跳过 grep 断言，仅确认 `go test ./...` 全绿。

- [ ] **Step 3: 提交**

```bash
git add README.md
git commit -m "docs: document SectionSynthesizer layer in architecture and data flow"
```

---

## Self-Review 摘要

- **Spec 覆盖**：
  - 类型与接口（spec §组件设计 1）→ Task 1
  - SectionExecution 增强（§2）→ Task 2
  - RunnerConfig 装配 + 数据流 + 失败兜底（§3/§4/§5 失败兜底）→ Task 3
  - 事件 + BudgetMeter（§6）→ Task 4
  - Final 紧凑消费（§5）→ Task 5
  - Execute 接线（§4 数据流）→ Task 6
  - 文档 → Task 7
  - 验证策略（§验证策略）→ 各 Task TDD step + Task 6 集成 + race
- **占位扫描**：无 TBD/TODO；每个改代码的 step 都给出完整代码或精确命令。
- **类型一致性**：`SectionAnswer` / `SectionSynthesisInput` / `SectionSynthesizer` / `AgentSectionSynthesizer` / `synthesizeSections` / `fallbackSectionAnswer` / `collectSectionDocuments` / `sectionTitleOrID` / `isEmptySectionAnswer` / `normalizeSectionAnswer` 在各 Task 间签名一致；`EventSectionStarted` / `EventSectionCompleted` 在 Task 4 引入后被 Task 6 间接使用；复用的既有 helper（`summarizeSectionTodos`、`collectTopFindingClaims`、`collectFinalLimitations`、`dedupeSourceDocuments`、`trimNonEmptyStrings`、`isNilDependency`、`compactResearcherResults`）均已在包内存在。
- **显式不做**：section 并行、section 级 citation、renderer Markdown 增强、Phase 3 接入——均未出现在任务中，符合 spec 范围控制。
```
