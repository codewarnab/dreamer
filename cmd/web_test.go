package cmd

import (
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"dreamer/internal/config"
)

func TestResolveWebPort_PrefersPortFile(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "web.port"), []byte("12345\n"), 0o644)
	cfg := &config.Config{Daemon: config.DaemonConfig{OutputRoot: dir}, Web: config.WebConfig{Port: 7777}}
	got, _ := resolveWebPort(cfg)
	if got != 12345 {
		t.Fatalf("got %d want 12345", got)
	}
}

func TestResolveWebPort_FallsBackToConfigPort(t *testing.T) {
	cfg := &config.Config{Daemon: config.DaemonConfig{OutputRoot: t.TempDir()}, Web: config.WebConfig{Port: 7777}}
	got, _ := resolveWebPort(cfg)
	if got != 7777 {
		t.Fatalf("got %d want 7777", got)
	}
}

func TestResolveWebPort_DefaultsTo7777(t *testing.T) {
	cfg := &config.Config{Daemon: config.DaemonConfig{OutputRoot: t.TempDir()}}
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
	l := listenAndClose(t)
	if err := probeHealth("http://"+l.Host+"/api/health", 50*time.Millisecond); err == nil {
		t.Fatalf("probe should have failed")
	}
}

func listenAndClose(t *testing.T) *url.URL {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := l.Addr().String()
	_ = l.Close()
	u, _ := url.Parse("http://" + addr)
	return u
}
