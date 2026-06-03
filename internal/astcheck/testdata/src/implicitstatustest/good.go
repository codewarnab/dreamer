package implicitstatustest

import (
	"io"
	"net/http"
)

// Handler with explicit WriteHeader before Write — not flagged.
func handleGood1(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("hello"))
}

// Handler with explicit WriteHeader (error status) — not flagged.
func handleGood2(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusInternalServerError)
	io.WriteString(w, "error")
}

// Handler using http.Error — not flagged (http.Error calls WriteHeader internally).
func handleGood3(w http.ResponseWriter, r *http.Request) {
	http.Error(w, "bad request", http.StatusBadRequest)
}

// Non-handler function — not flagged (no ResponseWriter param).
func notAHandler(data []byte) {
	_ = data
}

// Handler with WriteHeader in conditional — still flagged only if Write
// appears without any preceding WriteHeader in the function.
// Here WriteHeader is called before Write, so not flagged.
func handleGood4(w http.ResponseWriter, r *http.Request) {
	if r.Method == "GET" {
		w.WriteHeader(http.StatusOK)
	} else {
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
	w.Write([]byte("done"))
}

// Handler with no Write call at all — not flagged.
func handleGood5(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusNoContent)
}
