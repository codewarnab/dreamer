package web

import (
	"encoding/json"
	"fmt"
	"net/http"

	"dreamer/internal/pipeline"
)

// StreamSSE writes events from bus to w until the request context is done.
func StreamSSE(w http.ResponseWriter, r *http.Request, bus *pipeline.EventBus) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	ch := bus.Subscribe(16)
	defer bus.Unsubscribe(ch)
	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case e, ok := <-ch:
			if !ok {
				return
			}
			data, err := json.Marshal(e.Payload)
			if err != nil {
				continue
			}
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", e.Type, data)
			flusher.Flush()
		}
	}
}
