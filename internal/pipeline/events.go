package pipeline

import (
	"sync"
	"time"
)

// EventType enumerates SSE event names.
const (
	EventRunStart       = "run.start"
	EventRunDone        = "run.done"
	EventRunError       = "run.error"
	EventFindingApplied = "finding.applied"
	EventFindingUndone  = "finding.undone"
	EventFindingDismiss = "finding.dismissed"
	EventFindingResolve = "finding.resolved"
	EventConfigReload   = "config.reloaded"
)

// Event is one SSE-shaped notification. Payload is a JSON-serializable map.
type Event struct {
	Type    string         `json:"type"`
	At      time.Time      `json:"at"`
	Payload map[string]any `json:"payload,omitempty"`
}

// EventBus is the in-process pub-sub used by the pipeline and HTTP
// handlers to flow events to SSE subscribers. Slow subscribers drop
// events (no back-pressure on the publisher).
type EventBus struct {
	mu          sync.RWMutex
	subscribers []chan Event
}

func NewEventBus() *EventBus { return &EventBus{} }

func (b *EventBus) Subscribe(buf int) chan Event {
	ch := make(chan Event, buf)
	b.mu.Lock()
	b.subscribers = append(b.subscribers, ch)
	b.mu.Unlock()
	return ch
}

func (b *EventBus) Unsubscribe(ch chan Event) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for i, s := range b.subscribers {
		if s == ch {
			b.subscribers = append(b.subscribers[:i], b.subscribers[i+1:]...)
			close(ch)
			return
		}
	}
}

func (b *EventBus) Publish(e Event) {
	if e.At.IsZero() {
		e.At = time.Now().UTC()
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	for _, ch := range b.subscribers {
		select {
		case ch <- e:
		default: // drop on full
		}
	}
}
