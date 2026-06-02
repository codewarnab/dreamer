package implicitstatustest

import (
	"io"
	"net/http"
)

// w.Write without preceding w.WriteHeader — should be flagged.
func handleBadWrite(w http.ResponseWriter, r *http.Request) {
	w.Write([]byte("hello")) // want "w\\.Write called without preceding w\\.WriteHeader; add explicit status code"
}

// io.WriteString without preceding w.WriteHeader — should be flagged.
func handleBadWriteString(w http.ResponseWriter, r *http.Request) {
	io.WriteString(w, "hello") // want "io\\.WriteString\\(w, \\.\\.\\.\\) called without preceding w\\.WriteHeader; add explicit status code"
}

// Write after an early return without WriteHeader — should be flagged.
func handleBadEarlyReturn(w http.ResponseWriter, r *http.Request) error {
	if r.Method != "GET" {
		return nil
	}
	w.Write([]byte("hello")) // want "w\\.Write called without preceding w\\.WriteHeader; add explicit status code"
	return nil
}

// Known false positive: http.Error calls WriteHeader internally, but the
// analyzer only tracks explicit w.WriteHeader. This is flagged because the
// heuristic is intentionally simple — it's info-level, not error-level.
func handleFalsePositiveHttpError(w http.ResponseWriter, r *http.Request) {
	http.Error(w, "bad", http.StatusBadRequest)
	w.Write([]byte("after error")) // want "w\\.Write called without preceding w\\.WriteHeader; add explicit status code"
}
