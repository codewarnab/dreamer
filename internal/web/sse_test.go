package web

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"dreamer/internal/pipeline"
)

func TestSSE_StreamsEvent(t *testing.T) {
	bus := pipeline.NewEventBus()
	w := httptest.NewRecorder()
	ctx, cancel := context.WithCancel(context.Background())
	r := httptest.NewRequest("GET", "/api/events", nil).WithContext(ctx)
	done := make(chan struct{})
	go func() {
		StreamSSE(w, r, bus)
		close(done)
	}()
	time.Sleep(10 * time.Millisecond)
	bus.Publish(pipeline.Event{Type: "run.done", Payload: map[string]any{"project": "p"}})
	time.Sleep(20 * time.Millisecond)
	cancel()
	<-done
	body := w.Body.String()
	if !strings.Contains(body, "event: run.done") {
		t.Fatalf("missing event line: %s", body)
	}
	if !strings.Contains(body, `"project":"p"`) {
		t.Fatalf("missing payload: %s", body)
	}
}
