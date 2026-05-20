package web

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"net/url"
)

// MintCSRFToken returns a fresh 32-byte hex token. Daemon mints one per
// process lifetime and renders it into the SPA shell.
func MintCSRFToken() string {
	buf := make([]byte, 32)
	_, _ = rand.Read(buf)
	return hex.EncodeToString(buf)
}

// CSRFMiddleware enforces:
//   - GET/HEAD/OPTIONS pass through.
//   - State-changing methods require the token AND a loopback Origin.
func CSRFMiddleware(token string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			next.ServeHTTP(w, r)
			return
		}
		if r.Header.Get("X-Dreamer-CSRF") != token {
			http.Error(w, "CSRF token missing or wrong", http.StatusForbidden)
			return
		}
		// Require a loopback Origin (or Referer fallback) on every
		// state-changing request. An entirely missing Origin can come
		// from non-browser clients that may have read the CSRF token
		// out of the SPA HTML; rejecting forces them to identify as
		// loopback so DNS-rebinding/cross-tool scripts can't sneak in.
		origin := r.Header.Get("Origin")
		if origin == "" {
			origin = r.Header.Get("Referer")
		}
		if origin == "" {
			http.Error(w, "Origin or Referer required", http.StatusForbidden)
			return
		}
		u, err := url.Parse(origin)
		if err != nil || !isLoopbackHost(u.Hostname()) {
			http.Error(w, "non-loopback Origin", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func isLoopbackHost(h string) bool {
	switch h {
	case "127.0.0.1", "localhost", "::1", "":
		return true
	}
	return false
}
