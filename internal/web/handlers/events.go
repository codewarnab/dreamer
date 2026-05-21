package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
)

// sseSubscribeBuffer is the channel buffer size for SSE event subscribers.
// Matches the constant in internal/web to keep the two SSE paths consistent.
const sseSubscribeBuffer = 16

// Events returns an http.HandlerFunc for GET /api/events. Streams
// pipeline events to the client over SSE until the connection closes.
func Events(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if deps.Events == nil {
			http.Error(w, "events bus unavailable", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming unsupported", http.StatusInternalServerError)
			return
		}
		ch := deps.Events.Subscribe(sseSubscribeBuffer)
		defer deps.Events.Unsubscribe(ch)
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
}
