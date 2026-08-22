package capture

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"dreamer/internal/fsutil"
)

func testMeta(runID string) RunMeta {
	return RunMeta{
		RunID:       runID,
		ProjectName: "proj",
		ProjectPath: "/repo",
		ProviderID:  "claude-cli",
		Model:       "claude-sonnet",
		Sandbox:     "auto",
		StartedAt:   time.Date(2026, 8, 22, 10, 0, 0, 0, time.UTC),
	}
}

func TestOpenWritesMetaAndAppendsRecords(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "run-a")
	w, err := Open(dir, testMeta("run-a"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	for i := 0; i < 3; i++ {
		rec := Record{
			Phase:      PhasePhase1,
			ChunkIndex: i,
			ChunkCount: 3,
			Prompt:     fmt.Sprintf("prompt-%d", i),
			Response:   `{"summary":"s"}`,
			Status:     StatusOK,
		}
		if err := w.Append(rec); err != nil {
			t.Fatalf("Append %d: %v", i, err)
		}
	}
	if got := w.Count(); got != 3 {
		t.Fatalf("Count = %d, want 3", got)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("second Close should be a no-op, got %v", err)
	}

	meta, err := LoadMeta(dir)
	if err != nil {
		t.Fatalf("LoadMeta: %v", err)
	}
	if meta.RunID != "run-a" || meta.ProviderID != "claude-cli" || meta.Model != "claude-sonnet" {
		t.Fatalf("meta roundtrip mismatch: %+v", meta)
	}

	root := filepath.Dir(dir)
	gotMeta, records, err := LoadRun(root, "run-a")
	if err != nil {
		t.Fatalf("LoadRun: %v", err)
	}
	if gotMeta.RunID != "run-a" {
		t.Fatalf("LoadRun meta = %+v", gotMeta)
	}
	if len(records) != 3 {
		t.Fatalf("records = %d, want 3", len(records))
	}
	for i, rec := range records {
		if rec.Index != i {
			t.Fatalf("record %d Index = %d, want %d", i, rec.Index, i)
		}
		if rec.Prompt != fmt.Sprintf("prompt-%d", i) {
			t.Fatalf("record %d Prompt = %q", i, rec.Prompt)
		}
		if rec.Status != StatusOK {
			t.Fatalf("record %d Status = %q", i, rec.Status)
		}
	}
}

func TestAppendTruncatesOversizedFields(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "run-t")
	w, err := Open(dir, testMeta("run-t"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = w.Close() }()
	w.SetMaxFieldKB(1)

	big := strings.Repeat("x", 5000)
	err = w.Append(Record{Phase: PhasePhase1, ChunkIndex: 0, Prompt: big, Response: big, Status: StatusOK})
	if err != nil {
		t.Fatalf("Append: %v", err)
	}
	records, err := loadRecords(dir)
	if err != nil {
		t.Fatalf("loadRecords: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("records = %d, want 1", len(records))
	}
	limit := DefaultMaxRecordKB * 1024 // SetMaxFieldKB(1) == 1 KB
	for _, field := range []string{records[0].Prompt, records[0].Response} {
		if len(field) > limit+len(truncatedMarker) {
			t.Fatalf("stored field length %d exceeds cap %d", len(field), limit+len(truncatedMarker))
		}
		if !strings.HasSuffix(field, truncatedMarker) {
			t.Fatalf("stored field missing truncation marker")
		}
	}
}

func TestSetMaxFieldKBUnlimitedKeepsFullPayloads(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "run-u")
	w, err := Open(dir, testMeta("run-u"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = w.Close() }()
	w.SetMaxFieldKB(-1)

	big := strings.Repeat("y", DefaultMaxRecordKB*1024+100)
	if err := w.Append(Record{Phase: PhasePhase2, ChunkIndex: -1, Prompt: big, Status: StatusOK}); err != nil {
		t.Fatalf("Append: %v", err)
	}
	records, err := loadRecords(dir)
	if err != nil {
		t.Fatalf("loadRecords: %v", err)
	}
	if len(records) != 1 || records[0].Prompt != big {
		t.Fatalf("unlimited mode clipped or lost payload")
	}
}

func TestConcurrentAppendsProduceValidJSONL(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "run-c")
	w, err := Open(dir, testMeta("run-c"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	const goroutines = 8
	const perGoroutine = 25
	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < perGoroutine; i++ {
				rec := Record{Phase: PhasePhase1, ChunkIndex: i, Prompt: "p", Status: StatusOK}
				if err := w.Append(rec); err != nil {
					t.Errorf("concurrent Append: %v", err)
					return
				}
			}
		}()
	}
	wg.Wait()
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	records, err := loadRecords(dir)
	if err != nil {
		t.Fatalf("loadRecords: %v", err)
	}
	if len(records) != goroutines*perGoroutine {
		t.Fatalf("records = %d, want %d", len(records), goroutines*perGoroutine)
	}
	seen := map[int]bool{}
	for _, rec := range records {
		if seen[rec.Index] {
			t.Fatalf("duplicate record index %d", rec.Index)
		}
		seen[rec.Index] = true
	}
	for want := 0; want < goroutines*perGoroutine; want++ {
		if !seen[want] {
			t.Fatalf("missing record index %d", want)
		}
	}
}

func TestPruneKeepsNewestRunsAndDropsCorruptFirst(t *testing.T) {
	t.Parallel()
	root := t.TempDir()

	mkRun := func(name string, startedAt time.Time) {
		t.Helper()
		dir := filepath.Join(root, name)
		if err := os.MkdirAll(dir, fsutil.DirPerms); err != nil {
			t.Fatalf("mkdir %s: %v", name, err)
		}
		meta := testMeta(name)
		meta.StartedAt = startedAt
		raw, err := json.Marshal(meta)
		if err != nil {
			t.Fatalf("marshal meta: %v", err)
		}
		if err := fsutil.WriteFileAtomic(filepath.Join(dir, MetaFileName), raw, fsutil.FilePerms); err != nil {
			t.Fatalf("write meta: %v", err)
		}
	}

	base := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	mkRun("old-1", base)
	mkRun("mid", base.Add(24*time.Hour))
	mkRun("new-1", base.Add(48*time.Hour))
	// Corrupt dir: no meta.json — must be pruned before dated runs.
	if err := os.MkdirAll(filepath.Join(root, "corrupt"), fsutil.DirPerms); err != nil {
		t.Fatalf("mkdir corrupt: %v", err)
	}

	// keep=3 over 4 dirs: only the corrupt run (sorted oldest) goes.
	if err := Prune(root, 3); err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "corrupt")); !os.IsNotExist(err) {
		t.Fatalf("corrupt should have been pruned")
	}
	for _, kept := range []string{"mid", "new-1", "old-1"} {
		if _, err := os.Stat(filepath.Join(root, kept)); err != nil {
			t.Fatalf("%s should have survived prune: %v", kept, err)
		}
	}

	// keep=1: everything but the newest goes.
	if err := Prune(root, 1); err != nil {
		t.Fatalf("Prune keep=1: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "new-1")); err != nil {
		t.Fatalf("new-1 should have survived: %v", err)
	}
	for _, gone := range []string{"mid", "old-1"} {
		if _, err := os.Stat(filepath.Join(root, gone)); !os.IsNotExist(err) {
			t.Fatalf("%s should have been pruned at keep=1", gone)
		}
	}

	// RetainRuns <= 0 falls back to the default and must not panic.
	if err := Prune(root, 0); err != nil {
		t.Fatalf("Prune default: %v", err)
	}
}

func TestListRunsNewestFirstWithWorstStatus(t *testing.T) {
	t.Parallel()
	root := t.TempDir()

	writeRun := func(runID string, at time.Time, statuses ...string) {
		t.Helper()
		dir := filepath.Join(root, runID)
		meta := testMeta(runID)
		meta.StartedAt = at
		w, err := Open(dir, meta)
		if err != nil {
			t.Fatalf("Open %s: %v", runID, err)
		}
		for _, st := range statuses {
			rec := Record{Phase: PhasePhase1, ChunkIndex: 0, Status: st}
			if st == StatusParseFailed {
				rec.Error = "invalid phase-1 JSON"
			}
			if err := w.Append(rec); err != nil {
				t.Fatalf("Append: %v", err)
			}
		}
		if err := w.Close(); err != nil {
			t.Fatalf("Close: %v", err)
		}
	}

	base := time.Date(2026, 8, 20, 0, 0, 0, 0, time.UTC)
	writeRun("r-ok", base, StatusOK, StatusOK)
	writeRun("r-parse", base.Add(time.Hour), StatusOK, StatusParseFailed)
	writeRun("r-error", base.Add(2*time.Hour), StatusError)

	summaries, err := ListRuns(root)
	if err != nil {
		t.Fatalf("ListRuns: %v", err)
	}
	if len(summaries) != 3 {
		t.Fatalf("summaries = %d, want 3", len(summaries))
	}
	wantOrder := []string{"r-error", "r-parse", "r-ok"}
	for i, want := range wantOrder {
		if summaries[i].RunID != want {
			t.Fatalf("summaries[%d].RunID = %q, want %q", i, summaries[i].RunID, want)
		}
	}
	if got := summaries[0].Status; got != StatusError {
		t.Fatalf("r-error status = %q, want %q", got, StatusError)
	}
	if got := summaries[1].Status; got != StatusParseFailed {
		t.Fatalf("r-parse status = %q, want %q", got, StatusParseFailed)
	}
	if got := summaries[2].Calls; got != 2 {
		t.Fatalf("r-ok calls = %d, want 2", got)
	}
}

func TestLoadRunMissingReturnsSentinel(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	if _, _, err := LoadRun(root, "nope"); !errors.Is(err, ErrRunNotFound) {
		t.Fatalf("LoadRun missing = %v, want ErrRunNotFound", err)
	}
}
