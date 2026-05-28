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
	EventTodoDispatched      EventKind = "todo.dispatched"
	EventResearcherStarted   EventKind = "researcher.started"
	EventResearcherCompleted EventKind = "researcher.completed"
	EventToolCall            EventKind = "tool.call"
	EventSynthesisCompleted  EventKind = "synthesis.completed"
	EventGapRetry            EventKind = "todo.retry"
	EventFinalStarted        EventKind = "final.started"
	EventFinalCompleted  EventKind = "final.completed"
	EventEvidenceBound   EventKind = "evidence.bound"
	EventClaimsVerified  EventKind = "claims.verified"
	EventTraceTruncated  EventKind = "trace.truncated"
)

// Event 是阶段边界发出的结构化事件。Kind、At、RunID 为必填字段；其他字段使用 omitempty。
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
	mu    sync.Mutex
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
	b.mu.Lock()
	sinks := append([]EventSink(nil), b.sinks...)
	b.mu.Unlock()
	for _, sink := range sinks {
		safeEmit(ctx, sink, e)
	}
}

// safeEmit 调用单个 sink 并 recover 任何 panic，将恢复信息写到 stderr。
func safeEmit(ctx context.Context, sink EventSink, e Event) {
	defer func() {
		if r := recover(); r != nil {
			fmt.Fprintf(os.Stderr, "event sink panic recovered: %v\n", r)
		}
	}()
	sink.Emit(ctx, e)
}
