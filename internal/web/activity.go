package web

import (
	"context"
	"sync"

	"dreamer/internal/pipeline"
)

// ActivityRing is a fixed-capacity in-memory buffer of the most recent
// pipeline events. The web dashboard reads a snapshot on first paint so
// the live_activity panel hydrates without waiting for the SSE stream.
type ActivityRing struct {
	mu       sync.RWMutex
	events   []pipeline.Event
	capacity int
}

// NewActivityRing returns a ring with the given capacity. A non-positive
// capacity is treated as 1.
func NewActivityRing(capacity int) *ActivityRing {
	if capacity <= 0 {
		capacity = 1
	}
	return &ActivityRing{capacity: capacity}
}

// Push appends an event, evicting the oldest when the ring is full.
func (a *ActivityRing) Push(event pipeline.Event) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.events = append(a.events, event)
	if len(a.events) > a.capacity {
		a.events = a.events[len(a.events)-a.capacity:]
	}
}

// Snapshot returns a copy of the buffered events in oldest-first order.
func (a *ActivityRing) Snapshot() []pipeline.Event {
	a.mu.RLock()
	defer a.mu.RUnlock()
	cp := make([]pipeline.Event, len(a.events))
	copy(cp, a.events)
	return cp
}

// Bind subscribes to bus and pushes every received event into the ring
// until ctx is done. Run as a goroutine.
func (a *ActivityRing) Bind(ctx context.Context, bus *pipeline.EventBus) {
	ch := bus.Subscribe(16)
	defer bus.Unsubscribe(ch)
	for {
		select {
		case <-ctx.Done():
			return
		case event, ok := <-ch:
			if !ok {
				return
			}
			a.Push(event)
		}
	}
}
