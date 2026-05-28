package research

import (
	"context"
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
