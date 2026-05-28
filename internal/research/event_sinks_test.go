package research

import (
	"context"
	"sync"
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
	// max=2，但截断后 Snapshot 会在前面再加一条警告，所以是 3 条。
	if len(snap) != 3 {
		t.Fatalf("want 3 events after truncation (1 warning + 2 retained), got %d", len(snap))
	}
	if snap[0].Kind != EventTraceTruncated {
		t.Fatalf("want first event to be trace.truncated, got %s", snap[0].Kind)
	}
	// 最早的 EventPlanStarted 被丢弃，保留最新两条。
	if snap[1].Kind != EventTodoStarted || snap[2].Kind != EventTodoCompleted {
		t.Fatalf("want [truncated, todo.started, todo.completed], got [%s, %s, %s]",
			snap[0].Kind, snap[1].Kind, snap[2].Kind)
	}
}

func TestTraceStoreMaxOnePreservesLatestEvent(t *testing.T) {
	store := NewTraceStore(1)
	ctx := context.Background()
	store.Emit(ctx, Event{Kind: EventPlanStarted})
	store.Emit(ctx, Event{Kind: EventTodoCompleted}) // 触发截断

	snap := store.Snapshot()
	if len(snap) != 2 {
		t.Fatalf("want 2 events (warning + retained), got %d", len(snap))
	}
	if snap[0].Kind != EventTraceTruncated {
		t.Fatalf("want first to be trace.truncated, got %s", snap[0].Kind)
	}
	if snap[1].Kind != EventTodoCompleted {
		t.Fatalf("want retained event to be todo.completed, got %s", snap[1].Kind)
	}
}

func TestTraceStoreConcurrentEmitAndSnapshot(t *testing.T) {
	store := NewTraceStore(0) // 默认 5000，避免触发截断
	ctx := context.Background()
	var wg sync.WaitGroup
	const emitters = 20
	const perEmitter = 50
	for i := 0; i < emitters; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < perEmitter; j++ {
				store.Emit(ctx, Event{Kind: EventTodoCompleted, RunID: "r1"})
			}
		}()
	}
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = store.Snapshot()
		}()
	}
	wg.Wait()

	final := store.Snapshot()
	if len(final) != emitters*perEmitter {
		t.Fatalf("want %d events, got %d", emitters*perEmitter, len(final))
	}
}
