package web

import (
	"context"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"dreamer/internal/config"
	"dreamer/internal/logging"
	"dreamer/internal/pipeline"
)

func newTestLogger(t *testing.T) *logging.Logger {
	t.Helper()
	dir := t.TempDir()
	lg, err := logging.New(dir, "info", 1)
	if err != nil {
		t.Fatalf("logger: %v", err)
	}
	t.Cleanup(func() { _ = lg.Close() })
	return lg
}

func boolPtr(b bool) *bool { return &b }

func TestServer_StartAndServeIndex(t *testing.T) {
	cfg := &config.Config{
		Web:    config.WebConfig{Port: 0, Host: "127.0.0.1", LogTailKB: 1, Enabled: boolPtr(true)},
		Daemon: config.DaemonConfig{OutputRoot: t.TempDir()},
	}
	srv, err := NewServer(Options{Config: cfg, Logger: newTestLogger(t), Events: pipeline.NewEventBus()})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	if err := srv.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	}()
	resp, err := http.Get("http://" + srv.Addr() + "/")
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	portPath := filepath.Join(cfg.Daemon.OutputRoot, "web.port")
	data, err := os.ReadFile(portPath)
	if err != nil {
		t.Fatalf("read port file: %v", err)
	}
	parts := strings.Split(srv.Addr(), ":")
	if len(parts) < 2 || !strings.Contains(string(data), parts[len(parts)-1]) {
		t.Fatalf("port file %q does not contain Addr port from %q", data, srv.Addr())
	}
}

func TestServer_BindInUseReturnsError(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer l.Close()
	port := l.Addr().(*net.TCPAddr).Port
	cfg := &config.Config{
		Web:    config.WebConfig{Port: port, Host: "127.0.0.1", LogTailKB: 1, Enabled: boolPtr(true)},
		Daemon: config.DaemonConfig{OutputRoot: t.TempDir()},
	}
	srv, _ := NewServer(Options{Config: cfg, Logger: newTestLogger(t), Events: pipeline.NewEventBus()})
	if err := srv.Start(); err == nil {
		t.Fatalf("expected bind error")
	}
}

func TestServer_HealthEndpoint(t *testing.T) {
	cfg := &config.Config{
		Web:    config.WebConfig{Port: 0, Host: "127.0.0.1", LogTailKB: 1, Enabled: boolPtr(true)},
		Daemon: config.DaemonConfig{OutputRoot: t.TempDir()},
	}
	srv, _ := NewServer(Options{Config: cfg, Logger: newTestLogger(t), Events: pipeline.NewEventBus()})
	if err := srv.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	}()
	resp, err := http.Get("http://" + srv.Addr() + "/api/health")
	if err != nil {
		t.Fatalf("GET /api/health: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d", resp.StatusCode)
	}
}
