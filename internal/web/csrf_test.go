package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCSRF_PostWithoutTokenIsRejected(t *testing.T) {
	t.Parallel()
	tok := MintCSRFToken()
	h := CSRFMiddleware(tok, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(204)
	}))
	r := httptest.NewRequest("POST", "/api/x", strings.NewReader("{}"))
	r.Host = "127.0.0.1:7777"
	r.Header.Set("Origin", "http://127.0.0.1:7777")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatalf("status = %d, want 403", w.Code)
	}
}

func TestCSRF_PostWithTokenAndLoopbackOriginAccepted(t *testing.T) {
	t.Parallel()
	tok := MintCSRFToken()
	h := CSRFMiddleware(tok, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	r := httptest.NewRequest("POST", "/api/x", strings.NewReader("{}"))
	r.Host = "127.0.0.1:7777"
	r.Header.Set("Origin", "http://127.0.0.1:7777")
	r.Header.Set("X-Dreamer-CSRF", tok)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 204 {
		t.Fatalf("status = %d, want 204", w.Code)
	}
}

func TestCSRF_NonLoopbackOriginRejected(t *testing.T) {
	t.Parallel()
	tok := MintCSRFToken()
	h := CSRFMiddleware(tok, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	r := httptest.NewRequest("POST", "/api/x", strings.NewReader("{}"))
	r.Host = "127.0.0.1:7777"
	r.Header.Set("Origin", "http://evil.example")
	r.Header.Set("X-Dreamer-CSRF", tok)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatalf("status = %d, want 403", w.Code)
	}
}

func TestCSRF_PostWithoutOriginOrRefererRejected(t *testing.T) {
	t.Parallel()
	tok := MintCSRFToken()
	h := CSRFMiddleware(tok, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	r := httptest.NewRequest("POST", "/api/x", strings.NewReader("{}"))
	r.Host = "127.0.0.1:7777"
	r.Header.Set("X-Dreamer-CSRF", tok)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatalf("status = %d, want 403", w.Code)
	}
}

func TestCSRF_PostWithRefererFallback(t *testing.T) {
	t.Parallel()
	tok := MintCSRFToken()
	h := CSRFMiddleware(tok, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	r := httptest.NewRequest("POST", "/api/x", strings.NewReader("{}"))
	r.Host = "127.0.0.1:7777"
	r.Header.Set("X-Dreamer-CSRF", tok)
	r.Header.Set("Referer", "http://127.0.0.1:7777/settings")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 204 {
		t.Fatalf("status = %d, want 204", w.Code)
	}
}

func TestCSRF_OpaqueOriginRejected(t *testing.T) {
	t.Parallel()
	tok := MintCSRFToken()
	h := CSRFMiddleware(tok, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	// "null", file://, data: all parse to an empty hostname via
	// net/url; treat those as non-loopback so a sandboxed iframe or
	// non-browser caller cannot bypass the CSRF gate.
	for _, origin := range []string{"null", "file:///etc/passwd", "data:text/html,foo", "http://"} {
		r := httptest.NewRequest("POST", "/api/x", strings.NewReader("{}"))
		r.Host = "127.0.0.1:7777"
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
	t.Parallel()
	tok := MintCSRFToken()
	h := CSRFMiddleware(tok, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	r := httptest.NewRequest("POST", "/api/x", strings.NewReader("{}"))
	r.Host = "127.0.0.1:7777"
	r.Header.Set("Origin", "http://127.0.0.1:7777")
	r.Header.Set("X-Dreamer-CSRF", "0000000000000000000000000000000000000000000000000000000000000000")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatalf("status = %d, want 403", w.Code)
	}
}

func TestCSRF_GetBypassesTokenCheck(t *testing.T) {
	t.Parallel()
	tok := MintCSRFToken()
	h := CSRFMiddleware(tok, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))
	r := httptest.NewRequest("GET", "/", nil)
	r.Host = "127.0.0.1:7777"
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatalf("status = %d, want 200", w.Code)
	}
}

// TestCSRF_PutAcceptedWithValidToken verifies that PUT (used by settings-write)
// passes through with a valid token and loopback Origin.
func TestCSRF_PutAcceptedWithValidToken(t *testing.T) {
	t.Parallel()
	tok := MintCSRFToken()
	h := CSRFMiddleware(tok, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	r := httptest.NewRequest("PUT", "/api/settings", strings.NewReader("{}"))
	r.Host = "127.0.0.1:7777"
	r.Header.Set("Origin", "http://127.0.0.1:7777")
	r.Header.Set("X-Dreamer-CSRF", tok)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 204 {
		t.Fatalf("status = %d, want 204", w.Code)
	}
}

// TestCSRF_DeleteAcceptedWithValidToken verifies that DELETE (used by project
// deletion) passes through with a valid token and loopback Origin.
func TestCSRF_DeleteAcceptedWithValidToken(t *testing.T) {
	t.Parallel()
	tok := MintCSRFToken()
	h := CSRFMiddleware(tok, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	r := httptest.NewRequest("DELETE", "/api/projects/myproj", nil)
	r.Host = "localhost:7777"
	r.Header.Set("Origin", "http://localhost:7777")
	r.Header.Set("X-Dreamer-CSRF", tok)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 204 {
		t.Fatalf("status = %d, want 204", w.Code)
	}
}

// TestCSRF_HeadBypassesTokenCheck verifies that HEAD is not subject to the
// token/Origin check (same as GET).
func TestCSRF_HeadBypassesTokenCheck(t *testing.T) {
	t.Parallel()
	tok := MintCSRFToken()
	h := CSRFMiddleware(tok, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))
	r := httptest.NewRequest("HEAD", "/", nil)
	r.Host = "127.0.0.1:7777"
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatalf("status = %d, want 200", w.Code)
	}
}

// TestCSRF_OptionsBypassesTokenCheck verifies that OPTIONS (preflight) is not
// subject to the token/Origin check.
func TestCSRF_OptionsBypassesTokenCheck(t *testing.T) {
	t.Parallel()
	tok := MintCSRFToken()
	h := CSRFMiddleware(tok, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))
	r := httptest.NewRequest("OPTIONS", "/api/x", nil)
	r.Host = "127.0.0.1:7777"
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatalf("status = %d, want 200", w.Code)
	}
}

// TestCSRF_HostValidation_NonLoopbackRejected verifies that requests with a
// non-loopback Host header are rejected on all methods, closing the
// DNS-rebinding read surface.
func TestCSRF_HostValidation_NonLoopbackRejected(t *testing.T) {
	t.Parallel()
	tok := MintCSRFToken()
	h := CSRFMiddleware(tok, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))

	for _, host := range []string{"attacker.com", "attacker.com:80", "evil.example:7777"} {
		r := httptest.NewRequest("GET", "/api/projects", nil)
		r.Host = host
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 403 {
			t.Fatalf("Host=%q: GET status = %d, want 403", host, w.Code)
		}
	}
}

// TestCSRF_HostValidation_LoopbackHostsPassed verifies that all three loopback
// forms are accepted, with and without a port.
func TestCSRF_HostValidation_LoopbackHostsPassed(t *testing.T) {
	t.Parallel()
	tok := MintCSRFToken()
	h := CSRFMiddleware(tok, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))

	for _, host := range []string{
		"127.0.0.1",
		"127.0.0.1:7777",
		"localhost",
		"localhost:7777",
		"[::1]",
		"[::1]:7777",
	} {
		r := httptest.NewRequest("GET", "/api/projects", nil)
		r.Host = host
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 200 {
			t.Fatalf("Host=%q: GET status = %d, want 200", host, w.Code)
		}
	}
}
