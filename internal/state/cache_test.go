package state

import (
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestStateCache_GetState_MissThenHit(t *testing.T) {
	home := t.TempDir()
	setTestHome(t, home)

	sc := NewStateCache()

	// Save a state so Load has something to read.
	initial := &State{
		LastRunUTC:  time.Now().UTC().Truncate(time.Second),
		RepoHeadSHA: "abc123",
		ChatHashes:  map[string]string{"/a": "h1"},
	}
	if err := Save("", "project-a", initial); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// First call — cache miss, reads from disk.
	st1, err := sc.GetState("", "project-a")
	if err != nil {
		t.Fatalf("GetState (miss): %v", err)
	}
	if st1.RepoHeadSHA != "abc123" {
		t.Fatalf("RepoHeadSHA = %q, want %q", st1.RepoHeadSHA, "abc123")
	}

	// Second call — cache hit, returns same pointer.
	st2, err := sc.GetState("", "project-a")
	if err != nil {
		t.Fatalf("GetState (hit): %v", err)
	}
	if st1 != st2 {
		t.Fatalf("expected same pointer on cache hit, got different pointers")
	}
}

func TestStateCache_GetState_MtimeInvalidation(t *testing.T) {
	home := t.TempDir()
	setTestHome(t, home)

	sc := NewStateCache()

	// Save initial state.
	if err := Save("", "project-a", &State{RepoHeadSHA: "v1"}); err != nil {
		t.Fatalf("Save v1: %v", err)
	}

	// Prime the cache.
	st1, err := sc.GetState("", "project-a")
	if err != nil {
		t.Fatalf("GetState v1: %v", err)
	}
	if st1.RepoHeadSHA != "v1" {
		t.Fatalf("RepoHeadSHA = %q, want %q", st1.RepoHeadSHA, "v1")
	}

	// Save a new state (changes mtime+size).
	if err := Save("", "project-a", &State{RepoHeadSHA: "v2"}); err != nil {
		t.Fatalf("Save v2: %v", err)
	}

	// GetState should detect mtime change and re-read.
	st2, err := sc.GetState("", "project-a")
	if err != nil {
		t.Fatalf("GetState v2: %v", err)
	}
	if st2.RepoHeadSHA != "v2" {
		t.Fatalf("RepoHeadSHA = %q, want %q", st2.RepoHeadSHA, "v2")
	}
	if st1 == st2 {
		t.Fatalf("expected different pointer after mtime change")
	}
}

func TestStateCache_Invalidate(t *testing.T) {
	home := t.TempDir()
	setTestHome(t, home)

	sc := NewStateCache()

	if err := Save("", "project-a", &State{RepoHeadSHA: "v1"}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Prime cache.
	st1, err := sc.GetState("", "project-a")
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}

	// Invalidate.
	sc.Invalidate("project-a")

	// Save new state.
	if err := Save("", "project-a", &State{RepoHeadSHA: "v2"}); err != nil {
		t.Fatalf("Save v2: %v", err)
	}

	// Next GetState should read from disk (invalidated, not from cache).
	st2, err := sc.GetState("", "project-a")
	if err != nil {
		t.Fatalf("GetState after invalidate: %v", err)
	}
	if st2.RepoHeadSHA != "v2" {
		t.Fatalf("RepoHeadSHA = %q, want %q", st2.RepoHeadSHA, "v2")
	}
	if st1 == st2 {
		t.Fatalf("expected different pointer after Invalidate")
	}
}

func TestStateCache_Concurrent(t *testing.T) {
	home := t.TempDir()
	setTestHome(t, home)

	sc := NewStateCache()

	if err := Save("", "project-a", &State{RepoHeadSHA: "v1"}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	var wg sync.WaitGroup
	const goroutines = 10
	wg.Add(goroutines)
	for range goroutines {
		go func() {
			defer wg.Done()
			_, _ = sc.GetState("", "project-a")
		}()
	}
	wg.Wait()

	// Also test concurrent Invalidate + GetState.
	wg.Add(goroutines * 2)
	for range goroutines {
		go func() {
			defer wg.Done()
			sc.Invalidate("project-a")
		}()
		go func() {
			defer wg.Done()
			_, _ = sc.GetState("", "project-a")
		}()
	}
	wg.Wait()
}

func TestStateCache_GetHistory_MissThenHit(t *testing.T) {
	home := t.TempDir()
	setTestHome(t, home)

	sc := NewStateCache()
	outputRoot := filepath.Join(home, "output")

	// Save a history so LoadHistory has something to read.
	hist := &History{
		Version: historyVersion,
		Days: []DaySummary{
			{Date: "2026-05-28", Runs: 1, FindingsNew: 3},
		},
	}
	if err := SaveHistory(outputRoot, "project-a", hist); err != nil {
		t.Fatalf("SaveHistory: %v", err)
	}

	// First call — cache miss.
	h1, err := sc.GetHistory(outputRoot, "project-a")
	if err != nil {
		t.Fatalf("GetHistory (miss): %v", err)
	}
	if len(h1.Days) != 1 {
		t.Fatalf("Days = %d, want 1", len(h1.Days))
	}

	// Second call — cache hit, same pointer.
	h2, err := sc.GetHistory(outputRoot, "project-a")
	if err != nil {
		t.Fatalf("GetHistory (hit): %v", err)
	}
	if h1 != h2 {
		t.Fatalf("expected same pointer on cache hit")
	}
}

func TestStateCache_GetHistory_MtimeInvalidation(t *testing.T) {
	home := t.TempDir()
	setTestHome(t, home)

	sc := NewStateCache()
	outputRoot := filepath.Join(home, "output")

	hist1 := &History{
		Version: historyVersion,
		Days:    []DaySummary{{Date: "2026-05-28", Runs: 1}},
	}
	if err := SaveHistory(outputRoot, "project-a", hist1); err != nil {
		t.Fatalf("SaveHistory v1: %v", err)
	}

	// Prime cache.
	h1, err := sc.GetHistory(outputRoot, "project-a")
	if err != nil {
		t.Fatalf("GetHistory v1: %v", err)
	}

	// Save new history (changes mtime).
	hist2 := &History{
		Version: historyVersion,
		Days:    []DaySummary{{Date: "2026-05-28", Runs: 2}},
	}
	if err := SaveHistory(outputRoot, "project-a", hist2); err != nil {
		t.Fatalf("SaveHistory v2: %v", err)
	}

	// Should detect mtime change and re-read.
	h2, err := sc.GetHistory(outputRoot, "project-a")
	if err != nil {
		t.Fatalf("GetHistory v2: %v", err)
	}
	if h2.Days[0].Runs != 2 {
		t.Fatalf("Runs = %d, want 2", h2.Days[0].Runs)
	}
	if h1 == h2 {
		t.Fatalf("expected different pointer after mtime change")
	}
}

func TestStateCache_GetState_MissingProject(t *testing.T) {
	home := t.TempDir()
	setTestHome(t, home)

	sc := NewStateCache()

	// GetState for a project that has no state.json should return a
	// default state (same as Load behavior).
	st, err := sc.GetState("", "nonexistent")
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}
	if st == nil {
		t.Fatalf("expected non-nil default state")
	}
	if st.Version != StateVersion {
		t.Fatalf("Version = %d, want %d", st.Version, StateVersion)
	}
}
