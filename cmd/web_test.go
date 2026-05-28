package cmd

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"dreamer/internal/config"
)

func TestResolveWebPort_PrefersPortFile(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "web.port"), []byte("12345\n"), 0o644)
	cfg := &config.App{Daemon: config.DaemonConfig{OutputRoot: dir}, Web: config.WebConfig{Port: 7777}}
	got, _ := resolveWebPort(cfg)
	if got != 12345 {
		t.Fatalf("got %d want 12345", got)
	}
}

func TestResolveWebPort_FallsBackToConfigPort(t *testing.T) {
	cfg := &config.App{Daemon: config.DaemonConfig{OutputRoot: t.TempDir()}, Web: config.WebConfig{Port: 7777}}
	got, _ := resolveWebPort(cfg)
	if got != 7777 {
		t.Fatalf("got %d want 7777", got)
	}
}

func TestResolveWebPort_DefaultsTo7777(t *testing.T) {
	cfg := &config.App{Daemon: config.DaemonConfig{OutputRoot: t.TempDir()}}
	got, _ := resolveWebPort(cfg)
	if got != 7777 {
		t.Fatalf("got %d want 7777", got)
	}
}

func TestProbeHealth_ReachesRealServer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/health" {
			w.WriteHeader(200)
			return
		}
		w.WriteHeader(404)
	}))
	defer srv.Close()
	if err := probeHealth(srv.URL+"/api/health", 500*time.Millisecond); err != nil {
		t.Fatalf("probe: %v", err)
	}
}

func TestProbeHealth_FailsWhenServerDown(t *testing.T) {
	// 192.0.2.0/24 is TEST-NET (RFC 5737) — guaranteed unroutable, no TOCTOU.
	if err := probeHealth("http://192.0.2.1:1/api/health", 50*time.Millisecond); err == nil {
		t.Fatalf("probe should have failed")
	}
}
