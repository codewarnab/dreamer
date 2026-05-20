package handlers

import (
	"encoding/json"
	"net/http"
)

// DaemonRestart returns an http.HandlerFunc for POST /api/daemon/restart.
// Triggers an injected shutdown hook so that systemd / Task Scheduler can
// respawn the process. The response is flushed before the hook fires so
// the client always receives confirmation.
func DaemonRestart(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if deps.RestartDaemon == nil {
			http.Error(w, "restart hook not configured", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":      true,
			"message": "daemon restart triggered",
		})
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		go func() { _ = deps.RestartDaemon() }()
	}
}
