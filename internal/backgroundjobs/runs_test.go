package backgroundjobs

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"dreamer/internal/logging"
)

func testLogger(t *testing.T) *logging.Logger {
	t.Helper()
	dir := t.TempDir()
	logger, err := logging.New(dir, "debug", 10)
	if err != nil {
		t.Fatalf("create logger: %v", err)
	}
	t.Cleanup(func() { logger.Close() })
	return logger
}

func newTestRunStore(t *testing.T) *RunStore {
	t.Helper()
	return NewRunStore(t.TempDir(), testLogger(t))
}

func TestRunStore_AppendAndList(t *testing.T) {
	rs := newTestRunStore(t)

	now := time.Now().UTC()
	run := Run{
		ID:             "run-001",
		JobID:          "job-aaa",
		ScheduledFor:   now,
		Status:         RunStatusCompleted,
		StartedAt:      now.Add(-5 * time.Second),
		DurationMillis: 5000,
		ProviderID:     "openclaude-cli",
		PromptSnapshot: "analyze this codebase",
		OutputSummary:  "no issues found",
	}

	if err := rs.Append(run); err != nil {
		t.Fatalf("Append: %v", err)
	}

	runs, err := rs.List("job-aaa")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("len(runs) = %d, want 1", len(runs))
	}
	if runs[0].ID != "run-001" {
		t.Errorf("runs[0].ID = %q, want %q", runs[0].ID, "run-001")
	}
	if runs[0].Status != RunStatusCompleted {
		t.Errorf("runs[0].Status = %q, want %q", runs[0].Status, RunStatusCompleted)
	}
}

func TestRunStore_AppendMultiple_OrderByStartedAtDesc(t *testing.T) {
	rs := newTestRunStore(t)

	base := time.Now().UTC()
	for i, status := range []RunStatus{RunStatusCompleted, RunStatusFailed, RunStatusCompleted} {
		run := Run{
			ID:        "run-00" + string(rune('1'+i)),
			JobID:     "job-order",
			Status:    status,
			StartedAt: base.Add(time.Duration(i) * time.Minute),
		}
		if err := rs.Append(run); err != nil {
			t.Fatalf("Append %d: %v", i, err)
		}
	}

	runs, err := rs.List("job-order")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(runs) != 3 {
		t.Fatalf("len(runs) = %d, want 3", len(runs))
	}
	// Most recent first.
	if !runs[0].StartedAt.After(runs[1].StartedAt) {
		t.Error("runs not sorted most recent first")
	}
}

func TestRunStore_ListEmpty(t *testing.T) {
	rs := newTestRunStore(t)

	runs, err := rs.List("nonexistent")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(runs) != 0 {
		t.Errorf("len(runs) = %d, want 0", len(runs))
	}
}

func TestRunStore_Latest(t *testing.T) {
	rs := newTestRunStore(t)

	// Empty → nil.
	latest, err := rs.Latest("job-latest")
	if err != nil {
		t.Fatalf("Latest: %v", err)
	}
	if latest != nil {
		t.Errorf("latest = %v, want nil", latest)
	}

	// Two runs → most recent returned.
	base := time.Now().UTC()
	rs.Append(Run{ID: "old", JobID: "job-latest", StartedAt: base})
	rs.Append(Run{ID: "new", JobID: "job-latest", StartedAt: base.Add(time.Hour)})

	latest, err = rs.Latest("job-latest")
	if err != nil {
		t.Fatalf("Latest: %v", err)
	}
	if latest == nil || latest.ID != "new" {
		t.Errorf("latest.ID = %q, want %q", latest.ID, "new")
	}
}

func TestRunStore_Count(t *testing.T) {
	rs := newTestRunStore(t)

	if n, err := rs.Count("job-cnt"); err != nil || n != 0 {
		t.Fatalf("Count(empty) = %d, %v; want 0, nil", n, err)
	}

	rs.Append(Run{ID: "a", JobID: "job-cnt", StartedAt: time.Now()})
	rs.Append(Run{ID: "b", JobID: "job-cnt", StartedAt: time.Now()})

	if n, err := rs.Count("job-cnt"); err != nil || n != 2 {
		t.Fatalf("Count = %d, %v; want 2, nil", n, err)
	}
}

func TestRunStore_Prune(t *testing.T) {
	rs := newTestRunStore(t)

	base := time.Now().UTC()
	for i := 0; i < 10; i++ {
		rs.Append(Run{
			ID:        "run-prune",
			JobID:     "job-prune",
			StartedAt: base.Add(time.Duration(i) * time.Minute),
		})
	}

	removed, err := rs.Prune("job-prune", 5)
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if removed != 5 {
		t.Errorf("removed = %d, want 5", removed)
	}

	runs, err := rs.List("job-prune")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(runs) != 5 {
		t.Errorf("len(runs) after prune = %d, want 5", len(runs))
	}
}

func TestRunStore_PruneNoOp(t *testing.T) {
	rs := newTestRunStore(t)

	rs.Append(Run{ID: "a", JobID: "job-noop", StartedAt: time.Now()})

	removed, err := rs.Prune("job-noop", 10)
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if removed != 0 {
		t.Errorf("removed = %d, want 0", removed)
	}
}

func TestRunStore_SkipsMalformedLines(t *testing.T) {
	rs := newTestRunStore(t)

	// Manually write a file with a valid line and a garbage line.
	dir := rs.dir
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	path := filepath.Join(dir, "job-bad.jsonl")

	run := Run{
		ID:        "good-run",
		JobID:     "job-bad",
		Status:    RunStatusCompleted,
		StartedAt: time.Now().UTC(),
	}
	goodData, _ := jsonMarshal(t, run)

	content := goodData + "\nthis is not json\n" + goodData + "\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	runs, err := rs.List("job-bad")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	// Two valid lines, one garbage skipped.
	if len(runs) != 2 {
		t.Errorf("len(runs) = %d, want 2 (malformed line should be skipped)", len(runs))
	}
}

func jsonMarshal(t *testing.T, v any) (string, error) {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func TestRunStore_ConcurrentAppend(t *testing.T) {
	rs := newTestRunStore(t)

	done := make(chan error, 10)
	for i := 0; i < 10; i++ {
		go func(i int) {
			done <- rs.Append(Run{
				ID:        "concurrent",
				JobID:     "job-concurrent",
				StartedAt: time.Now().UTC().Add(time.Duration(i) * time.Millisecond),
			})
		}(i)
	}
	for i := 0; i < 10; i++ {
		if err := <-done; err != nil {
			t.Fatalf("concurrent Append %d: %v", i, err)
		}
	}

	runs, err := rs.List("job-concurrent")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(runs) != 10 {
		t.Errorf("len(runs) = %d, want 10", len(runs))
	}
}

func TestRunStore_Dir(t *testing.T) {
	parent := t.TempDir()
	lg := testLogger(t)
	rs := NewRunStore(parent, lg)

	want := filepath.Join(parent, "runs")
	if rs.Dir() != want {
		t.Errorf("Dir() = %q, want %q", rs.Dir(), want)
	}
}

func TestRunStore_LogDir(t *testing.T) {
	parent := t.TempDir()
	lg := testLogger(t)
	rs := NewRunStore(parent, lg)

	want := filepath.Join(parent, "runs", "job-abc")
	if rs.LogDir("job-abc") != want {
		t.Errorf("LogDir() = %q, want %q", rs.LogDir("job-abc"), want)
	}
}

func TestRun_LogPathJSONRoundTrip(t *testing.T) {
	run := Run{
		ID:      "run-log",
		JobID:   "job-log",
		Status:  RunStatusCompleted,
		LogPath: "/tmp/runs/job-log/run-log.log",
	}
	data, err := json.Marshal(run)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var decoded Run
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if decoded.LogPath != run.LogPath {
		t.Errorf("LogPath = %q, want %q", decoded.LogPath, run.LogPath)
	}
}

func TestRun_LogPathOmittedWhenEmpty(t *testing.T) {
	run := Run{ID: "run-no-log", JobID: "job-no-log", Status: RunStatusCompleted}
	data, err := json.Marshal(run)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if strings.Contains(string(data), "log_path") {
		t.Errorf("log_path should be omitted when empty, got: %s", data)
	}
}

func TestRunStore_PruneRemovesOrphanedLogFiles(t *testing.T) {
	rs := newTestRunStore(t)

	// Create a job directory with log files.
	logDir := rs.LogDir("job-prune-logs")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	base := time.Now().UTC()
	for i := 0; i < 5; i++ {
		runID := "run-" + string(rune('a'+i))
		run := Run{
			ID:        runID,
			JobID:     "job-prune-logs",
			StartedAt: base.Add(time.Duration(i) * time.Minute),
			LogPath:   filepath.Join(logDir, runID+".log"),
		}
		if err := rs.Append(run); err != nil {
			t.Fatalf("Append %d: %v", i, err)
		}
		// Create the log file.
		logPath := filepath.Join(logDir, runID+".log")
		if err := os.WriteFile(logPath, []byte("output for "+runID), 0o644); err != nil {
			t.Fatalf("write log: %v", err)
		}
	}

	// Prune to keep only 2 runs.
	removed, err := rs.Prune("job-prune-logs", 2)
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if removed != 3 {
		t.Errorf("removed = %d, want 3", removed)
	}

	// Verify pruned log files are removed (oldest 3: run-a, run-b, run-c).
	for i := 0; i < 3; i++ {
		runID := "run-" + string(rune('a'+i))
		logPath := filepath.Join(logDir, runID+".log")
		if _, statErr := os.Stat(logPath); !os.IsNotExist(statErr) {
			t.Errorf("log file %s should have been removed", logPath)
		}
	}

	// Verify kept log files still exist (most recent 2: run-d, run-e).
	for i := 3; i < 5; i++ {
		runID := "run-" + string(rune('a'+i))
		logPath := filepath.Join(logDir, runID+".log")
		if _, statErr := os.Stat(logPath); statErr != nil {
			t.Errorf("log file %s should still exist: %v", logPath, statErr)
		}
	}
}
