package cmd

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

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

// readPortFile polls <dir>/web.port (written by Server.Start when bound to an
// ephemeral port) until it appears or the deadline passes.
func readPortFile(t *testing.T, dir string) string {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	path := filepath.Join(dir, "web.port")
	for time.Now().Before(deadline) {
		if data, err := os.ReadFile(path); err == nil {
			if p := strings.TrimSpace(string(data)); p != "" {
				return p
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("web.port not written under %s within deadline", dir)
	return ""
}

// TestServeWeb_StandaloneReadOnly verifies `dreamer web --serve` stands up a
// real server with no daemon: health is reachable, but producer endpoints that
// require unwired hooks (run) degrade to 503 rather than panicking.
func TestServeWeb_StandaloneReadOnly(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	cfgYAML := "daemon:\n  output_root: " + dir +
		"\n  frequency_seconds: 60\nweb:\n  enabled: false\nprojects:\n  - name: p\n    path: " + dir + "\n"
	if err := os.WriteFile(cfgPath, []byte(cfgYAML), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	// serveWeb reads the package-global configPath; set and restore it.
	prev := configPath
	configPath = cfgPath
	t.Cleanup(func() { configPath = prev })

	cmd := &cobra.Command{Use: "web"}
	ctx, cancel := context.WithCancel(t.Context())
	cmd.SetContext(ctx)

	errCh := make(chan error, 1)
	go func() {
		// portOverride 0 → ephemeral port written to <output_root>/web.port.
		errCh <- serveWeb(cmd, 0, false)
	}()

	port := readPortFile(t, dir)
	base := "http://127.0.0.1:" + port

	// Health is served even with no daemon behind it.
	if err := probeHealth(base+"/api/health", 2*time.Second); err != nil {
		cancel()
		t.Fatalf("health probe failed: %v", err)
	}

	// The read-only dashboard endpoint must serve without any producer hooks
	// wired (this is the whole point of --serve). 503-on-write for the
	// producer endpoints is covered by the handler-level tests.
	resp, err := http.Get(base + "/api/dashboard")
	if err != nil {
		cancel()
		t.Fatalf("GET dashboard: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		cancel()
		t.Fatalf("dashboard status = %d, want 200", resp.StatusCode)
	}

	// Canceling the context triggers graceful shutdown; serveWeb returns nil.
	cancel()
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("serveWeb returned error: %v", err)
		}
	case <-time.After(webShutdownTimeout + 2*time.Second):
		t.Fatal("serveWeb did not return after context cancellation")
	}
}

// TestServeWeb_PortOverride verifies an explicit --port value is honored over
// the configured port.
func TestServeWeb_PortOverride(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	cfgYAML := "daemon:\n  output_root: " + dir +
		"\n  frequency_seconds: 60\nweb:\n  enabled: false\n  port: 7777\nprojects: []\n"
	if err := os.WriteFile(cfgPath, []byte(cfgYAML), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	prev := configPath
	configPath = cfgPath
	t.Cleanup(func() { configPath = prev })

	port := freeTCPPort(t)

	cmd := &cobra.Command{Use: "web"}
	ctx, cancel := context.WithCancel(t.Context())
	cmd.SetContext(ctx)

	errCh := make(chan error, 1)
	go func() { errCh <- serveWeb(cmd, port, false) }()

	base := fmt.Sprintf("http://127.0.0.1:%d", port)
	deadline := time.Now().Add(3 * time.Second)
	var healthy bool
	for time.Now().Before(deadline) {
		if err := probeHealth(base+"/api/health", 250*time.Millisecond); err == nil {
			healthy = true
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	if !healthy {
		t.Fatalf("server did not become reachable on overridden port %d", port)
	}
	select {
	case <-errCh:
	case <-time.After(webShutdownTimeout + 2*time.Second):
		t.Fatal("serveWeb did not return after context cancellation")
	}
}

// freeTCPPort asks the OS for an unused loopback port, then releases it.
// There is an inherent TOCTOU window, but it is acceptable for a local test.
func freeTCPPort(t *testing.T) int {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	addr := srv.Listener.Addr().(*net.TCPAddr)
	port := addr.Port
	srv.Close()
	return port
}
