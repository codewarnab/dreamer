package cmd

import (
	"sync"
	"testing"
	"time"

	"dreamer/internal/logging"
)

// newTestWorkerPool builds a minimal pool sufficient to exercise stop().
// It deliberately wires only logger + wg; the queue/config fields are unused
// by stop() so we keep the fixture small.
func newTestWorkerPool(t *testing.T) *workerPool {
	t.Helper()
	logger, err := logging.New(t.TempDir(), "error", 1)
	if err != nil {
		t.Fatalf("logger: %v", err)
	}
	t.Cleanup(func() { _ = logger.Close() })
	return &workerPool{
		ctx:    t.Context(),
		logger: logger,
	}
}

// TestWorkerPoolStop_ReturnsWhenDrained verifies stop returns promptly once
// every worker goroutine has called wg.Done, well within the grace window.
func TestWorkerPoolStop_ReturnsWhenDrained(t *testing.T) {
	wp := newTestWorkerPool(t)
	wp.wg.Add(1)
	go func() {
		time.Sleep(20 * time.Millisecond)
		wp.wg.Done()
	}()

	start := time.Now()
	wp.stop(2 * time.Second)
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("stop took %s; expected to return shortly after the worker drained", elapsed)
	}
}

// TestWorkerPoolStop_ReturnsOnGraceTimeout verifies stop abandons a stuck
// worker once the grace period elapses rather than blocking indefinitely.
func TestWorkerPoolStop_ReturnsOnGraceTimeout(t *testing.T) {
	wp := newTestWorkerPool(t)

	// A worker that never finishes within the test (simulates a provider
	// subprocess mid-prompt). Released via the channel so the goroutine
	// doesn't leak past the test.
	release := make(chan struct{})
	var once sync.Once
	wp.wg.Add(1)
	go func() {
		<-release
		wp.wg.Done()
	}()
	t.Cleanup(func() { once.Do(func() { close(release) }) })

	start := time.Now()
	wp.stop(50 * time.Millisecond)
	elapsed := time.Since(start)
	if elapsed < 50*time.Millisecond {
		t.Fatalf("stop returned in %s; should have waited the full grace period", elapsed)
	}
	if elapsed > time.Second {
		t.Fatalf("stop took %s; should have given up at the grace period", elapsed)
	}
}
