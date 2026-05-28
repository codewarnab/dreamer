package pipeline

import (
	"testing"
	"time"
)

func TestEventBus_PublishDeliversToSubscriber(t *testing.T) {
	bus := NewEventBus()
	ch := bus.Subscribe(4)
	defer bus.Unsubscribe(ch)
	bus.Publish(Event{Type: EventRunDone, Payload: map[string]any{"project": "x"}})
	select {
	case e := <-ch:
		if e.Type != EventRunDone {
			t.Fatalf("Type = %q, want %q", e.Type, EventRunDone)
		}
		if e.Payload["project"] != "x" {
			t.Fatalf("payload = %v", e.Payload)
		}
		if e.At.IsZero() {
			t.Fatalf("At unset, want auto-stamp")
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatalf("event not delivered")
	}
}

func TestEventBus_SlowSubscriberDropsOnFull(t *testing.T) {
	bus := NewEventBus()
	ch := bus.Subscribe(1) // buf=1
	defer bus.Unsubscribe(ch)
	bus.Publish(Event{Type: "a"})
	bus.Publish(Event{Type: "b"}) // dropped — buffer full, not draining
	// Drain one.
	select {
	case e := <-ch:
		if e.Type != "a" {
			t.Fatalf("got %q want a", e.Type)
		}
	case <-time.After(50 * time.Millisecond):
		t.Fatalf("no event")
	}
	// Buffer should now be empty (b was dropped, not buffered).
	select {
	case e := <-ch:
		t.Fatalf("unexpected event %+v — b should have been dropped", e)
	case <-time.After(20 * time.Millisecond):
		// pass
	}
}

func TestEventBus_UnsubscribeRemovesAndCloses(t *testing.T) {
	bus := NewEventBus()
	ch := bus.Subscribe(2)
	bus.Unsubscribe(ch)
	// Channel must be closed after Unsubscribe.
	_, ok := <-ch
	if ok {
		t.Fatalf("channel not closed after Unsubscribe")
	}
}

func TestEventBus_DroppedEventsCountsDrops(t *testing.T) {
	bus := NewEventBus()
	ch := bus.Subscribe(1) // buf=1
	defer bus.Unsubscribe(ch)
	if bus.DroppedEvents() != 0 {
		t.Fatalf("initial drops = %d, want 0", bus.DroppedEvents())
	}
	bus.Publish(Event{Type: "a"}) // fills buffer
	bus.Publish(Event{Type: "b"}) // dropped
	bus.Publish(Event{Type: "c"}) // dropped
	if got := bus.DroppedEvents(); got != 2 {
		t.Fatalf("drops = %d, want 2", got)
	}
}
