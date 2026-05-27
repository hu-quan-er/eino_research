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
