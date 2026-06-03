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
//
// NOTE: Standalone mode (dreamer web --serve) is historically documented as "read-only" because
// it does not configure producer hooks (EnqueueRun, RestartHook, Activity, Jobs, OverlayPath).
// However, it is NOT fully read-only: endpoints that mutate finding lifecycle state
// (Apply, Undo, Dismiss, Resolve, Undismiss, Unresolve) and chat history (chats deletion and
// bulk-deletion) remain active and write to disk using deps.Config() and deps.StateCache.
// Similarly, project deletion is supported because ConfigPath is wired. This is intentional:
// standalone mode is not going to stay read-only in future versions, and these mutating
// features are kept active by design.
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

	portVal := freeTCPPort(t)

	errCh := make(chan error, 1)
	go func() {
		errCh <- serveWeb(cmd, portVal, false, "")
	}()

	base := fmt.Sprintf("http://127.0.0.1:%d", portVal)

	// Health is served even with no daemon behind it. Poll until reachable.
	deadline := time.Now().Add(3 * time.Second)
	var healthy bool
	for time.Now().Before(deadline) {
		if err := probeHealth(base+"/api/health", 250*time.Millisecond); err == nil {
			healthy = true
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !healthy {
		cancel()
		t.Fatalf("health probe failed: server did not become reachable on port %d", portVal)
	}

	client := http.Client{Timeout: 2 * time.Second}

	// The read-only dashboard endpoint must serve without any producer hooks
	// wired (this is the whole point of --serve).
	resp, err := client.Get(base + "/api/dashboard")
	if err != nil {
		cancel()
		t.Fatalf("GET dashboard: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		cancel()
		t.Fatalf("dashboard status = %d, want 200", resp.StatusCode)
	}

	// Verify that a producer endpoint like GET /api/jobs degrades to 503.
	respJobs, err := client.Get(base + "/api/jobs")
	if err != nil {
		cancel()
		t.Fatalf("GET jobs: %v", err)
	}
	_ = respJobs.Body.Close()
	if respJobs.StatusCode != http.StatusServiceUnavailable {
		cancel()
		t.Fatalf("jobs status = %d, want 503", respJobs.StatusCode)
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

	// Get two distinct free ports by holding them open concurrently.
	l1, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen 1: %v", err)
	}
	cfgPort := l1.Addr().(*net.TCPAddr).Port

	l2, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		l1.Close()
		t.Fatalf("listen 2: %v", err)
	}
	overridePort := l2.Addr().(*net.TCPAddr).Port

	l1.Close()
	l2.Close()

	cfgPath := filepath.Join(dir, "config.yaml")
	cfgYAML := fmt.Sprintf("daemon:\n  output_root: %s\n  frequency_seconds: 60\nweb:\n  enabled: false\n  port: %d\nprojects: []\n", dir, cfgPort)
	if err := os.WriteFile(cfgPath, []byte(cfgYAML), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	prev := configPath
	configPath = cfgPath
	t.Cleanup(func() { configPath = prev })

	cmd := &cobra.Command{Use: "web"}
	ctx, cancel := context.WithCancel(t.Context())
	cmd.SetContext(ctx)

	errCh := make(chan error, 1)
	go func() { errCh <- serveWeb(cmd, overridePort, false, "") }()

	// Assert the server is reachable on the override port.
	baseOverride := fmt.Sprintf("http://127.0.0.1:%d", overridePort)
	deadline := time.Now().Add(3 * time.Second)
	var healthyOverride bool
	for time.Now().Before(deadline) {
		if err := probeHealth(baseOverride+"/api/health", 250*time.Millisecond); err == nil {
			healthyOverride = true
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !healthyOverride {
		cancel()
		t.Fatalf("server did not become reachable on overridden port %d", overridePort)
	}

	// Assert the server is NOT reachable on the configured port.
	baseCfg := fmt.Sprintf("http://127.0.0.1:%d", cfgPort)
	if err := probeHealth(baseCfg+"/api/health", 100*time.Millisecond); err == nil {
		cancel()
		t.Fatalf("server was reachable on the configured port %d, but override port %d should have been used instead", cfgPort, overridePort)
	}

	cancel()
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
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("freeTCPPort: %v", err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	return port
}
