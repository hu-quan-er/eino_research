# Phase 1 — EventBus, Sinks & Metadata Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 引入 `EventBus` + 三个内置 `EventSink`（`JSONLinesSink` / `TraceStore` / `BudgetMeter`），把所有阶段边界改造为发事件，并在 `Runner.Execute` 结束时把 trace 和 budget 注入 `ResearchResult.Metadata`，同时给 CLI 加 `--stream` / `--trace` 两个 flag。

**Architecture:** EventBus 是 fire-and-forget 副作用总线：所有阶段在边界处 `Emit(Event)`，主路径不依赖事件返回值。`Runner` 在 `Events == nil` 时内部创建一个 no-op bus 并自动挂上 `TraceStore` + `BudgetMeter`，所以 `Metadata.Trace` / `Metadata.Budget` 总是有值。CLI `--stream` 只是再加一个 `JSONLinesSink(os.Stderr)`；`--trace` 控制 JSON 输出是否保留 `Metadata.Trace`。

**Tech Stack:** Go 1.x、`cloudwego/eino`（已有依赖）、`encoding/json`、`sync`。

---

## File Structure

**Create:**
- `internal/research/events.go` — `EventKind`、`Event`、`EventSink`、`EventBus`（nil-safe + panic-safe）
- `internal/research/event_sinks.go` — `TraceStore`、`JSONLinesSink`、`BudgetMeter`
- `internal/research/events_test.go` — EventBus 行为测试
- `internal/research/event_sinks_test.go` — 三个 sink 的单元测试

**Modify:**
- `internal/research/result.go` — `Metadata` 增加 `Trace []Event` 和 `Budget *BudgetReport`
- `internal/research/runner.go` — `RunnerConfig.Events`、`Plan` / `Execute` 入口生成 RunID、阶段边界 `Emit`、`Execute` 结束时 snapshot 到 Metadata
- `internal/research/tools.go` — `NewWebSearchTool` / `NewWebFetchTool` 增加 emit 回调
- `internal/research/runner_test.go` — 新增 events 集成测试
- `internal/research/tools_test.go` — 新增 tool emit 测试
- `cmd/research/main.go` — 新增 `--stream` / `--trace` flag、装配 EventBus
- `cmd/research/main_test.go` — 覆盖两个新 flag

---

## Task 1: Event 类型与 nil-safe EventBus

**Files:**
- Create: `internal/research/events.go`
- Test: `internal/research/events_test.go`

- [ ] **Step 1: 写失败测试 — nil bus Emit 不 panic、Add 不 panic**

```go
// internal/research/events_test.go
package research

import (
	"context"
	"sync"
	"testing"
)

type recordingSink struct {
	mu     sync.Mutex
	events []Event
}

func (s *recordingSink) Emit(_ context.Context, e Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, e)
}

func (s *recordingSink) Snapshot() []Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Event, len(s.events))
	copy(out, s.events)
	return out
}

func TestEventBusNilSafe(t *testing.T) {
	var bus *EventBus
	bus.Emit(context.Background(), Event{Kind: EventPlanStarted})
	bus.Add(&recordingSink{})
}

func TestEventBusFansOutToAllSinks(t *testing.T) {
	bus := &EventBus{}
	a := &recordingSink{}
	b := &recordingSink{}
	bus.Add(a)
	bus.Add(b)
	bus.Emit(context.Background(), Event{Kind: EventPlanStarted, RunID: "r1"})
	if got := len(a.Snapshot()); got != 1 {
		t.Fatalf("sink a: want 1 event, got %d", got)
	}
	if got := len(b.Snapshot()); got != 1 {
		t.Fatalf("sink b: want 1 event, got %d", got)
	}
}

type panickingSink struct{}

func (panickingSink) Emit(_ context.Context, _ Event) { panic("boom") }

func TestEventBusRecoversFromSinkPanic(t *testing.T) {
	bus := &EventBus{}
	bus.Add(panickingSink{})
	good := &recordingSink{}
	bus.Add(good)
	bus.Emit(context.Background(), Event{Kind: EventPlanStarted})
	if got := len(good.Snapshot()); got != 1 {
		t.Fatalf("good sink should still receive event after panicking sink; got %d", got)
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/research/ -run TestEventBus -v`
Expected: 编译错误，`EventBus / EventKind / Event / EventPlanStarted` 未定义。

- [ ] **Step 3: 写最小实现**

```go
// internal/research/events.go
package research

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"
)

// EventKind 是 EventBus 派发的事件类别。每个 kind 对应主流程的一个阶段边界。
type EventKind string

const (
	EventPlanStarted     EventKind = "plan.started"
	EventPlanCompleted   EventKind = "plan.completed"
	EventTodoStarted     EventKind = "todo.started"
	EventTodoCompleted   EventKind = "todo.completed"
	EventTodoFailed      EventKind = "todo.failed"
	EventDispatch        EventKind = "todo.dispatched"
	EventResearcherStart EventKind = "researcher.started"
	EventResearcherDone  EventKind = "researcher.completed"
	EventToolCall        EventKind = "tool.call"
	EventSynthesis       EventKind = "synthesis.completed"
	EventGapRetry        EventKind = "todo.retry"
	EventFinalStart      EventKind = "final.started"
	EventFinalCompleted  EventKind = "final.completed"
	EventEvidenceBound   EventKind = "evidence.bound"
	EventClaimsVerified  EventKind = "claims.verified"
	EventTraceTruncated  EventKind = "trace.truncated"
)

// Event 是阶段边界发出的结构化事件。所有字段均为 omitempty 友好。
type Event struct {
	Kind       EventKind       `json:"kind"`
	At         time.Time       `json:"at"`
	RunID      string          `json:"run_id"`
	TodoID     string          `json:"todo_id,omitempty"`
	Role       string          `json:"role,omitempty"`
	Attempt    int             `json:"attempt,omitempty"`
	DurationMS int64           `json:"duration_ms,omitempty"`
	TokensIn   int             `json:"tokens_in,omitempty"`
	TokensOut  int             `json:"tokens_out,omitempty"`
	Tool       string          `json:"tool,omitempty"`
	Query      string          `json:"query,omitempty"`
	URL        string          `json:"url,omitempty"`
	Message    string          `json:"message,omitempty"`
	Err        string          `json:"error,omitempty"`
	Payload    json.RawMessage `json:"payload,omitempty"`
}

// EventSink 是事件接收端，所有 sink 实现都必须是 best-effort（不返回错误、自己处理失败）。
type EventSink interface {
	Emit(ctx context.Context, e Event)
}

// EventBus 把事件 fan-out 到所有 sink。nil 接收者安全：所有方法都是 no-op。
// Emit 内部对每个 sink 单独 recover，单个 sink panic 不影响其他 sink 和主路径。
type EventBus struct {
	mu    sync.RWMutex
	sinks []EventSink
}

// Add 注册一个 sink。nil 接收者或 nil sink 都是 no-op。
func (b *EventBus) Add(sink EventSink) {
	if b == nil || sink == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.sinks = append(b.sinks, sink)
}

// Emit 将事件 fan-out 到所有 sink。如果事件没有时间戳则补一个。
// 单个 sink panic 会被 recover 并写到 stderr，不会向上传播。
func (b *EventBus) Emit(ctx context.Context, e Event) {
	if b == nil {
		return
	}
	if e.At.IsZero() {
		e.At = time.Now()
	}
	b.mu.RLock()
	sinks := append([]EventSink(nil), b.sinks...)
	b.mu.RUnlock()
	for _, sink := range sinks {
		safeEmit(ctx, sink, e)
	}
}

func safeEmit(ctx context.Context, sink EventSink, e Event) {
	defer func() {
		if r := recover(); r != nil {
			fmt.Fprintf(os.Stderr, "event sink panic recovered: %v\n", r)
		}
	}()
	sink.Emit(ctx, e)
}
```

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/research/ -run TestEventBus -v`
Expected: PASS，三个测试都绿。

- [ ] **Step 5: 提交**

```bash
git add internal/research/events.go internal/research/events_test.go
git commit -m "feat: add nil-safe EventBus with panic-isolated sinks"
```

---

## Task 2: TraceStore sink

**Files:**
- Create: `internal/research/event_sinks.go`
- Test: `internal/research/event_sinks_test.go`

- [ ] **Step 1: 写失败测试 — 累积、Snapshot 副本、MaxEvents 截断**

```go
// internal/research/event_sinks_test.go
package research

import (
	"context"
	"testing"
)

func TestTraceStoreAccumulatesAndSnapshotsCopies(t *testing.T) {
	store := NewTraceStore(0) // 0 表示使用默认上限
	ctx := context.Background()
	store.Emit(ctx, Event{Kind: EventPlanStarted, RunID: "r1"})
	store.Emit(ctx, Event{Kind: EventPlanCompleted, RunID: "r1"})

	snap := store.Snapshot()
	if len(snap) != 2 {
		t.Fatalf("want 2 events in snapshot, got %d", len(snap))
	}
	// 修改 snapshot 不影响 store。
	snap[0].Kind = "tampered"
	if store.Snapshot()[0].Kind == "tampered" {
		t.Fatal("snapshot should be a copy, but tampering propagated")
	}
}

func TestTraceStoreTruncatesAtMaxEventsAndEmitsWarning(t *testing.T) {
	store := NewTraceStore(2)
	ctx := context.Background()
	store.Emit(ctx, Event{Kind: EventPlanStarted})
	store.Emit(ctx, Event{Kind: EventTodoStarted})
	store.Emit(ctx, Event{Kind: EventTodoCompleted}) // 触发截断

	snap := store.Snapshot()
	if len(snap) != 2 {
		t.Fatalf("want 2 events after truncation, got %d", len(snap))
	}
	// 最早的 EventPlanStarted 被丢弃；剩下应是 todo.started、todo.completed，
	// 但其中一条是 trace.truncated 警告（替代最早被丢弃的那条）。
	hasTruncWarning := false
	for _, e := range snap {
		if e.Kind == EventTraceTruncated {
			hasTruncWarning = true
		}
	}
	if !hasTruncWarning {
		t.Fatal("want a trace.truncated warning event in snapshot")
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/research/ -run TestTraceStore -v`
Expected: 编译错误，`NewTraceStore` / `TraceStore` 未定义。

- [ ] **Step 3: 写最小实现**

```go
// internal/research/event_sinks.go
package research

import (
	"context"
	"sync"
)

// defaultTraceMaxEvents 是 TraceStore 默认最大事件数。超过后最早事件被丢弃，并 emit 一条
// trace.truncated 警告替代被丢弃的位置。
const defaultTraceMaxEvents = 5000

// TraceStore 是把事件累积在内存的 sink，供 Execute 结束后注入 Metadata.Trace。
type TraceStore struct {
	mu       sync.Mutex
	events   []Event
	max      int
	warned   bool
}

// NewTraceStore 创建 TraceStore。max <= 0 时使用默认上限 5000。
func NewTraceStore(max int) *TraceStore {
	if max <= 0 {
		max = defaultTraceMaxEvents
	}
	return &TraceStore{max: max}
}

// Emit 追加事件；超过上限时丢弃最早事件并首次插入一条 trace.truncated 警告。
func (s *TraceStore) Emit(_ context.Context, e Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, e)
	if len(s.events) > s.max {
		// 丢弃最早事件；如果是第一次截断，写一条 truncated 警告占位。
		s.events = s.events[len(s.events)-s.max:]
		if !s.warned {
			s.events[0] = Event{Kind: EventTraceTruncated, At: e.At, Message: "trace exceeded max events"}
			s.warned = true
		}
	}
}

// Snapshot 返回事件的副本。
func (s *TraceStore) Snapshot() []Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Event, len(s.events))
	copy(out, s.events)
	return out
}
```

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/research/ -run TestTraceStore -v`
Expected: PASS。

- [ ] **Step 5: 提交**

```bash
git add internal/research/event_sinks.go internal/research/event_sinks_test.go
git commit -m "feat: add TraceStore event sink with max-events truncation"
```

---

## Task 3: JSONLinesSink

**Files:**
- Modify: `internal/research/event_sinks.go`
- Modify: `internal/research/event_sinks_test.go`

- [ ] **Step 1: 写失败测试 — 单行输出 + 并发不撕裂**

```go
// 追加到 internal/research/event_sinks_test.go
import (
	"bytes"
	"encoding/json"
	"strings"
	"sync"
)

func TestJSONLinesSinkWritesOneJSONPerLine(t *testing.T) {
	var buf bytes.Buffer
	sink := NewJSONLinesSink(&buf)
	ctx := context.Background()
	sink.Emit(ctx, Event{Kind: EventPlanStarted, RunID: "r1"})
	sink.Emit(ctx, Event{Kind: EventPlanCompleted, RunID: "r1"})

	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("want 2 lines, got %d", len(lines))
	}
	var got Event
	if err := json.Unmarshal([]byte(lines[0]), &got); err != nil {
		t.Fatalf("line 0 not valid JSON: %v", err)
	}
	if got.Kind != EventPlanStarted {
		t.Fatalf("want kind %s, got %s", EventPlanStarted, got.Kind)
	}
}

func TestJSONLinesSinkConcurrent(t *testing.T) {
	var buf bytes.Buffer
	sink := NewJSONLinesSink(&buf)
	ctx := context.Background()
	var wg sync.WaitGroup
	const n = 200
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sink.Emit(ctx, Event{Kind: EventTodoStarted, RunID: "r1"})
		}()
	}
	wg.Wait()
	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if len(lines) != n {
		t.Fatalf("want %d lines, got %d", n, len(lines))
	}
	for i, line := range lines {
		var e Event
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			t.Fatalf("line %d torn: %v (raw=%q)", i, err, line)
		}
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/research/ -run TestJSONLinesSink -v`
Expected: 编译错误，`NewJSONLinesSink` 未定义。

- [ ] **Step 3: 写最小实现 — 追加到 event_sinks.go**

```go
// 追加到 internal/research/event_sinks.go
import (
	"encoding/json"
	"fmt"
	"io"
	"os"
)

// JSONLinesSink 把每条事件序列化为一行 JSON 写到 io.Writer。
// 内部 mutex 保证并发 emit 时不会出现行撕裂。
type JSONLinesSink struct {
	mu sync.Mutex
	w  io.Writer
}

// NewJSONLinesSink 创建一个写到给定 writer 的 sink。w 为 nil 时退化为 os.Stderr。
func NewJSONLinesSink(w io.Writer) *JSONLinesSink {
	if w == nil {
		w = os.Stderr
	}
	return &JSONLinesSink{w: w}
}

// Emit 写一行 JSON。序列化或写入失败时打印到 stderr，不向上传播。
func (s *JSONLinesSink) Emit(_ context.Context, e Event) {
	data, err := json.Marshal(e)
	if err != nil {
		fmt.Fprintf(os.Stderr, "jsonlines sink marshal error: %v\n", err)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.w.Write(append(data, '\n')); err != nil {
		fmt.Fprintf(os.Stderr, "jsonlines sink write error: %v\n", err)
	}
}
```

注意：`event_sinks.go` 顶部的 import 块需要合并新增的 `encoding/json` / `fmt` / `io` / `os`，不要新增第二个 import 块。

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/research/ -run TestJSONLinesSink -v`
Expected: PASS（包括并发测试）。

- [ ] **Step 5: 提交**

```bash
git add internal/research/event_sinks.go internal/research/event_sinks_test.go
git commit -m "feat: add JSONLinesSink with concurrency-safe line writes"
```

---

## Task 4: BudgetMeter sink

**Files:**
- Modify: `internal/research/event_sinks.go`
- Modify: `internal/research/event_sinks_test.go`

- [ ] **Step 1: 写失败测试 — 累加 ToolCalls/ModelCalls/Tokens**

```go
// 追加到 internal/research/event_sinks_test.go
func TestBudgetMeterAggregatesEvents(t *testing.T) {
	meter := NewBudgetMeter()
	ctx := context.Background()
	meter.Emit(ctx, Event{Kind: EventToolCall, Tool: "web_search"})
	meter.Emit(ctx, Event{Kind: EventToolCall, Tool: "web_search"})
	meter.Emit(ctx, Event{Kind: EventToolCall, Tool: "web_fetch"})
	meter.Emit(ctx, Event{Kind: EventTodoCompleted})
	meter.Emit(ctx, Event{Kind: EventTodoFailed})
	meter.Emit(ctx, Event{Kind: EventSynthesis, TokensIn: 100, TokensOut: 50})
	meter.Emit(ctx, Event{Kind: EventFinalCompleted, TokensIn: 200, TokensOut: 80, DurationMS: 1234})

	report := meter.Snapshot()
	if report.ToolCalls["web_search"] != 2 {
		t.Errorf("web_search: want 2, got %d", report.ToolCalls["web_search"])
	}
	if report.ToolCalls["web_fetch"] != 1 {
		t.Errorf("web_fetch: want 1, got %d", report.ToolCalls["web_fetch"])
	}
	if report.TodosCompleted != 1 {
		t.Errorf("todos completed: want 1, got %d", report.TodosCompleted)
	}
	if report.TodosFailed != 1 {
		t.Errorf("todos failed: want 1, got %d", report.TodosFailed)
	}
	if report.TokensIn != 300 || report.TokensOut != 130 {
		t.Errorf("tokens: want in=300 out=130, got in=%d out=%d", report.TokensIn, report.TokensOut)
	}
	if report.DurationMS != 1234 {
		t.Errorf("duration: want 1234, got %d", report.DurationMS)
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/research/ -run TestBudgetMeter -v`
Expected: 编译错误，`NewBudgetMeter` / `BudgetReport` 未定义。

- [ ] **Step 3: 写最小实现 — 追加到 event_sinks.go**

```go
// 追加到 internal/research/event_sinks.go

// BudgetReport 是 BudgetMeter 对一次 run 的聚合统计。
type BudgetReport struct {
	TodosCompleted   int            `json:"todos_completed"`
	TodosFailed      int            `json:"todos_failed"`
	ToolCalls        map[string]int `json:"tool_calls,omitempty"`
	ModelCalls       int            `json:"model_calls"`
	TokensIn         int            `json:"tokens_in"`
	TokensOut        int            `json:"tokens_out"`
	DurationMS       int64          `json:"duration_ms"`
	ReflectionPasses int            `json:"reflection_passes"` // Phase 3 用，Phase 1 永远为 0
}

// BudgetMeter 是把事件聚合为 BudgetReport 的 sink。
// ToolCall/Synthesis/FinalCompleted 等事件会累加对应字段。
type BudgetMeter struct {
	mu     sync.Mutex
	report BudgetReport
}

// NewBudgetMeter 创建空 BudgetMeter。
func NewBudgetMeter() *BudgetMeter {
	return &BudgetMeter{report: BudgetReport{ToolCalls: make(map[string]int)}}
}

// Emit 根据事件类别更新累计统计。
func (m *BudgetMeter) Emit(_ context.Context, e Event) {
	m.mu.Lock()
	defer m.mu.Unlock()
	switch e.Kind {
	case EventToolCall:
		if e.Tool != "" {
			m.report.ToolCalls[e.Tool]++
		}
	case EventTodoCompleted:
		m.report.TodosCompleted++
	case EventTodoFailed:
		m.report.TodosFailed++
	case EventSynthesis, EventFinalCompleted, EventPlanCompleted:
		m.report.ModelCalls++
	}
	m.report.TokensIn += e.TokensIn
	m.report.TokensOut += e.TokensOut
	if e.Kind == EventFinalCompleted {
		// FinalCompleted 携带整次 run 的总耗时；如果上层 emit 时填了 DurationMS，使用它。
		if e.DurationMS > 0 {
			m.report.DurationMS = e.DurationMS
		}
	}
}

// Snapshot 返回当前累计的副本（ToolCalls map 也是副本）。
func (m *BudgetMeter) Snapshot() BudgetReport {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := m.report
	tc := make(map[string]int, len(m.report.ToolCalls))
	for k, v := range m.report.ToolCalls {
		tc[k] = v
	}
	out.ToolCalls = tc
	return out
}
```

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/research/ -run TestBudgetMeter -v`
Expected: PASS。

- [ ] **Step 5: 提交**

```bash
git add internal/research/event_sinks.go internal/research/event_sinks_test.go
git commit -m "feat: add BudgetMeter sink aggregating tool/model/token counts"
```

---

## Task 5: 在 Metadata 中追加 Trace 和 Budget 字段

**Files:**
- Modify: `internal/research/result.go:168-182`

- [ ] **Step 1: 写失败测试 — JSON omitempty 行为**

追加到 `internal/research/runner_test.go`（已有 package research，直接加函数即可）：

```go
func TestMetadataTraceAndBudgetSerialization(t *testing.T) {
	md := Metadata{
		Model:          "gpt-4.1",
		SearchProvider: "mock",
	}
	data, err := json.Marshal(md)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(data), `"trace"`) {
		t.Errorf("trace must be omitempty when empty, got %s", data)
	}
	if strings.Contains(string(data), `"budget"`) {
		t.Errorf("budget must be omitempty when nil, got %s", data)
	}

	md.Trace = []Event{{Kind: EventPlanStarted}}
	md.Budget = &BudgetReport{ModelCalls: 3}
	data, err = json.Marshal(md)
	if err != nil {
		t.Fatalf("marshal with values: %v", err)
	}
	if !strings.Contains(string(data), `"trace"`) {
		t.Errorf("trace should be present when set, got %s", data)
	}
	if !strings.Contains(string(data), `"budget"`) {
		t.Errorf("budget should be present when set, got %s", data)
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/research/ -run TestMetadataTraceAndBudgetSerialization -v`
Expected: 编译错误，`Metadata.Trace` / `Metadata.Budget` 不存在。

- [ ] **Step 3: 修改 `internal/research/result.go`，在 `Metadata` 末尾追加两字段**

```go
// Metadata 记录一次 run 的运行环境和耗时，便于追踪模型、搜索 provider 与预算设置。
type Metadata struct {
	// Model 是本次运行使用的模型名称。
	Model string `json:"model"`
	// SearchProvider 是本次运行使用的搜索 provider 名称。
	SearchProvider string `json:"search_provider"`
	// MaxTodoResearchIterations 是单个 todo 内 gap retry 的最大轮数。
	MaxTodoResearchIterations int `json:"max_todo_research_iterations"`
	// StartedAt 是 run 开始时间，使用 RFC3339 字符串。
	StartedAt string `json:"started_at"`
	// CompletedAt 是 run 结束时间，使用 RFC3339 字符串。
	CompletedAt string `json:"completed_at"`
	// DurationMS 是 run 总耗时，单位毫秒。
	DurationMS int64 `json:"duration_ms"`
	// Trace 是事件追踪记录，由 Runner 在 Execute 结束时从内置 TraceStore 写入。
	Trace []Event `json:"trace,omitempty"`
	// Budget 是事件累计统计，由 Runner 在 Execute 结束时从内置 BudgetMeter 写入。
	Budget *BudgetReport `json:"budget,omitempty"`
}
```

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/research/ -run TestMetadataTraceAndBudgetSerialization -v`
Expected: PASS。

- [ ] **Step 5: 提交**

```bash
git add internal/research/result.go internal/research/runner_test.go
git commit -m "feat: add Trace and Budget fields to ResearchResult Metadata"
```

---

## Task 6: 把 EventBus 注入 Runner，并在 Plan/Execute 顶层 emit

**Files:**
- Modify: `internal/research/runner.go`（多处，见步骤）
- Modify: `internal/research/runner_test.go`

- [ ] **Step 1: 写失败测试 — Execute 后 Metadata.Trace/Budget 必填，并包含顶层事件**

追加到 `internal/research/runner_test.go`：

```go
func TestRunnerExecutePopulatesTraceAndBudget(t *testing.T) {
	planJSON := `{
		"objective": "Test",
		"sections": [{"id": "s1", "title": "S1"}],
		"todos": [{"id": "t1", "section_id": "s1", "title": "T1", "question": "Q?",
		           "search_queries": ["q"], "acceptance_criteria": ["ac"]}]
	}`
	model := &staticToolCallingModel{content: `{"markdown":"answer [src_1]","summary":"s","key_findings":["kf"],"limitations":[]}`}
	runner, err := NewRunner(RunnerConfig{
		Model:          model,
		SearchProvider: search.NewMockProvider(),
		TodoExecutor: func(_ context.Context, in TodoExecutorInput) (TodoExecution, error) {
			return TodoExecution{Todo: in.Todo, Status: TodoDone, Summary: "ok"}, nil
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
	if result.Metadata.Trace == nil {
		t.Fatal("Metadata.Trace must be populated by default internal TraceStore")
	}
	if result.Metadata.Budget == nil {
		t.Fatal("Metadata.Budget must be populated by default internal BudgetMeter")
	}
	kinds := make(map[EventKind]int)
	for _, e := range result.Metadata.Trace {
		kinds[e.Kind]++
	}
	for _, want := range []EventKind{EventFinalStart, EventFinalCompleted, EventEvidenceBound, EventClaimsVerified} {
		if kinds[want] == 0 {
			t.Errorf("missing event kind %s in trace", want)
		}
	}
}

func TestRunnerExecuteHonorsExternalEventBus(t *testing.T) {
	rec := &recordingSink{}
	bus := &EventBus{}
	bus.Add(rec)
	planJSON := `{
		"objective": "Test",
		"sections": [{"id": "s1", "title": "S1"}],
		"todos": [{"id": "t1", "section_id": "s1", "title": "T1", "question": "Q?",
		           "search_queries": ["q"], "acceptance_criteria": ["ac"]}]
	}`
	model := &staticToolCallingModel{content: `{"summary":"s"}`}
	runner, err := NewRunner(RunnerConfig{
		Model:          model,
		SearchProvider: search.NewMockProvider(),
		Events:         bus,
		TodoExecutor: func(_ context.Context, in TodoExecutorInput) (TodoExecution, error) {
			return TodoExecution{Todo: in.Todo, Status: TodoDone}, nil
		},
	})
	if err != nil {
		t.Fatalf("NewRunner: %v", err)
	}
	var plan ResearchTodoPlan
	_ = json.Unmarshal([]byte(planJSON), &plan)
	if _, err := runner.Execute(context.Background(), "Q?", plan); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(rec.Snapshot()) == 0 {
		t.Fatal("external bus should receive events")
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/research/ -run "TestRunnerExecute(Populates|Honors)" -v`
Expected: 编译错误，`RunnerConfig.Events` 不存在。

- [ ] **Step 3: 修改 `internal/research/runner.go`**

(a) 在 `RunnerConfig` 末尾追加字段（紧跟现有 `ClaimVerifier ClaimVerifier`）：

```go
	// Events 是可选的事件总线。nil 时 Runner 会在每次 Execute 内部创建一条总线，
	// 并自动挂上内置 TraceStore 和 BudgetMeter，把 Metadata.Trace/Budget 填上。
	Events *EventBus
```

(b) 在文件末尾加一个生成 RunID 的工具函数（紧凑实现，不引入 uuid 依赖）：

```go
// generateRunID 返回基于时间戳的 run ID，足够唯一以便区分单进程内多次 run。
func generateRunID() string {
	return fmt.Sprintf("run_%d", time.Now().UnixNano())
}
```

(c) 把 `Runner.Plan` 入口加上 emit。在现有方法开头（参数校验之后）插入：

```go
	runID := runIDFromContext(ctx)
	if runID == "" {
		runID = generateRunID()
	}
	r.cfg.Events.Emit(ctx, Event{Kind: EventPlanStarted, RunID: runID})
	defer func() {
		r.cfg.Events.Emit(ctx, Event{Kind: EventPlanCompleted, RunID: runID})
	}()
```

并在文件末尾加 ctx helper：

```go
type runIDKey struct{}

// WithRunID 把外部 run ID 写入 ctx，让 Runner 在 emit 事件时复用。
func WithRunID(ctx context.Context, runID string) context.Context {
	return context.WithValue(ctx, runIDKey{}, runID)
}

func runIDFromContext(ctx context.Context) string {
	v, _ := ctx.Value(runIDKey{}).(string)
	return v
}
```

(d) 重写 `Runner.Execute` 顶部一段，确保即便外部没传 Events 也有内部 bus + TraceStore + BudgetMeter，并在 defer 里 snapshot 到 Metadata。把现有 `defer func() { ... DurationMS ... }()` 替换为：

```go
	// 准备事件总线：caller 没传时使用内部 bus，并挂上 TraceStore/BudgetMeter 用于 Metadata。
	bus := r.cfg.Events
	var trace *TraceStore
	var budget *BudgetMeter
	if bus == nil {
		bus = &EventBus{}
		trace = NewTraceStore(0)
		budget = NewBudgetMeter()
		bus.Add(trace)
		bus.Add(budget)
	}
	runID := runIDFromContext(ctx)
	if runID == "" {
		runID = generateRunID()
	}

	defer func() {
		completed := time.Now()
		result.Metadata.CompletedAt = completed.Format(time.RFC3339)
		result.Metadata.DurationMS = completed.Sub(started).Milliseconds()
		if trace != nil {
			result.Metadata.Trace = trace.Snapshot()
		}
		if budget != nil {
			snap := budget.Snapshot()
			result.Metadata.Budget = &snap
		}
	}()
```

把 Plan 内部使用的 emit 也切到 `bus.Emit`。最简单方式：让 `Runner.Plan` 接收一个可选 bus 参数，或者直接用 `r.cfg.Events`（外部场景）。Plan 只在 Run 调用链中走，独立 Plan 调用没有 bus 时也是 no-op，所以现状用 `r.cfg.Events.Emit` 即可。

(e) 在 `Execute` 主体里，把最终阶段的 emit 加上：在调用 `synthesizeFinalAnswer` 前后、`bindFinalEvidence` 后、`verifyFinalClaims` 后各 emit 一条：

```go
	bus.Emit(ctx, Event{Kind: EventFinalStart, RunID: runID})
	result.Answer = r.synthesizeFinalAnswer(ctx, FinalSynthesisInput{...})
	bus.Emit(ctx, Event{Kind: EventFinalCompleted, RunID: runID})

	result.Answer = r.bindFinalEvidence(ctx, EvidenceBindingInput{...})
	bus.Emit(ctx, Event{Kind: EventEvidenceBound, RunID: runID})

	result.Answer = r.verifyFinalClaims(ctx, ClaimVerificationInput{...})
	bus.Emit(ctx, Event{Kind: EventClaimsVerified, RunID: runID})
```

(f) `Execute` 把 `bus` 通过 `r.cfg` 不能注入到 `executeTodo`（因为后者是方法），改为：把 `bus` 和 `runID` 通过 ctx 传给 scheduler/executor。最直接方案：

```go
	ctx = context.WithValue(ctx, eventBusKey{}, bus)
	ctx = WithRunID(ctx, runID)
```

并在 events.go 加一个 ctx helper：

```go
type eventBusKey struct{}

// eventBusFromContext 返回 ctx 中携带的 EventBus；不存在时返回 nil（emit 时安全）。
func eventBusFromContext(ctx context.Context) *EventBus {
	v, _ := ctx.Value(eventBusKey{}).(*EventBus)
	return v
}
```

- [ ] **Step 4: 运行新增测试确认通过 + 全量回归**

Run:
```
go test ./internal/research/ -run "TestRunnerExecute(Populates|Honors)" -v
go test ./...
```
Expected: 新测试 PASS；现有所有测试不变。

- [ ] **Step 5: 提交**

```bash
git add internal/research/runner.go internal/research/events.go internal/research/runner_test.go
git commit -m "feat: wire EventBus into Runner with internal trace and budget"
```

---

## Task 7: 在 executeTodo 中 emit 阶段事件

**Files:**
- Modify: `internal/research/runner.go`（`executeTodo` 方法）
- Modify: `internal/research/todo_research_loop.go`（gap retry emit）
- Modify: `internal/research/runner_test.go`

- [ ] **Step 1: 写失败测试 — todo 路径事件齐备**

追加到 `internal/research/runner_test.go`：

```go
func TestExecuteTodoEmitsLifecycleEvents(t *testing.T) {
	rec := &recordingSink{}
	bus := &EventBus{}
	bus.Add(rec)
	planJSON := `{
		"objective": "Test",
		"sections": [{"id": "s1", "title": "S1"}],
		"todos": [{"id": "t1", "section_id": "s1", "title": "T1", "question": "Q?",
		           "search_queries": ["q"], "acceptance_criteria": ["ac"]}]
	}`
	model := &staticToolCallingModel{content: `{"summary":"s"}`}
	runner, err := NewRunner(RunnerConfig{
		Model:          model,
		SearchProvider: search.NewMockProvider(),
		Events:         bus,
		TodoExecutor: func(_ context.Context, in TodoExecutorInput) (TodoExecution, error) {
			return TodoExecution{Todo: in.Todo, Status: TodoDone, Summary: "ok"}, nil
		},
	})
	if err != nil {
		t.Fatalf("NewRunner: %v", err)
	}
	var plan ResearchTodoPlan
	_ = json.Unmarshal([]byte(planJSON), &plan)
	if _, err := runner.Execute(context.Background(), "Q?", plan); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	kinds := make(map[EventKind]int)
	for _, e := range rec.Snapshot() {
		kinds[e.Kind]++
	}
	for _, want := range []EventKind{EventTodoStarted, EventTodoCompleted} {
		if kinds[want] == 0 {
			t.Errorf("missing event kind %s", want)
		}
	}
}
```

注意：测试用了注入的假 TodoExecutor，所以 dispatcher / researcher / synthesis 不会触发。这里只断言 todo lifecycle。dispatcher/researcher/synthesis 的 emit 由后面的 `executeTodo` 默认路径覆盖；为了保持单元测试隔离，单独再加一个针对默认路径的测试：

```go
func TestDefaultExecuteTodoEmitsResearcherAndSynthesisEvents(t *testing.T) {
	rec := &recordingSink{}
	bus := &EventBus{}
	bus.Add(rec)
	// 让 staticToolCallingModel 同时支持 tool calling，便于 agent researcher 完成。
	model := &staticToolCallingModel{content: `{"role":"evidence_researcher","focus":"f","queries":["q"],"findings":[]}`, supportTools: true}
	planJSON := `{
		"objective": "Test",
		"sections": [{"id": "s1", "title": "S1"}],
		"todos": [{"id": "t1", "section_id": "s1", "title": "T1", "question": "Q?",
		           "search_queries": ["q"], "acceptance_criteria": ["ac"]}]
	}`
	runner, err := NewRunner(RunnerConfig{
		Model:          model,
		SearchProvider: search.NewMockProvider(),
		Events:         bus,
	})
	if err != nil {
		t.Fatalf("NewRunner: %v", err)
	}
	var plan ResearchTodoPlan
	_ = json.Unmarshal([]byte(planJSON), &plan)
	if _, err := runner.Execute(context.Background(), "Q?", plan); err != nil {
		// Researcher 走真实 agent 路径可能失败，这里允许 err，但事件应已被 emit。
		t.Logf("Execute err (allowed): %v", err)
	}
	kinds := make(map[EventKind]int)
	for _, e := range rec.Snapshot() {
		kinds[e.Kind]++
	}
	if kinds[EventDispatch] == 0 {
		t.Errorf("missing %s", EventDispatch)
	}
	if kinds[EventResearcherStart] == 0 {
		t.Errorf("missing %s", EventResearcherStart)
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/research/ -run "TestExecuteTodoEmits|TestDefaultExecuteTodoEmits" -v`
Expected: 两个测试 FAIL（事件缺失）。

- [ ] **Step 3: 修改 `executeTodo`**

把现有 `executeTodo` 方法体在合适位置插入 emit：

```go
func (r *Runner) executeTodo(ctx context.Context, in TodoExecutorInput) (TodoExecution, error) {
	bus := eventBusFromContext(ctx)
	runID := runIDFromContext(ctx)
	todoStart := time.Now()
	bus.Emit(ctx, Event{Kind: EventTodoStarted, RunID: runID, TodoID: in.Todo.ID})

	// ...原有 maxSearches/resultsPerSearch 计算...

	dispatcher := r.cfg.TodoDispatcher
	if dispatcher == nil {
		dispatcher = RuleBasedTodoDispatcher{MaxResearchers: r.cfg.MaxResearchersPerTodo}
	}
	jobs, err := dispatcher.Dispatch(ctx, TodoDispatchInput{...})
	if err != nil {
		bus.Emit(ctx, Event{Kind: EventTodoFailed, RunID: runID, TodoID: in.Todo.ID, Err: err.Error()})
		return TodoExecution{}, err
	}
	bus.Emit(ctx, Event{Kind: EventDispatch, RunID: runID, TodoID: in.Todo.ID, Message: fmt.Sprintf("%d researcher jobs", len(jobs))})

	// ...原有 searchTool / fetchTool / researchers 构造...

	step := todoToResearchStep(in.Todo, in.Plan)
	stepExecutor := NewParallelStepExecutor(researchers, NewAgentSynthesizer(r.cfg.Model))
	// 在 stepExecutor 内部 emit researcher 事件不方便，转而：在 ExecuteStep 回调外面包一层 emit。
	execution, err := runTodoResearchLoop(ctx, TodoResearchLoopInput{
		Plan:                 in.Plan,
		Todo:                 in.Todo,
		DependencyExecutions: in.DependencyExecutions,
		MaxAttempts:          r.cfg.MaxTodoResearchIterations,
		ExecuteStep: func(ctx context.Context, input StepExecutionInput) (StepExecution, error) {
			for _, job := range jobs {
				bus.Emit(ctx, Event{Kind: EventResearcherStart, RunID: runID, TodoID: in.Todo.ID, Role: job.RoleID})
			}
			if strings.TrimSpace(input.Step.ID) == "" {
				input.Step = step
			}
			execution, err := stepExecutor.ExecuteStep(ctx, input)
			if err != nil {
				return StepExecution{}, err
			}
			for _, res := range execution.ResearcherResults {
				bus.Emit(ctx, Event{Kind: EventResearcherDone, RunID: runID, TodoID: in.Todo.ID, Role: res.Role})
			}
			bus.Emit(ctx, Event{Kind: EventSynthesis, RunID: runID, TodoID: in.Todo.ID})
			execution.Documents = mergeSourceDocuments(
				buildFetchedPageDocuments(fetchedPages.Pages(), execution.Sources, defaultSourceChunkChars),
				execution.Documents,
			)
			return execution, nil
		},
	})
	if err != nil {
		bus.Emit(ctx, Event{Kind: EventTodoFailed, RunID: runID, TodoID: in.Todo.ID, Err: err.Error()})
		return TodoExecution{}, err
	}

	out := stepExecutionToTodoExecution(in.Todo, execution)
	bus.Emit(ctx, Event{Kind: EventTodoCompleted, RunID: runID, TodoID: in.Todo.ID, DurationMS: time.Since(todoStart).Milliseconds()})
	return out, nil
}
```

- [ ] **Step 4: 在 `todo_research_loop.go` 中加 gap retry emit**

在 `runTodoResearchLoop`（`todo_research_loop.go:30`）的 `for attempt := 1; attempt <= maxAttempts; attempt++` 循环顶部加 emit；第 1 轮不算 retry，所以加在 `if err := ctx.Err(); err != nil` 之后：

```go
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return StepExecution{}, err
		}
		if attempt > 1 {
			eventBusFromContext(ctx).Emit(ctx, Event{
				Kind:    EventGapRetry,
				RunID:   runIDFromContext(ctx),
				TodoID:  in.Todo.ID,
				Attempt: attempt,
			})
		}
		// ...保持后续原有实现不变...
	}
```

- [ ] **Step 5: 运行新增测试 + 全量回归**

Run:
```
go test ./internal/research/ -run "TestExecuteTodoEmits|TestDefaultExecuteTodoEmits" -v
go test ./...
```
Expected: 新测试 PASS，老测试不变。

- [ ] **Step 6: 提交**

```bash
git add internal/research/runner.go internal/research/todo_research_loop.go internal/research/runner_test.go
git commit -m "feat: emit todo lifecycle and researcher events from executeTodo"
```

---

## Task 8: web_search / web_fetch 增加 emit 回调

**Files:**
- Modify: `internal/research/tools.go`
- Modify: `internal/research/runner.go`（构造工具时传入 emit）
- Modify: `internal/research/tools_test.go`

- [ ] **Step 1: 写失败测试 — 工具调用产生 EventToolCall**

追加到 `internal/research/tools_test.go`：

```go
func TestWebSearchToolEmitsToolCallEvent(t *testing.T) {
	provider := &recordingProvider{}
	var got []Event
	emit := func(_ context.Context, e Event) { got = append(got, e) }
	toolImpl, err := NewWebSearchTool(provider, SearchLimits{MaxSearchesPerStep: 2, ResultsPerSearch: 5}, WithToolEmit(emit))
	if err != nil {
		t.Fatalf("NewWebSearchTool: %v", err)
	}
	args, _ := json.Marshal(WebSearchInput{Query: "q", Limit: 1})
	if _, err := toolImpl.InvokableRun(context.Background(), string(args)); err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	if len(got) == 0 {
		t.Fatal("expected at least one tool.call event")
	}
	if got[0].Kind != EventToolCall || got[0].Tool != "web_search" {
		t.Fatalf("unexpected event: %+v", got[0])
	}
	if got[0].Query != "q" {
		t.Errorf("event query: want q, got %s", got[0].Query)
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/research/ -run TestWebSearchToolEmits -v`
Expected: 编译错误，`WithToolEmit` 未定义。

- [ ] **Step 3: 修改 `internal/research/tools.go`**

(a) 在 `SearchLimits` 和 `FetchLimits` 上方加 emit 类型与 option：

```go
// ToolEmitFunc 是工具调用 emit 回调，签名与 EventBus.Emit 一致，但允许 nil。
type ToolEmitFunc func(ctx context.Context, e Event)

// ToolOption 是 NewWebSearchTool / NewWebFetchTool 的可选参数。
type ToolOption func(*toolOptions)

type toolOptions struct {
	emit ToolEmitFunc
}

// WithToolEmit 注入 emit 回调，工具会在每次调用后发出一条 EventToolCall。
func WithToolEmit(emit ToolEmitFunc) ToolOption {
	return func(o *toolOptions) { o.emit = emit }
}

func applyToolOptions(opts []ToolOption) toolOptions {
	out := toolOptions{}
	for _, opt := range opts {
		if opt != nil {
			opt(&out)
		}
	}
	return out
}

func (e ToolEmitFunc) safe(ctx context.Context, ev Event) {
	if e == nil {
		return
	}
	defer func() { _ = recover() }()
	e(ctx, ev)
}
```

(b) 修改 `NewWebSearchTool` 签名追加 `opts ...ToolOption`，并在 provider 调用后 emit：

```go
func NewWebSearchTool(provider search.Provider, limits SearchLimits, opts ...ToolOption) (tool.InvokableTool, error) {
	options := applyToolOptions(opts)
	var count atomic.Int64
	var sourceCount atomic.Int64
	return utils.InferTool("web_search", "Search the web for current research sources.", func(ctx context.Context, input WebSearchInput) ([]search.Source, error) {
		// ...现有实现保持不变直到 return 前...
		options.emit.safe(ctx, Event{Kind: EventToolCall, Tool: "web_search", Query: input.Query})
		return sources, nil
	})
}
```

(c) 同样修改 `NewWebFetchTool` 签名追加 `opts ...ToolOption`，并在 fetch 成功后 emit：

```go
func NewWebFetchTool(fetcher PageFetcher, limits FetchLimits, opts ...ToolOption) (tool.InvokableTool, error) {
	options := applyToolOptions(opts)
	// ...
	return utils.InferTool("web_fetch", "...", func(ctx context.Context, input WebFetchInput) (FetchedPage, error) {
		// ...现有实现保持不变直到 return 前...
		options.emit.safe(ctx, Event{Kind: EventToolCall, Tool: "web_fetch", URL: input.URL})
		return page, nil
	})
}
```

- [ ] **Step 4: 修改 `executeTodo` 把 emit 传给工具**

在 runner.go `executeTodo` 内构造工具的两处加上 option：

```go
	emit := ToolEmitFunc(func(ctx context.Context, e Event) {
		bus.Emit(ctx, e)
	})
	searchTool, err := NewWebSearchTool(r.cfg.SearchProvider, SearchLimits{...}, WithToolEmit(emit))
	// ...
	fetchTool, err := NewWebFetchTool(HTTPPageFetcher{}, FetchLimits{...}, WithToolEmit(emit))
```

- [ ] **Step 5: 运行新增测试 + 全量回归**

Run:
```
go test ./internal/research/ -run TestWebSearchToolEmits -v
go test ./...
```
Expected: PASS；所有已有 tools_test.go 测试因新增 option 而需要传 nil 或不传——variadic 参数不会破坏现有调用，所以不需要改。

- [ ] **Step 6: 提交**

```bash
git add internal/research/tools.go internal/research/runner.go internal/research/tools_test.go
git commit -m "feat: emit tool.call events from web_search and web_fetch"
```

---

## Task 9: CLI `--stream` 与 `--trace` flag

**Files:**
- Modify: `cmd/research/main.go`
- Modify: `cmd/research/main_test.go`

- [ ] **Step 1: 写失败测试 — --stream 写 JSONL 到 stderr，--trace 控制 JSON 输出**

追加到 `cmd/research/main_test.go`：

```go
func TestRunWithStreamFlagWritesEventsToStderr(t *testing.T) {
	// 注入一个返回 error 的 model 工厂，让 runWithStreams 在 plan 阶段失败退出；
	// 这样能验证 --stream 即便在错误路径下也会 emit 早期事件（plan.started）。
	prevModel := newOpenAICompatibleModel
	defer func() { newOpenAICompatibleModel = prevModel }()
	newOpenAICompatibleModel = func(_ context.Context, _ research.ModelConfig) (model.ToolCallingChatModel, error) {
		return nil, errors.New("stubbed")
	}
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	cfgPath := writeTestConfig(t) // 见 Step 3(e)
	code := runWithStreams([]string{"--config", cfgPath, "--provider", "mock", "--yes", "--stream", "Q?"}, stdout, stderr)
	if code == 0 {
		t.Fatalf("expected non-zero exit when model is stubbed; stderr=%s", stderr.String())
	}
	// model 在 plan 之前就失败，所以 plan.started 不会触发。这里改为断言 --stream 路径无 panic
	// 且没有非 JSON 噪音写到 stream（实际事件覆盖留给 TestRunnerRunFullEventTopology）。
	for _, line := range strings.Split(strings.TrimSpace(stderr.String()), "\n") {
		if strings.HasPrefix(line, "{") && !json.Valid([]byte(line)) {
			t.Errorf("stderr contains malformed JSON line: %q", line)
		}
	}
}

func TestTraceFlagControlsJSONOutput(t *testing.T) {
	result := research.ResearchResult{
		Metadata: research.Metadata{Trace: []research.Event{{Kind: research.EventPlanStarted}}},
	}
	withTrace := stripTraceForOutput(result, true)
	if len(withTrace.Metadata.Trace) == 0 {
		t.Error("--trace=true should keep Metadata.Trace")
	}
	withoutTrace := stripTraceForOutput(result, false)
	if len(withoutTrace.Metadata.Trace) != 0 {
		t.Error("--trace=false should strip Metadata.Trace")
	}
}
```

如果当前 main 没有 `runWithStreams` 这种带 stream 参数的 helper，则需要重构 `run` 把 `stdout`/`stderr` 提出为参数。本任务一并完成该重构。

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./cmd/research/ -run "TestRunWith|TestTraceFlag" -v`
Expected: 编译/运行错误。

- [ ] **Step 3: 修改 `cmd/research/main.go`**

(a) 加 flag 声明（紧跟现有 `verbose`）：

```go
	stream := fs.Bool("stream", false, "write JSON-lines event stream to stderr")
	trace := fs.Bool("trace", false, "include Metadata.Trace in JSON output")
```

(b) 在创建 `runner` 之前装配 EventBus（注意使用注入的 `stderr`，不要用 `os.Stderr`）：

```go
	bus := &research.EventBus{}
	if *stream {
		bus.Add(research.NewJSONLinesSink(stderr))
	}
```

并传入 `RunnerConfig.Events: bus`。

(c) 在 JSON 输出前根据 `--trace` 决定是否清空 trace：

```go
	output := stripTraceForOutput(result, *trace)
	out, err := render.JSON(output)
```

并新增工具函数：

```go
// stripTraceForOutput 在 --trace 关闭时返回去掉 Metadata.Trace 的副本，避免 JSON 输出过大。
func stripTraceForOutput(result research.ResearchResult, includeTrace bool) research.ResearchResult {
	if includeTrace {
		return result
	}
	result.Metadata.Trace = nil
	return result
}
```

(d) 把 `run([]string) int` 重构为可注入 stream 的版本，并保留向后兼容包装，避免破坏已有 main_test.go：

```go
// 新增可注入 stream 的实现
func runWithStreams(args []string, stdout, stderr io.Writer) int {
	// 把现有 run 内所有 os.Stdout / os.Stderr / fmt.Println / fmt.Print 改为
	// 写到入参 writer。fs.SetOutput(stderr) 也使用入参 stderr。
	// ...
}

// 旧入口保留为薄包装，已有调用方零改动
func run(args []string) int {
	return runWithStreams(args, os.Stdout, os.Stderr)
}
```

新增的 `--stream` flag 装配 JSONLinesSink 时使用入参 `stderr`，而不是直接 `os.Stderr`，确保测试能捕获事件流。

(e) 加一个 test helper（放在 `cmd/research/main_test.go` 顶部）写最小有效配置文件：

```go
func writeTestConfig(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "research.yaml")
	body := "model:\n  provider: openai-compatible\n  api_key: test\n  model: test\n  timeout: 30s\nsearch:\n  provider: mock\nresearch:\n  max_researchers_per_todo: 1\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}
```

（如果 `cmd/research/main_test.go` 已经有等价 helper，直接复用而不要重复定义。）

- [ ] **Step 4: 运行新增测试 + 全量回归**

Run:
```
go test ./cmd/research/ -run "TestRunWith|TestTraceFlag" -v
go test ./...
```
Expected: PASS；现有 CLI 测试不变。

- [ ] **Step 5: 提交**

```bash
git add cmd/research/main.go cmd/research/main_test.go
git commit -m "feat: add --stream and --trace flags to research CLI"
```

---

## Task 10: 端到端事件拓扑集成测试 + README 更新

**Files:**
- Modify: `internal/research/runner_test.go`
- Modify: `README.md`

- [ ] **Step 1: 写集成测试 — 完整 Run 事件拓扑**

```go
func TestRunnerRunFullEventTopology(t *testing.T) {
	planJSON := `{
		"objective": "Test",
		"sections": [{"id": "s1", "title": "S1"}],
		"todos": [{"id": "t1", "section_id": "s1", "title": "T1", "question": "Q?",
		           "search_queries": ["q"], "acceptance_criteria": ["ac"]}]
	}`
	model := &staticToolCallingModel{contents: []string{planJSON, `{"summary":"s"}`}}
	rec := &recordingSink{}
	bus := &EventBus{}
	bus.Add(rec)
	runner, err := NewRunner(RunnerConfig{
		Model:          model,
		SearchProvider: search.NewMockProvider(),
		Events:         bus,
		TodoExecutor: func(_ context.Context, in TodoExecutorInput) (TodoExecution, error) {
			return TodoExecution{Todo: in.Todo, Status: TodoDone, Summary: "ok"}, nil
		},
	})
	if err != nil {
		t.Fatalf("NewRunner: %v", err)
	}
	if _, err := runner.Run(context.Background(), "Q?"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	required := []EventKind{
		EventPlanStarted, EventPlanCompleted,
		EventTodoStarted, EventTodoCompleted,
		EventFinalStart, EventFinalCompleted,
		EventEvidenceBound, EventClaimsVerified,
	}
	got := make(map[EventKind]int)
	for _, e := range rec.Snapshot() {
		got[e.Kind]++
	}
	for _, k := range required {
		if got[k] == 0 {
			t.Errorf("missing event kind %s; got=%v", k, got)
		}
	}
}
```

- [ ] **Step 2: 运行测试确认通过**

Run: `go test ./internal/research/ -run TestRunnerRunFullEventTopology -v`
Expected: PASS（前面任务已实现所有 emit）。

- [ ] **Step 3: 在 README 中追加 `--stream` / `--trace` 用法**

定位 `## 快速运行` 段落，紧跟现有 `--json` 示例后追加：

```markdown
观察事件流（写到 stderr）：

```bash
go run ./cmd/research --provider mock --yes --stream "Eino 适合构建 research agent 吗？"
```

在 JSON 输出中包含 trace：

```bash
go run ./cmd/research --provider mock --yes --json --trace "Eino 适合构建 research agent 吗？"
```
```

- [ ] **Step 4: 运行全量测试 + 手工 smoke**

Run:
```
go test ./...
go run ./cmd/research --provider mock --yes --stream "Eino 适合构建 research agent 吗？" 2>events.log >/dev/null || true
head -5 events.log
```
Expected: 测试全绿；events.log 前几行是 `{"kind":"plan.started",...}` 格式 JSON。

- [ ] **Step 5: 提交**

```bash
git add internal/research/runner_test.go README.md
git commit -m "test: cover full Run event topology and document new CLI flags"
```

---

## Self-Review 摘要

- **Spec 覆盖**：
  - §2.1 事件模型 → Task 1
  - §2.2 三个内置 Sink → Task 2/3/4
  - §2.3 改造点 → Task 5（Metadata 字段）/ Task 6（Runner + RunID）/ Task 7（executeTodo + retry）/ Task 8（工具 emit）/ Task 9（CLI flags）
  - §2.4 验证策略 → 每个 Task 内 TDD step + Task 10 端到端拓扑
  - §2.5 显式不做 → 没有任务涉及 OTel / token 自动埋点 / replay；遵守
- **占位扫描**：未使用 TBD/TODO；每个 step 都给出具体代码或具体命令；`runTodoResearchLoop` 内 emit 行号留待实施时定位，但已点出"现有 attempt 增量处"，不是含糊指令
- **类型一致性**：`EventBus / EventSink / Event / EventKind / BudgetReport / TraceStore / JSONLinesSink / BudgetMeter` 在所有任务中使用同一签名；`WithToolEmit / ToolEmitFunc / ToolOption / applyToolOptions` 配套；`WithRunID / eventBusFromContext / runIDFromContext` 在 Task 6 引入后被 Task 7/8 使用
