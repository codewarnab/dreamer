package web

import (
	"context"
	"testing"
	"time"

	"dreamer/internal/pipeline"
)

func TestActivityRing_PushSnapshot(t *testing.T) {
	r := NewActivityRing(5)
	r.Push(pipeline.Event{Type: "a"})
	r.Push(pipeline.Event{Type: "b"})
	snap := r.Snapshot()
	if len(snap) != 2 {
		t.Fatalf("len = %d, want 2", len(snap))
	}
	if snap[0].Type != "a" || snap[1].Type != "b" {
		t.Errorf("order = [%s,%s], want [a,b]", snap[0].Type, snap[1].Type)
	}
}

func TestActivityRing_TrimsAtCap(t *testing.T) {
	r := NewActivityRing(3)
	for i, n := range []string{"a", "b", "c", "d", "e"} {
		_ = i
		r.Push(pipeline.Event{Type: n})
	}
	snap := r.Snapshot()
	if len(snap) != 3 {
		t.Fatalf("len = %d, want 3", len(snap))
	}
	if snap[0].Type != "c" || snap[1].Type != "d" || snap[2].Type != "e" {
		t.Errorf("snap = %+v, want c,d,e", snap)
	}
}

func TestActivityRing_Bind_AppendsFromBus(t *testing.T) {
	bus := pipeline.NewEventBus()
	r := NewActivityRing(10)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { r.Bind(ctx, bus); close(done) }()
	// Give Bind time to subscribe.
	time.Sleep(10 * time.Millisecond)
	bus.Publish(pipeline.Event{Type: "run.start", Payload: map[string]any{"p": "x"}})
	bus.Publish(pipeline.Event{Type: "run.done", Payload: map[string]any{"p": "x"}})

	// Poll briefly until both events arrive.
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if len(r.Snapshot()) >= 2 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	snap := r.Snapshot()
	if len(snap) != 2 {
		t.Fatalf("len = %d, want 2", len(snap))
	}
	if snap[0].Type != "run.start" || snap[1].Type != "run.done" {
		t.Errorf("order = [%s,%s]", snap[0].Type, snap[1].Type)
	}
	cancel()
	<-done
}
