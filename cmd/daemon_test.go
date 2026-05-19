package cmd

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"dreamer/internal/config"
	"dreamer/internal/logging"
	"dreamer/internal/pipeline"
)

func TestDaemonSignalsIncludesSIGTERMOnUnix(t *testing.T) {
	signals := daemonSignals()

	if len(signals) == 0 {
		t.Fatal("daemonSignals returned empty slice")
	}

	// os.Interrupt should always be present.
	foundInterrupt := false
	for _, s := range signals {
		if s == os.Interrupt {
			foundInterrupt = true
		}
	}
	if !foundInterrupt {
		t.Fatal("daemonSignals should include os.Interrupt")
	}

	if runtime.GOOS == "windows" {
		if len(signals) != 1 {
			t.Fatalf("Windows: expected 1 signal, got %d", len(signals))
		}
	} else {
		if len(signals) != 2 {
			t.Fatalf("Unix: expected 2 signals (SIGINT+SIGTERM), got %d", len(signals))
		}
	}
}

// TestStartConfigWatcher_PublishesOnWrite verifies the fsnotify goroutine
// publishes a config.reloaded event when the base config file is written.
func TestStartConfigWatcher_PublishesOnWrite(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	overlayPath := filepath.Join(dir, "ui-overrides.yaml")

	baseYAML := []byte("daemon:\n  output_root: " + dir + "\n  frequency_seconds: 60\nprojects:\n  - name: p\n    path: " + dir + "\n")
	if err := os.WriteFile(configPath, baseYAML, 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	logger, err := logging.New(dir, "error", 1)
	if err != nil {
		t.Fatalf("logger: %v", err)
	}
	defer logger.Close()

	events := pipeline.NewEventBus()
	sub := events.Subscribe(4)
	defer events.Unsubscribe(sub)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var live atomic.Pointer[config.Config]
	live.Store(&config.Config{})
	startConfigWatcher(ctx, logger, events, &live, configPath, overlayPath)

	// Give the watcher a moment to register.
	time.Sleep(50 * time.Millisecond)

	// Append a benign byte to trigger a WRITE event.
	if err := os.WriteFile(configPath, append(baseYAML, '\n'), 0o644); err != nil {
		t.Fatalf("rewrite config: %v", err)
	}

	select {
	case ev := <-sub:
		if ev.Type != pipeline.EventConfigReload {
			t.Fatalf("event type = %q, want %q", ev.Type, pipeline.EventConfigReload)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for config.reloaded event")
	}
}

// TestStartConfigWatcher_SwapsLiveConfig verifies the atomic pointer is
// CAS-swapped on a successful overlay reload before the SSE event fires.
func TestStartConfigWatcher_SwapsLiveConfig(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	overlayPath := filepath.Join(dir, "ui-overrides.yaml")

	baseYAML := []byte("daemon:\n  output_root: " + dir + "\n  frequency_seconds: 60\ndefault_provider: copilot\nprojects:\n  - name: p\n    path: " + dir + "\n")
	if err := os.WriteFile(configPath, baseYAML, 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	logger, err := logging.New(dir, "error", 1)
	if err != nil {
		t.Fatalf("logger: %v", err)
	}
	defer logger.Close()

	events := pipeline.NewEventBus()
	sub := events.Subscribe(4)
	defer events.Unsubscribe(sub)

	var live atomic.Pointer[config.Config]
	live.Store(&config.Config{DefaultProvider: "copilot"})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	startConfigWatcher(ctx, logger, events, &live, configPath, overlayPath)
	time.Sleep(50 * time.Millisecond)

	// Write an overlay that flips default_provider.
	overlay := []byte("default_provider: claude-cli\n")
	if err := os.WriteFile(overlayPath, overlay, 0o644); err != nil {
		t.Fatalf("write overlay: %v", err)
	}

	select {
	case ev := <-sub:
		if ev.Type != pipeline.EventConfigReload {
			t.Fatalf("event type = %q, want %q", ev.Type, pipeline.EventConfigReload)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for config.reloaded event")
	}

	got := live.Load()
	if got == nil {
		t.Fatal("live.Load() == nil after reload")
	}
	if got.DefaultProvider != "claude-cli" {
		t.Fatalf("live.DefaultProvider = %q, want %q", got.DefaultProvider, "claude-cli")
	}
}
