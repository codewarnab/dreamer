package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCSRF_PostWithoutTokenIsRejected(t *testing.T) {
	tok := MintCSRFToken()
	h := CSRFMiddleware(tok, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(204)
	}))
	r := httptest.NewRequest("POST", "/api/x", strings.NewReader("{}"))
	r.Header.Set("Origin", "http://127.0.0.1:7777")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatalf("status = %d, want 403", w.Code)
	}
}

func TestCSRF_PostWithTokenAndLoopbackOriginAccepted(t *testing.T) {
	tok := MintCSRFToken()
	h := CSRFMiddleware(tok, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	r := httptest.NewRequest("POST", "/api/x", strings.NewReader("{}"))
	r.Header.Set("Origin", "http://127.0.0.1:7777")
	r.Header.Set("X-Dreamer-CSRF", tok)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 204 {
		t.Fatalf("status = %d, want 204", w.Code)
	}
}

func TestCSRF_NonLoopbackOriginRejected(t *testing.T) {
	tok := MintCSRFToken()
	h := CSRFMiddleware(tok, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	r := httptest.NewRequest("POST", "/api/x", strings.NewReader("{}"))
	r.Header.Set("Origin", "http://evil.example")
	r.Header.Set("X-Dreamer-CSRF", tok)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatalf("status = %d, want 403", w.Code)
	}
}

func TestCSRF_PostWithoutOriginOrRefererRejected(t *testing.T) {
	tok := MintCSRFToken()
	h := CSRFMiddleware(tok, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	r := httptest.NewRequest("POST", "/api/x", strings.NewReader("{}"))
	r.Header.Set("X-Dreamer-CSRF", tok)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatalf("status = %d, want 403", w.Code)
	}
}

func TestCSRF_PostWithRefererFallback(t *testing.T) {
	tok := MintCSRFToken()
	h := CSRFMiddleware(tok, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	r := httptest.NewRequest("POST", "/api/x", strings.NewReader("{}"))
	r.Header.Set("X-Dreamer-CSRF", tok)
	r.Header.Set("Referer", "http://127.0.0.1:7777/settings")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 204 {
		t.Fatalf("status = %d, want 204", w.Code)
	}
}

func TestCSRF_OpaqueOriginRejected(t *testing.T) {
	tok := MintCSRFToken()
	h := CSRFMiddleware(tok, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	// "null", file://, data: all parse to an empty hostname via
	// net/url; treat those as non-loopback so a sandboxed iframe or
	// non-browser caller cannot bypass the CSRF gate.
	for _, origin := range []string{"null", "file:///etc/passwd", "data:text/html,foo", "http://"} {
		r := httptest.NewRequest("POST", "/api/x", strings.NewReader("{}"))
		r.Header.Set("X-Dreamer-CSRF", tok)
		r.Header.Set("Origin", origin)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 403 {
			t.Fatalf("Origin=%q: status = %d, want 403", origin, w.Code)
		}
	}
}

func TestCSRF_WrongTokenRejected(t *testing.T) {
	tok := MintCSRFToken()
	h := CSRFMiddleware(tok, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	r := httptest.NewRequest("POST", "/api/x", strings.NewReader("{}"))
	r.Header.Set("Origin", "http://127.0.0.1:7777")
	r.Header.Set("X-Dreamer-CSRF", "0000000000000000000000000000000000000000000000000000000000000000")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatalf("status = %d, want 403", w.Code)
	}
}

func TestCSRF_GetBypassesCheck(t *testing.T) {
	tok := MintCSRFToken()
	h := CSRFMiddleware(tok, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))
	r := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatalf("status = %d, want 200", w.Code)
	}
}
