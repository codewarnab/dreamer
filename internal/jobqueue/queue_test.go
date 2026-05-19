package jobqueue

import (
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func testQueue(t *testing.T) *Queue {
	t.Helper()
	return New(Options{
		StorePath:     filepath.Join(t.TempDir(), "jobs.json"),
		MaxConcurrent: 2,
		MaxDuration:   time.Hour,
	})
}

func TestEnqueueAddsJob(t *testing.T) {
	q := testQueue(t)
	job := q.Enqueue("proj-a", EnqueueConfig{ProjectPath: "/a", Provider: "test", Since: "1h"})
	if job == nil {
		t.Fatal("expected non-nil job")
	}
	if job.Project != "proj-a" {
		t.Fatalf("project = %q, want proj-a", job.Project)
	}
	if job.Status != StatusPending {
		t.Fatalf("status = %q, want pending", job.Status)
	}
}

func TestEnqueueDedup(t *testing.T) {
	q := testQueue(t)
	_ = q.Enqueue("proj-a", EnqueueConfig{ProjectPath: "/a"})
	dup := q.Enqueue("proj-a", EnqueueConfig{ProjectPath: "/a"})
	if dup != nil {
		t.Fatal("expected nil on duplicate enqueue")
	}
}

func TestEnqueueDedupAllowsAfterComplete(t *testing.T) {
	q := testQueue(t)
	j1 := q.Enqueue("proj-a", EnqueueConfig{ProjectPath: "/a"})
	q.Complete(j1, 5, 10, 2)
	j2 := q.Enqueue("proj-a", EnqueueConfig{ProjectPath: "/a"})
	if j2 == nil {
		t.Fatal("expected non-nil job after prior completed")
	}
}

func TestDequeueReturnsPendingJob(t *testing.T) {
	q := testQueue(t)
	_ = q.Enqueue("proj-a", EnqueueConfig{ProjectPath: "/a"})
	job := q.Dequeue()
	if job == nil {
		t.Fatal("expected job from dequeue")
	}
	if job.Status != StatusRunning {
		t.Fatalf("status = %q, want running", job.Status)
	}
	if job.StartedAt == nil {
		t.Fatal("expected StartedAt to be set")
	}
}

func TestDequeueNilAtConcurrencyLimit(t *testing.T) {
	q := testQueue(t)
	_ = q.Enqueue("proj-a", EnqueueConfig{ProjectPath: "/a"})
	_ = q.Enqueue("proj-b", EnqueueConfig{ProjectPath: "/b"})
	_ = q.Enqueue("proj-c", EnqueueConfig{ProjectPath: "/c"})
	_ = q.Dequeue() // takes slot 1
	_ = q.Dequeue() // takes slot 2
	got := q.Dequeue()
	if got != nil {
		t.Fatal("expected nil at concurrency limit")
	}
}

func TestDequeueFreesSlotAfterComplete(t *testing.T) {
	q := testQueue(t)
	_ = q.Enqueue("proj-a", EnqueueConfig{ProjectPath: "/a"})
	_ = q.Enqueue("proj-b", EnqueueConfig{ProjectPath: "/b"})
	j1 := q.Dequeue()
	q.Complete(j1, 0, 0, 0)
	j2 := q.Dequeue()
	if j2 == nil {
		t.Fatal("expected job after freeing slot")
	}
}

func TestCompleteSetsMetrics(t *testing.T) {
	q := testQueue(t)
	j := q.Enqueue("proj-a", EnqueueConfig{ProjectPath: "/a"})
	_ = q.Dequeue()
	q.Complete(j, 7, 42, 3)
	if j.Status != StatusCompleted {
		t.Fatalf("status = %q, want completed", j.Status)
	}
	if j.FindingsAdded != 7 {
		t.Fatalf("findings = %d, want 7", j.FindingsAdded)
	}
	if j.Duration == 0 {
		t.Fatal("expected non-zero duration")
	}
	if j.FinishedAt == nil {
		t.Fatal("expected FinishedAt to be set")
	}
}

func TestFailedSetsError(t *testing.T) {
	q := testQueue(t)
	j := q.Enqueue("proj-a", EnqueueConfig{ProjectPath: "/a"})
	_ = q.Dequeue()
	q.Failed(j, errors.New("rate limit"))
	if j.Status != StatusFailed {
		t.Fatalf("status = %q, want failed", j.Status)
	}
	if j.Error != "rate limit" {
		t.Fatalf("error = %q, want rate limit", j.Error)
	}
}

func TestTimedOutSetsStatus(t *testing.T) {
	q := testQueue(t)
	j := q.Enqueue("proj-a", EnqueueConfig{ProjectPath: "/a"})
	_ = q.Dequeue()
	q.TimedOut(j)
	if j.Status != StatusTimedOut {
		t.Fatalf("status = %q, want timed_out", j.Status)
	}
}

func TestCancelSetsStatus(t *testing.T) {
	q := testQueue(t)
	j := q.Enqueue("proj-a", EnqueueConfig{ProjectPath: "/a"})
	_ = q.Dequeue()
	q.Cancel(j)
	if j.Status != StatusCancelled {
		t.Fatalf("status = %q, want cancelled", j.Status)
	}
}

func TestHasActiveJob(t *testing.T) {
	q := testQueue(t)
	if q.HasActiveJob("proj-a") {
		t.Fatal("expected no active job before enqueue")
	}
	j := q.Enqueue("proj-a", EnqueueConfig{ProjectPath: "/a"})
	if !q.HasActiveJob("proj-a") {
		t.Fatal("expected active job after enqueue")
	}
	q.Complete(j, 0, 0, 0)
	if q.HasActiveJob("proj-a") {
		t.Fatal("expected no active job after complete")
	}
}

func TestStatusCounts(t *testing.T) {
	q := testQueue(t)
	_ = q.Enqueue("a", EnqueueConfig{ProjectPath: "/a"})
	_ = q.Enqueue("b", EnqueueConfig{ProjectPath: "/b"})
	_ = q.Enqueue("c", EnqueueConfig{ProjectPath: "/c"})
	j1 := q.Dequeue()
	j2 := q.Dequeue()
	q.Complete(j1, 0, 0, 0)
	q.Failed(j2, errors.New("err"))

	s := q.Status()
	if s.Completed != 1 {
		t.Fatalf("completed = %d, want 1", s.Completed)
	}
	if s.Failed != 1 {
		t.Fatalf("failed = %d, want 1", s.Failed)
	}
	if s.Pending != 1 {
		t.Fatalf("pending = %d, want 1", s.Pending)
	}
}

func TestPruneHistory(t *testing.T) {
	q := testQueue(t)
	j := q.Enqueue("a", EnqueueConfig{ProjectPath: "/a"})
	_ = q.Dequeue()
	q.Complete(j, 0, 0, 0)
	// Fake an old finish time.
	old := time.Now().UTC().Add(-48 * time.Hour)
	j.FinishedAt = &old

	q.PruneHistory(24 * time.Hour)
	s := q.Status()
	if len(s.Jobs) != 0 {
		t.Fatalf("expected 0 jobs after prune, got %d", len(s.Jobs))
	}
}

func TestPruneHistoryKeepsActive(t *testing.T) {
	q := testQueue(t)
	_ = q.Enqueue("a", EnqueueConfig{ProjectPath: "/a"})
	q.PruneHistory(0)
	if !q.HasActiveJob("a") {
		t.Fatal("expected active job to survive prune")
	}
}

func TestCancelRunning(t *testing.T) {
	q := testQueue(t)
	_ = q.Enqueue("a", EnqueueConfig{ProjectPath: "/a"})
	_ = q.Enqueue("b", EnqueueConfig{ProjectPath: "/b"})
	j1 := q.Dequeue()
	j2 := q.Dequeue()
	q.CancelRunning()
	if j1.Status != StatusCancelled {
		t.Fatalf("j1 status = %q, want cancelled", j1.Status)
	}
	if j2.Status != StatusCancelled {
		t.Fatalf("j2 status = %q, want cancelled", j2.Status)
	}
}

func TestRecoverLoadsFromDisk(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "jobs.json")

	q1 := New(Options{StorePath: path, MaxConcurrent: 1, MaxDuration: time.Hour})
	j := q1.Enqueue("a", EnqueueConfig{ProjectPath: "/a"})
	_ = q1.Dequeue()
	q1.Complete(j, 3, 10, 2)

	q2 := New(Options{StorePath: path, MaxConcurrent: 1, MaxDuration: time.Hour})
	if err := q2.Recover(); err != nil {
		t.Fatalf("recover: %v", err)
	}
	s := q2.Status()
	if len(s.Jobs) != 1 {
		t.Fatalf("expected 1 job after recover, got %d", len(s.Jobs))
	}
	if s.Jobs[0].FindingsAdded != 3 {
		t.Fatalf("findings = %d, want 3", s.Jobs[0].FindingsAdded)
	}
}

func TestRecoverMissingFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nonexistent.json")
	q := New(Options{StorePath: path, MaxConcurrent: 1, MaxDuration: time.Hour})
	if err := q.Recover(); err != nil {
		t.Fatalf("recover on missing file: %v", err)
	}
	s := q.Status()
	if len(s.Jobs) != 0 {
		t.Fatalf("expected 0 jobs, got %d", len(s.Jobs))
	}
}
