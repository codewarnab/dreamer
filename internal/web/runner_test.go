package web

import (
	"testing"

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
	var invoked string
	runner := NewRunner(func(project string) (string, bool) {
		invoked = project
		return "job-123", true
	}, newRunnerLogger(t))
	id, ok, err := runner.Enqueue("proj")
	if err != nil || !ok || id != "job-123" {
		t.Fatalf("Enqueue: id=%q ok=%v err=%v", id, ok, err)
	}
	if invoked != "proj" {
		t.Fatalf("invoke received %q", invoked)
	}
}

func TestRunner_EnqueueRejectedByQueue(t *testing.T) {
	runner := NewRunner(func(project string) (string, bool) {
		return "", false // queue dedup
	}, newRunnerLogger(t))
	id, ok, err := runner.Enqueue("proj")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok || id != "" {
		t.Fatalf("expected rejection: id=%q ok=%v", id, ok)
	}
}

func TestRunner_DifferentProjectsBothAccepted(t *testing.T) {
	runner := NewRunner(func(project string) (string, bool) {
		return "job-" + project, true
	}, newRunnerLogger(t))
	id1, ok1, _ := runner.Enqueue("a")
	id2, ok2, _ := runner.Enqueue("b")
	if !ok1 || !ok2 {
		t.Fatalf("a ok=%v b ok=%v", ok1, ok2)
	}
	if id1 != "job-a" || id2 != "job-b" {
		t.Fatalf("ids: %q %q", id1, id2)
	}
}

func TestRunner_EnqueueFunc(t *testing.T) {
	runner := NewRunner(func(project string) (string, bool) {
		return "id", true
	}, newRunnerLogger(t))
	fn := runner.EnqueueFunc()
	id, ok, err := fn("proj")
	if err != nil || !ok || id != "id" {
		t.Fatalf("EnqueueFunc: id=%q ok=%v err=%v", id, ok, err)
	}
}
