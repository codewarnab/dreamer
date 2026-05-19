package web

import (
	"context"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
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

func TestServer_CurrentConfigUsesAtomicPointer(t *testing.T) {
	var ptr atomic.Pointer[config.Config]
	initial := &config.Config{Web: config.WebConfig{Port: 0, Host: "127.0.0.1", LogTailKB: 1, Enabled: boolPtr(true)}, Daemon: config.DaemonConfig{OutputRoot: t.TempDir()}}
	ptr.Store(initial)
	srv, err := NewServer(Options{Config: initial, Logger: newTestLogger(t), Events: pipeline.NewEventBus(), ConfigPtr: &ptr})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	if srv.currentConfig() != initial {
		t.Fatalf("currentConfig != initial")
	}
	next := &config.Config{Web: config.WebConfig{Port: 0, Host: "localhost", LogTailKB: 9, Enabled: boolPtr(true)}, Daemon: config.DaemonConfig{OutputRoot: t.TempDir()}}
	ptr.Store(next)
	if srv.currentConfig() != next {
		t.Fatalf("currentConfig did not pick up CAS swap")
	}
}

func TestServer_RoutesAllHandlers(t *testing.T) {
	outRoot := t.TempDir()
	projPath := t.TempDir()
	cfg := &config.Config{
		Projects: []config.ProjectConfig{{Name: "proj", Path: projPath, Since: "24h"}},
		Daemon:   config.DaemonConfig{OutputRoot: outRoot},
		Web:      config.WebConfig{Port: 0, Host: "127.0.0.1", LogTailKB: 1, Enabled: boolPtr(true)},
	}
	srv, err := NewServer(Options{
		Config:      cfg,
		Logger:      newTestLogger(t),
		Events:      pipeline.NewEventBus(),
		OverlayPath: filepath.Join(outRoot, "ui-overrides.yaml"),
	})
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
	base := "http://" + srv.Addr()
	cases := []struct {
		path   string
		expect int
	}{
		{"/api/health", 200},
		{"/api/dashboard", 200},
		{"/api/projects", 200},
		{"/api/projects/proj", 200},
		{"/api/projects/unknown", 404},
		{"/api/projects/proj/findings", 200},
		{"/api/projects/proj/chats", 200},
		{"/api/projects/proj/history", 200},
		{"/api/providers", 200},
		{"/api/settings", 200},
		{"/api/logs/tail", 200},
		{"/api/fs/exists?path=" + outRoot, 200},
	}
	for _, c := range cases {
		resp, err := http.Get(base + c.path)
		if err != nil {
			t.Errorf("%s: %v", c.path, err)
			continue
		}
		if resp.StatusCode != c.expect {
			buf := make([]byte, 256)
			n, _ := resp.Body.Read(buf)
			t.Errorf("%s: status %d want %d (body: %s)", c.path, resp.StatusCode, c.expect, buf[:n])
		}
		resp.Body.Close()
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
