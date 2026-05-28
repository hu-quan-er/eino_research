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
