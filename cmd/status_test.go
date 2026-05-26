package cmd

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"dreamer/internal/config"
	"dreamer/internal/jobqueue"
	"github.com/spf13/cobra"
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

func TestPrintStatusTable_EmptyQueue(t *testing.T) {
	cmd := &cobra.Command{}
	var buf bytes.Buffer
	cmd.SetOut(&buf)

	printStatusTable(cmd, jobqueue.QueueStatus{})

	output := buf.String()
	if !strings.Contains(output, "No jobs in queue.") {
		t.Fatalf("expected 'No jobs in queue.', got: %q", output)
	}
}

func TestPrintStatusTable_WithJobs(t *testing.T) {
	cmd := &cobra.Command{}
	var buf bytes.Buffer
	cmd.SetOut(&buf)

	now := time.Now().UTC()
	started := now.Add(-5 * time.Minute)
	status := jobqueue.QueueStatus{
		Running:   1,
		Pending:   1,
		Completed: 1,
		Failed:    1,
		Jobs: []*jobqueue.Job{
			{Project: "projA", Status: jobqueue.StatusRunning, Provider: "test", StartedAt: &started},
			{Project: "projB", Status: jobqueue.StatusPending, Provider: "test", EnqueuedAt: now},
			{Project: "projC", Status: jobqueue.StatusCompleted, Provider: "test", FinishedAt: &now, Duration: 3 * time.Minute, FindingsAdded: 5},
			{Project: "projD", Status: jobqueue.StatusFailed, Provider: "test", FinishedAt: &now, Duration: 1 * time.Minute, Error: "something broke"},
		},
	}

	printStatusTable(cmd, status)
	output := buf.String()

	if !strings.Contains(output, "RUNNING") {
		t.Fatalf("output missing RUNNING section: %q", output)
	}
	if !strings.Contains(output, "PENDING") {
		t.Fatalf("output missing PENDING section: %q", output)
	}
	if !strings.Contains(output, "COMPLETED") {
		t.Fatalf("output missing COMPLETED section: %q", output)
	}
	if !strings.Contains(output, "projA") {
		t.Fatalf("output missing projA: %q", output)
	}
	if !strings.Contains(output, "projD") {
		t.Fatalf("output missing projD: %q", output)
	}
	if !strings.Contains(output, "something broke") {
		t.Fatalf("output missing error message: %q", output)
	}
}

func TestPrintStatusTable_RunningJobNoStartedAt(t *testing.T) {
	cmd := &cobra.Command{}
	var buf bytes.Buffer
	cmd.SetOut(&buf)

	status := jobqueue.QueueStatus{
		Running: 1,
		Jobs: []*jobqueue.Job{
			{Project: "projA", Status: jobqueue.StatusRunning, Provider: "test", StartedAt: nil},
		},
	}

	printStatusTable(cmd, status)
	output := buf.String()
	if !strings.Contains(output, "-") {
		t.Fatalf("running job with nil StartedAt should show '-', got: %q", output)
	}
}

func TestPrintStatusJSON_EmptyQueue(t *testing.T) {
	cmd := &cobra.Command{}
	var buf bytes.Buffer
	cmd.SetOut(&buf)

	err := printStatusJSON(cmd, jobqueue.QueueStatus{})
	if err != nil {
		t.Fatalf("printStatusJSON: %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, `"running": 0`) {
		t.Fatalf("expected running:0, got: %q", output)
	}
	if !strings.Contains(output, `"jobs": []`) {
		t.Fatalf("expected empty jobs array, got: %q", output)
	}
}

func TestPrintStatusJSON_WithJobs(t *testing.T) {
	cmd := &cobra.Command{}
	var buf bytes.Buffer
	cmd.SetOut(&buf)

	now := time.Now().UTC()
	status := jobqueue.QueueStatus{
		Running:   1,
		Completed: 1,
		Failed:    1,
		TimedOut:  1,
		Cancelled: 1,
		Jobs: []*jobqueue.Job{
			{
				ID:            "proj-001",
				Project:       "proj",
				Status:        jobqueue.StatusRunning,
				EnqueuedAt:    now,
				StartedAt:     &now,
				Provider:      "test",
				MessagesRead:  10,
				SourcesCount:  2,
			},
			{
				ID:            "proj-002",
				Project:       "proj",
				Status:        jobqueue.StatusCompleted,
				EnqueuedAt:    now,
				FinishedAt:    &now,
				Duration:      5 * time.Minute,
				FindingsAdded: 3,
				Provider:      "test",
			},
			{
				ID:            "proj-003",
				Project:       "proj",
				Status:        jobqueue.StatusFailed,
				EnqueuedAt:    now,
				FinishedAt:    &now,
				Error:         "analysis error",
				Provider:      "test",
			},
			{
				ID:       "proj-004",
				Project:  "proj",
				Status:   jobqueue.StatusTimedOut,
				EnqueuedAt: now,
				Provider: "test",
			},
			{
				ID:       "proj-005",
				Project:  "proj",
				Status:   jobqueue.StatusCancelled,
				EnqueuedAt: now,
				Provider: "test",
			},
		},
	}

	err := printStatusJSON(cmd, status)
	if err != nil {
		t.Fatalf("printStatusJSON: %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, `"timed_out": 1`) {
		t.Fatalf("expected timed_out:1, got: %q", output)
	}
	if !strings.Contains(output, `"cancelled": 1`) {
		t.Fatalf("expected cancelled:1, got: %q", output)
	}
	if !strings.Contains(output, `"findings_added": 3`) {
		t.Fatalf("expected findings_added:3, got: %q", output)
	}
	if !strings.Contains(output, `"error": "analysis error"`) {
		t.Fatalf("expected error field, got: %q", output)
	}
}
