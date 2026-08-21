package cmd

import (
	"context"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"dreamer/internal/logging"
)

func TestDaemonStopFilePath(t *testing.T) {
	got := daemonStopFilePath("/tmp/output")
	want := filepath.Join("/tmp/output", "dreamer.daemon.stop")
	if got != want {
		t.Errorf("daemonStopFilePath = %q, want %q", got, want)
	}
}

func TestWatchDaemonStopFile_TriggersWhenFileAppears(t *testing.T) {
	dir := t.TempDir()
	stopPath := filepath.Join(dir, daemonStopFileName)
	logger, err := logging.New(dir, "error", 1)
	if err != nil {
		t.Fatalf("logging.New: %v", err)
	}
	defer func() { _ = logger.Close() }()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var triggered atomic.Int32
	done := make(chan struct{})
	go func() {
		watchDaemonStopFile(ctx, logger, stopPath, 10*time.Millisecond, func() {
			triggered.Add(1)
			close(done)
		})
	}()

	if writeErr := os.WriteFile(stopPath, []byte("stop"), 0o600); writeErr != nil {
		t.Fatalf("write stop file: %v", writeErr)
	}

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("watchDaemonStopFile did not trigger within timeout")
	}

	if _, statErr := os.Stat(stopPath); !os.IsNotExist(statErr) {
		t.Error("stop file should be removed before triggering shutdown")
	}
	cancel()
	if got := triggered.Load(); got != 1 {
		t.Errorf("trigger called %d times, want exactly 1", got)
	}
}

func TestRequestGracefulStop_SucceedsWhenDaemonRemovesLock(t *testing.T) {
	dir := t.TempDir()
	lockPath := filepath.Join(dir, "dreamer.daemon.lock")
	stopPath := filepath.Join(dir, daemonStopFileName)
	if err := os.WriteFile(lockPath, []byte("pid=123"), 0o600); err != nil {
		t.Fatalf("write lock: %v", err)
	}

	// Simulate the daemon: on seeing the sentinel it removes the lockfile.
	go func() {
		for i := 0; i < 100; i++ {
			if _, err := os.Stat(stopPath); err == nil {
				_ = os.Remove(stopPath)
				_ = os.Remove(lockPath)
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
	}()

	if !requestGracefulStop(stopPath, lockPath, 2*time.Second) {
		t.Fatal("requestGracefulStop should succeed when the daemon removes the lockfile")
	}
}

func TestRequestGracefulStop_FallsBackAndClearsSentinelOnTimeout(t *testing.T) {
	dir := t.TempDir()
	lockPath := filepath.Join(dir, "dreamer.daemon.lock")
	stopPath := filepath.Join(dir, daemonStopFileName)
	if err := os.WriteFile(lockPath, []byte("pid=123"), 0o600); err != nil {
		t.Fatalf("write lock: %v", err)
	}

	if requestGracefulStop(stopPath, lockPath, 150*time.Millisecond) {
		t.Fatal("requestGracefulStop should fail when the daemon never removes the lockfile")
	}
	if _, err := os.Stat(stopPath); !os.IsNotExist(err) {
		t.Error("sentinel should be removed after timeout so a future daemon is not stopped")
	}
}
