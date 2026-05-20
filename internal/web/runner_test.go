package web

import (
	"context"
	"testing"
	"time"

	"dreamer/internal/logging"
)

func newRunnerLogger(t *testing.T) *logging.Logger {
	t.Helper()
	lg, err := logging.New(t.TempDir(), "info", 1)
	if err != nil {
		t.Fatalf("logger: %v", err)
	}
	t.Cleanup(func() { _ = lg.Close() })
	return lg
}

func TestRunner_EnqueueAccepts(t *testing.T) {
	started := make(chan string, 1)
	runner := NewRunner(nil, func(ctx context.Context, project string) error {
		started <- project
		return nil
	}, newRunnerLogger(t))
	id, ok, err := runner.Enqueue("proj")
	if err != nil || !ok || id == "" {
		t.Fatalf("Enqueue: id=%q ok=%v err=%v", id, ok, err)
	}
	select {
	case got := <-started:
		if got != "proj" {
			t.Fatalf("invoke received %q", got)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatalf("worker never started")
	}
}

func TestRunner_SecondEnqueueRejectedWhileInflight(t *testing.T) {
	block := make(chan struct{})
	runner := NewRunner(nil, func(ctx context.Context, project string) error {
		<-block
		return nil
	}, newRunnerLogger(t))
	_, ok1, _ := runner.Enqueue("proj")
	if !ok1 {
		t.Fatalf("first enqueue not accepted")
	}
	// Give the goroutine a moment to register in-flight (Enqueue
	// inserts before spawning, so this is just defensive).
	time.Sleep(10 * time.Millisecond)
	_, ok2, _ := runner.Enqueue("proj")
	if ok2 {
		t.Fatalf("second enqueue should be rejected (in-flight)")
	}
	close(block)
}

func TestRunner_DifferentProjectsConcurrent(t *testing.T) {
	block := make(chan struct{})
	runner := NewRunner(nil, func(ctx context.Context, project string) error {
		<-block
		return nil
	}, newRunnerLogger(t))
	_, ok1, _ := runner.Enqueue("a")
	_, ok2, _ := runner.Enqueue("b")
	if !ok1 || !ok2 {
		t.Fatalf("a=%v b=%v", ok1, ok2)
	}
	close(block)
}

// TestRunner_ParentCtxCancelPropagates exercises the SIGTERM/shutdown
// contract: cancelling the daemon's parent ctx must cancel any
// in-flight web-triggered run.
func TestRunner_ParentCtxCancelPropagates(t *testing.T) {
	parent, cancel := context.WithCancel(context.Background())
	defer cancel()
	cancelled := make(chan struct{})
	runner := NewRunner(parent, func(ctx context.Context, project string) error {
		<-ctx.Done()
		close(cancelled)
		return ctx.Err()
	}, newRunnerLogger(t))
	if _, ok, _ := runner.Enqueue("p"); !ok {
		t.Fatalf("enqueue rejected")
	}
	time.Sleep(20 * time.Millisecond)
	cancel()
	select {
	case <-cancelled:
		// pass
	case <-time.After(500 * time.Millisecond):
		t.Fatalf("worker did not observe parent ctx cancel")
	}
}
