package handlers

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"dreamer/internal/pipeline"
)

// flushRecorder wraps httptest.ResponseRecorder and implements http.Flusher
// so StreamSSE can write events to it.
type flushRecorder struct {
	*httptest.ResponseRecorder
	mu sync.Mutex
}

func (f *flushRecorder) Flush() {}

func (f *flushRecorder) Body() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.ResponseRecorder.Body.String()
}

func TestEventsHandler_MethodNotAllowed(t *testing.T) {
	h := Events(Deps{Events: pipeline.NewEventBus()})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/events", nil)
	h(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", rec.Code)
	}
}

func TestEventsHandler_NoBus(t *testing.T) {
	h := Events(Deps{})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/events", nil)
	h(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
}

func TestEventsHandler_Streams(t *testing.T) {
	bus := pipeline.NewEventBus()
	h := Events(Deps{Events: bus})

	rec := &flushRecorder{ResponseRecorder: httptest.NewRecorder()}
	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodGet, "/api/events", nil).WithContext(ctx)

	done := make(chan struct{})
	go func() {
		h(rec, req)
		close(done)
	}()
	// Wait for the subscriber to register inside StreamSSE.
	time.Sleep(20 * time.Millisecond)
	bus.Publish(pipeline.Event{Type: "run.done", Payload: map[string]any{"project": "x"}})

	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if strings.Contains(rec.Body(), "event: run.done") {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("handler did not return after cancel")
	}
	body := rec.Body()
	if !strings.Contains(body, "event: run.done") {
		t.Fatalf("body missing event: run.done; got: %s", body)
	}
	if !strings.Contains(body, `"project":"x"`) {
		t.Fatalf("body missing payload; got: %s", body)
	}
	_ = io.Discard
}
