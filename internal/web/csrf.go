package web

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"net"
	"net/http"
	"net/url"
	"strings"
)

// csrfHeader is the request header name that carries the per-process CSRF
// token. All mutating frontend calls must echo this header. Templates that
// read the token from the meta tag must send it under this exact name; the
// Go constant and the template literal must be kept in sync.
const csrfHeader = "X-Dreamer-CSRF"

// csrfTokenBytes is the number of random bytes used to generate a CSRF token
// (256-bit token).
const csrfTokenBytes = 32

// MintCSRFToken returns a fresh 256-bit hex token. The daemon mints one per
// process lifetime and renders it into the SPA shell. The token is only valid
// for the lifetime of the process; it rotates on restart. Rotation-on-restart
// is acceptable for the loopback-only v1.5 model: the browser tab reloads
// after a daemon restart and receives the new token automatically.
func MintCSRFToken() string {
	buf := make([]byte, csrfTokenBytes)
	if _, err := rand.Read(buf); err != nil {
		panic("web: failed to read crypto/rand for CSRF token: " + err.Error())
	}
	return hex.EncodeToString(buf)
}

// CSRFMiddleware enforces two independent layers of protection:
//
//  1. Host header validation (all methods, including GET) — rejects requests
//     whose Host header is not a loopback address. This closes the DNS-rebinding
//     read surface: even if an attacker resolves their domain to 127.0.0.1, the
//     browser will send Host: attacker.com, which is rejected here with 403.
//
//  2. Token + Origin check (state-changing methods only) — requires the
//     X-Dreamer-CSRF token AND a loopback Origin (or Referer fallback).
//
// Together the two layers prevent both read (DNS-rebinding) and write (CSRF)
// attacks from off-loopback origins.
func CSRFMiddleware(token string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Layer 1: Host validation for ALL methods (including GET/HEAD/OPTIONS).
		// Strip port before checking. net.SplitHostPort handles both
		// "host:port" and "[::1]:port" forms. For bare hostnames without a
		// port (e.g. "localhost", "127.0.0.1", "[::1]") it returns an error,
		// so we fall back to the raw value — but first strip any surrounding
		// brackets so that "[::1]" is normalised to "::1" for the loopback check.
		hostOnly := r.Host
		if h, _, err := net.SplitHostPort(r.Host); err == nil {
			hostOnly = h
		} else {
			// No port present: strip IPv6 brackets if present.
			hostOnly = strings.TrimPrefix(strings.TrimSuffix(r.Host, "]"), "[")
		}
		if !isLoopbackHost(hostOnly) {
			http.Error(w, "non-loopback Host", http.StatusForbidden)
			return
		}

		// Layer 2: Token + Origin check for state-changing methods only.
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			next.ServeHTTP(w, r)
			return
		}
		if subtle.ConstantTimeCompare([]byte(r.Header.Get(csrfHeader)), []byte(token)) != 1 {
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
		parsedOrigin, err := url.Parse(origin)
		if err != nil || !isLoopbackHost(parsedOrigin.Hostname()) {
			http.Error(w, "non-loopback Origin", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func isLoopbackHost(hostname string) bool {
	// Empty hostname comes from non-http schemes like file://, data:,
	// and the literal Origin: null; treat those as non-loopback so a
	// sandboxed iframe or non-browser caller cannot bypass the check.
	switch hostname {
	case "127.0.0.1", "localhost", "::1":
		return true
	}
	return false
}
