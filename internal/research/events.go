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

// EventKind 常量定义 research 主流程中会被观测和统计的阶段边界。
const (
	// EventPlanStarted 表示 planner 阶段开始，通常发生在 Runner.Plan 入口。
	EventPlanStarted EventKind = "plan.started"
	// EventPlanCompleted 表示 planner 阶段结束；失败时上层会通过返回 error 表达。
	EventPlanCompleted EventKind = "plan.completed"
	// EventTodoStarted 表示某个 runnable todo 已被 scheduler 派给 executor。
	EventTodoStarted EventKind = "todo.started"
	// EventTodoCompleted 表示 todo executor 成功返回或返回非 failed 终态。
	EventTodoCompleted EventKind = "todo.completed"
	// EventTodoFailed 表示 todo executor 返回错误或显式 failed 状态。
	EventTodoFailed EventKind = "todo.failed"
	// EventTodoDispatched 表示 todo 已被拆成若干 researcher jobs。
	EventTodoDispatched EventKind = "todo.dispatched"
	// EventResearcherStarted 表示某个 researcher 角色开始执行。
	EventResearcherStarted EventKind = "researcher.started"
	// EventResearcherCompleted 表示某个 researcher 角色已有结果。
	EventResearcherCompleted EventKind = "researcher.completed"
	// EventToolCall 表示 web_search、web_fetch 等工具调用成功完成。
	EventToolCall EventKind = "tool.call"
	// EventSynthesisCompleted 表示 todo 内部 synthesizer 已完成本轮综合。
	EventSynthesisCompleted EventKind = "synthesis.completed"
	// EventGapRetry 表示 todo 因确定性 gap 检查未通过而进入下一轮深挖。
	EventGapRetry EventKind = "todo.retry"
	// EventFinalStarted 表示所有 todo 执行后开始最终全局综合。
	EventFinalStarted EventKind = "final.started"
	// EventFinalCompleted 表示最终全局综合阶段完成。
	EventFinalCompleted EventKind = "final.completed"
	// EventEvidenceBound 表示最终答案已完成 claim -> evidence 绑定。
	EventEvidenceBound EventKind = "evidence.bound"
	// EventClaimsVerified 表示最终 claim 已完成规则校验和降级处理。
	EventClaimsVerified EventKind = "claims.verified"
	// EventTraceTruncated 是 TraceStore 快照中插入的截断提示事件。
	EventTraceTruncated EventKind = "trace.truncated"
	// EventSectionStarted 表示某个 section 的归纳开始；section id 通过 Event.TodoID 承载。
	EventSectionStarted EventKind = "section.started"
	// EventSectionCompleted 表示某个 section 的归纳完成；Message 可记录是否走了确定性兜底。
	EventSectionCompleted EventKind = "section.completed"
)

// Event 是阶段边界发出的结构化事件。Kind、At、RunID 为必填字段；其他字段使用 omitempty。
type Event struct {
	// Kind 标识事件类型，也是 BudgetMeter/TraceStore 的主要分流依据。
	Kind EventKind `json:"kind"`
	// At 是事件发生时间；EventBus.Emit 会在为空时自动填当前时间。
	At time.Time `json:"at"`
	// RunID 用于把一次 research run 的所有事件串起来。
	RunID string `json:"run_id"`
	// TodoID 标识事件所属 todo；全局阶段事件通常为空。
	TodoID string `json:"todo_id,omitempty"`
	// Role 标识 researcher 角色，例如 evidence_researcher。
	Role string `json:"role,omitempty"`
	// Attempt 标识 todo 内 bounded retry 的轮次。
	Attempt int `json:"attempt,omitempty"`
	// DurationMS 是该阶段耗时，单位毫秒。
	DurationMS int64 `json:"duration_ms,omitempty"`
	// TokensIn 预留给模型 token 统计输入量。
	TokensIn int `json:"tokens_in,omitempty"`
	// TokensOut 预留给模型 token 统计输出量。
	TokensOut int `json:"tokens_out,omitempty"`
	// Tool 是工具名，例如 web_search 或 web_fetch。
	Tool string `json:"tool,omitempty"`
	// Query 是 web_search 的搜索 query。
	Query string `json:"query,omitempty"`
	// URL 是 web_fetch 或未来 URL 型工具的目标地址。
	URL string `json:"url,omitempty"`
	// Message 是事件的人类可读补充说明。
	Message string `json:"message,omitempty"`
	// Err 是失败事件的错误摘要。
	Err string `json:"error,omitempty"`
	// Payload 预留给不适合展开为顶层字段的结构化扩展信息。
	Payload json.RawMessage `json:"payload,omitempty"`
}

// EventSink 是事件接收端，所有 sink 实现都必须是 best-effort（不返回错误、自己处理失败）。
type EventSink interface {
	Emit(ctx context.Context, e Event)
}

// eventBusKey 是 context 中保存 EventBus 的私有 key 类型。
type eventBusKey struct{}

// withEventBus 把 bus 写入 ctx，供下游 executor/tool 在不改方法签名的前提下取用。
func withEventBus(ctx context.Context, bus *EventBus) context.Context {
	return context.WithValue(ctx, eventBusKey{}, bus)
}

// eventBusFromContext 返回 ctx 中携带的 EventBus；不存在时返回 nil（nil bus 的 Emit 安全）。
func eventBusFromContext(ctx context.Context) *EventBus {
	v, _ := ctx.Value(eventBusKey{}).(*EventBus)
	return v
}

// runIDKey 是 context 中保存 run ID 的私有 key 类型。
type runIDKey struct{}

// WithRunID 把外部 run ID 写入 ctx，让 Runner 在 emit 事件时复用（HTTP 服务可借此关联一次 run）。
func WithRunID(ctx context.Context, runID string) context.Context {
	return context.WithValue(ctx, runIDKey{}, runID)
}

// runIDFromContext 读取 ctx 中的 run ID；没有时返回空字符串，由调用方决定是否生成默认值。
func runIDFromContext(ctx context.Context) string {
	v, _ := ctx.Value(runIDKey{}).(string)
	return v
}

// generateRunID 返回基于时间戳的 run ID，足够唯一以便区分单进程内多次 run。
func generateRunID() string {
	return fmt.Sprintf("run_%d", time.Now().UnixNano())
}

// EventBus 把事件 fan-out 到所有 sink。nil 接收者安全：所有方法都是 no-op。
// Emit 内部对每个 sink 单独 recover，单个 sink panic 不影响其他 sink 和主路径。
type EventBus struct {
	// mu 保护 sinks，允许事件 emit 和动态 Add 在并发场景下安全执行。
	mu sync.Mutex
	// sinks 是所有事件接收端；Emit 时会复制切片，避免持锁调用外部代码。
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
