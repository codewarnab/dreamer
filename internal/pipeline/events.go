package pipeline

import (
	"log/slog"
	"sync"
	"sync/atomic"
	"time"
)

// EventType enumerates SSE event names.
const (
	EventRunStart       = "run.start"
	EventRunDone        = "run.done"
	EventRunError       = "run.error"
	EventFindingApplied = "finding.applied"
	EventFindingUndone  = "finding.undone"
	EventFindingDismissed = "finding.dismissed"
	EventFindingResolved  = "finding.resolved"
	EventConfigReload   = "config.reloaded"
	EventChatDeleted    = "chat.deleted"
	EventJobCreated     = "job.created"
	EventJobDeleted     = "job.deleted"
	EventJobRunStart    = "job.run.start"
	EventJobRunDone     = "job.run.done"
	EventJobPaused      = "job.paused"
	EventJobResumed     = "job.resumed"
	EventJobUpdated     = "job.updated"
)

// Event is one SSE-shaped notification. Payload is a JSON-serializable map.
type Event struct {
	Type    string         `json:"type"`
	At      time.Time      `json:"at"`
	Payload map[string]any `json:"payload,omitempty"`
}

// EventBus is the in-process pub-sub used by the pipeline and HTTP
// handlers to flow events to SSE subscribers. Slow subscribers drop
// events (no back-pressure on the publisher). DroppedEvents returns
// the total number of events dropped across all subscribers.
type EventBus struct {
	mu          sync.RWMutex
	subscribers []chan Event
	drops       atomic.Int64
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
	for i, sub := range b.subscribers {
		if sub == ch {
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
			total := b.drops.Add(1)
			if total%100 == 1 {
				slog.Warn("eventbus: events dropped (slow subscriber)", "total", total)
			}
		}
	}
}

// DroppedEvents returns the total number of events dropped because
// subscriber buffers were full. Useful for operator observability.
func (b *EventBus) DroppedEvents() int64 {
	return b.drops.Load()
}
