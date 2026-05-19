package handlers

import (
	"net/http"

	"dreamer/internal/web"
)

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
		web.StreamSSE(w, r, deps.Events)
	}
}
