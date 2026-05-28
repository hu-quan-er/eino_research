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
	mu     sync.Mutex
	events []Event
	max    int
	warned bool
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
