package research

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sync"
	"time"
)

// defaultTraceMaxEvents 是 TraceStore 默认最大事件数。超过后最早事件被丢弃，并标记 truncated。
const defaultTraceMaxEvents = 5000

// TraceStore 是把事件累积在内存的 sink，供 Execute 结束后注入 Metadata.Trace。
type TraceStore struct {
	mu        sync.Mutex
	events    []Event
	max       int
	truncated bool // 一旦发生过截断永久为 true
}

// NewTraceStore 创建 TraceStore。max <= 0 时使用默认上限 5000。
func NewTraceStore(max int) *TraceStore {
	if max <= 0 {
		max = defaultTraceMaxEvents
	}
	return &TraceStore{max: max}
}

// Emit 追加事件；超过上限时丢弃最早事件，并标记 truncated。
// 不再向 events 写入哨兵——哨兵改由 Snapshot 在需要时前置。
func (s *TraceStore) Emit(_ context.Context, e Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, e)
	if len(s.events) > s.max {
		s.events = s.events[len(s.events)-s.max:]
		s.truncated = true
	}
}

// Snapshot 返回当前事件列表的深拷贝；调用方可以自由修改返回值，不会影响 store 内部状态。
// 如果曾发生过截断，返回的切片头部会有一条 EventTraceTruncated 警告。
func (s *TraceStore) Snapshot() []Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.truncated {
		out := make([]Event, 0, len(s.events)+1)
		out = append(out, Event{
			Kind:    EventTraceTruncated,
			At:      time.Now(),
			Message: "trace exceeded max events",
		})
		out = append(out, s.events...)
		return out
	}
	out := make([]Event, len(s.events))
	copy(out, s.events)
	return out
}

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
	case EventSynthesisCompleted, EventFinalCompleted, EventPlanCompleted:
		m.report.ModelCalls++
	}
	m.report.TokensIn += e.TokensIn
	m.report.TokensOut += e.TokensOut
	if e.Kind == EventFinalCompleted && e.DurationMS > 0 {
		// FinalCompleted 携带整次 run 的总耗时；上层 emit 时填了 DurationMS 才覆盖。
		m.report.DurationMS = e.DurationMS
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
