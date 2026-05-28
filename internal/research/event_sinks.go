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
