package research

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
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

	snapA := a.Snapshot()
	snapB := b.Snapshot()

	if got := len(snapA); got != 1 {
		t.Fatalf("sink a: want 1 event, got %d", got)
	}
	if got := len(snapB); got != 1 {
		t.Fatalf("sink b: want 1 event, got %d", got)
	}

	// Assert event fidelity: RunID and Kind must be preserved on delivery.
	if snapA[0].Kind != EventPlanStarted {
		t.Errorf("sink a: want Kind=%q, got %q", EventPlanStarted, snapA[0].Kind)
	}
	if snapA[0].RunID != "r1" {
		t.Errorf("sink a: want RunID=%q, got %q", "r1", snapA[0].RunID)
	}

	// Assert Emit auto-sets At when not provided.
	if snapA[0].At.IsZero() {
		t.Errorf("sink a: Emit should auto-set At, but At is zero")
	}
}

// TestEventBusConcurrentEmitAndAdd stress-tests EventBus under concurrent Emit
// and Add calls. Run with -race to detect data races. It asserts that every
// emitted event is received by the sink that existed before the first Emit.
func TestEventBusConcurrentEmitAndAdd(t *testing.T) {
	const emitters = 50
	const adders = 5
	const eventsPerEmitter = 10

	bus := &EventBus{}

	// Anchor sink registered before any goroutine starts — it must receive all events.
	anchor := &recordingSink{}
	bus.Add(anchor)

	var wg sync.WaitGroup
	var totalEmitted atomic.Int64

	// Goroutines that emit events.
	for i := range emitters {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			for j := range eventsPerEmitter {
				bus.Emit(context.Background(), Event{
					Kind:  EventPlanStarted,
					RunID: fmt.Sprintf("run-%d-%d", n, j),
				})
				totalEmitted.Add(1)
			}
		}(i)
	}

	// Goroutines that add new sinks concurrently.
	for range adders {
		wg.Add(1)
		go func() {
			defer wg.Done()
			bus.Add(&recordingSink{})
		}()
	}

	wg.Wait()

	want := int(totalEmitted.Load())
	got := len(anchor.Snapshot())
	if got != want {
		t.Errorf("anchor sink: want %d events, got %d", want, got)
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
