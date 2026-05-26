package cmd

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"dreamer/internal/config"
	"dreamer/internal/jobqueue"
)

func TestFilterStatus(t *testing.T) {
	status := jobqueue.QueueStatus{
		Jobs: []*jobqueue.Job{
			{Project: "foo", Status: jobqueue.StatusRunning},
			{Project: "bar", Status: jobqueue.StatusPending},
			{Project: "foo", Status: jobqueue.StatusCompleted},
		},
	}
	filtered := filterStatus(status, "foo")
	if len(filtered.Jobs) != 2 {
		t.Fatalf("filtered len = %d, want 2", len(filtered.Jobs))
	}
	for _, j := range filtered.Jobs {
		if j.Project != "foo" {
			t.Fatalf("unexpected project %q", j.Project)
		}
	}
}

func TestFilterStatusNoMatch(t *testing.T) {
	status := jobqueue.QueueStatus{
		Jobs: []*jobqueue.Job{
			{Project: "foo", Status: jobqueue.StatusRunning},
		},
	}
	filtered := filterStatus(status, "nonexistent")
	if len(filtered.Jobs) != 0 {
		t.Fatalf("filtered len = %d, want 0", len(filtered.Jobs))
	}
}

func TestRecount(t *testing.T) {
	now := time.Now().UTC()
	status := jobqueue.QueueStatus{
		Jobs: []*jobqueue.Job{
			{Status: jobqueue.StatusRunning},
			{Status: jobqueue.StatusRunning},
			{Status: jobqueue.StatusPending},
			{Status: jobqueue.StatusCompleted, FinishedAt: &now},
			{Status: jobqueue.StatusFailed, FinishedAt: &now},
			{Status: jobqueue.StatusTimedOut, FinishedAt: &now},
			{Status: jobqueue.StatusCancelled, FinishedAt: &now},
		},
	}
	status = recount(status)
	if status.Running != 2 {
		t.Fatalf("Running = %d, want 2", status.Running)
	}
	if status.Pending != 1 {
		t.Fatalf("Pending = %d, want 1", status.Pending)
	}
	if status.Completed != 1 {
		t.Fatalf("Completed = %d, want 1", status.Completed)
	}
	if status.Failed != 1 {
		t.Fatalf("Failed = %d, want 1", status.Failed)
	}
	if status.TimedOut != 1 {
		t.Fatalf("TimedOut = %d, want 1", status.TimedOut)
	}
	if status.Cancelled != 1 {
		t.Fatalf("Cancelled = %d, want 1", status.Cancelled)
	}
}

func TestRecountEmpty(t *testing.T) {
	status := jobqueue.QueueStatus{}
	status = recount(status)
	if status.Running != 0 || status.Pending != 0 || status.Completed != 0 {
		t.Fatalf("expected all zeros")
	}
}

func TestFilterRecent(t *testing.T) {
	now := time.Now().UTC()
	old := now.Add(-1 * time.Hour)
	recent := now.Add(-1 * time.Minute)

	status := jobqueue.QueueStatus{
		Jobs: []*jobqueue.Job{
			{Status: jobqueue.StatusRunning},                    // active
			{Status: jobqueue.StatusCompleted, FinishedAt: &recent}, // recent
			{Status: jobqueue.StatusCompleted, FinishedAt: &old},    // stale
		},
	}
	filtered := filterRecent(status, 5*time.Minute)
	if len(filtered.Jobs) != 2 {
		t.Fatalf("filtered len = %d, want 2 (active + recent)", len(filtered.Jobs))
	}
}

func TestFilterRecentNilFinishedAt(t *testing.T) {
	status := jobqueue.QueueStatus{
		Jobs: []*jobqueue.Job{
			{Status: jobqueue.StatusCompleted, FinishedAt: nil}, // terminal but no finish time
		},
	}
	filtered := filterRecent(status, 5*time.Minute)
	if len(filtered.Jobs) != 0 {
		t.Fatalf("filtered len = %d, want 0 (nil FinishedAt should be excluded)", len(filtered.Jobs))
	}
}

func TestCheckJobConflictNoConflict(t *testing.T) {
	// With no jobs.json file, checkJobConflict should return empty string.
	tmpDir := t.TempDir()
	cfg := &config.Config{
		Daemon: config.DaemonConfig{
			OutputRoot: tmpDir,
		},
	}
	result := checkJobConflict(cfg, "/some/project/path")
	if result != "" {
		t.Fatalf("checkJobConflict = %q, want empty", result)
	}
}

func TestPrintBox(t *testing.T) {
	command := newRootCommand()
	var buf = new(bytes.Buffer)
	command.SetOut(buf)

	printBox(command, []string{"hello", "world"})
	output := buf.String()
	if !strings.Contains(output, "hello") {
		t.Fatalf("output missing 'hello': %s", output)
	}
	if !strings.Contains(output, "world") {
		t.Fatalf("output missing 'world': %s", output)
	}
}

func TestPrintBoxEmpty(t *testing.T) {
	command := newRootCommand()
	var buf = new(bytes.Buffer)
	command.SetOut(buf)

	printBox(command, []string{})
	output := buf.String()
	// Should still render box borders.
	if !strings.Contains(output, "╔") {
		t.Fatalf("output missing top border: %s", output)
	}
}
