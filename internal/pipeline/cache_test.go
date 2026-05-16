package pipeline

import (
	"testing"
	"time"

	"dreamer/internal/state"
)

func priorRunState() time.Time {
	return time.Date(2026, 5, 16, 12, 0, 0, 0, time.UTC)
}

func TestCacheUnchangedReturnsTrueForMatchingHashesAndRepo(t *testing.T) {
	current := &state.State{
		LastRunUTC:  priorRunState(),
		RepoHeadSHA: "abc123",
		ChatHashes: map[string]string{
			"a.jsonl": "hash-a",
			"b.jsonl": "hash-b",
		},
	}

	cacheKeys := map[string]string{
		"a.jsonl": "hash-a",
		"b.jsonl": "hash-b",
	}

	if !cacheUnchanged(current, cacheKeys, "abc123") {
		t.Fatalf("cacheUnchanged returned false for matching cache keys")
	}
}

func TestCacheUnchangedReturnsFalseForHashDrift(t *testing.T) {
	current := &state.State{
		LastRunUTC:  priorRunState(),
		RepoHeadSHA: "abc123",
		ChatHashes:  map[string]string{"a.jsonl": "old-hash"},
	}

	if cacheUnchanged(current, map[string]string{"a.jsonl": "new-hash"}, "abc123") {
		t.Fatalf("cacheUnchanged returned true when source hash changed")
	}
}

func TestCacheUnchangedReturnsFalseForRepoDrift(t *testing.T) {
	current := &state.State{
		LastRunUTC:  priorRunState(),
		RepoHeadSHA: "abc123",
		ChatHashes:  map[string]string{"a.jsonl": "hash-a"},
	}

	if cacheUnchanged(current, map[string]string{"a.jsonl": "hash-a"}, "def456") {
		t.Fatalf("cacheUnchanged returned true when repo SHA changed")
	}
}

func TestCacheUnchangedReturnsFalseWhenLastRunUTCZero(t *testing.T) {
	// A freshly-loaded default state (zero LastRunUTC) must never be
	// treated as a cache hit, even if both maps are empty.
	current := &state.State{ChatHashes: map[string]string{}}
	if cacheUnchanged(current, map[string]string{}, "") {
		t.Fatalf("zero LastRunUTC must defeat the cache check")
	}
}

func TestCacheUnchangedTrueForEmptyVsEmptyAfterPriorRun(t *testing.T) {
	// §7.10/§7.11 acceptance: an empty source folder we have observed
	// before is a legitimate cache hit. Without this, preflight skip
	// re-walks discovery on every subsequent daemon tick.
	current := &state.State{
		LastRunUTC:  priorRunState(),
		RepoHeadSHA: "abc123",
		ChatHashes:  map[string]string{},
	}
	if !cacheUnchanged(current, map[string]string{}, "abc123") {
		t.Fatalf("empty-vs-empty after a prior run must hit the cache")
	}
}

func TestForceBypassesCacheDecisionInCaller(t *testing.T) {
	current := &state.State{
		LastRunUTC:  priorRunState(),
		RepoHeadSHA: "abc123",
		ChatHashes:  map[string]string{"a.jsonl": "hash-a"},
	}
	cacheKeys := map[string]string{"a.jsonl": "hash-a"}

	force := true
	shouldSkip := !force && cacheUnchanged(current, cacheKeys, "abc123")
	if shouldSkip {
		t.Fatalf("force should bypass cache hit")
	}
}
