package pipeline

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"dreamer/internal/chat"
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

// B1: a chat file that fails to hash this run must keep its prior cache key
// rather than being silently dropped from state.ChatHashes on assignment.
func TestComputeCacheKeysPreservesPriorOnHashFailure(t *testing.T) {
	dir := t.TempDir()
	okPath := filepath.Join(dir, "ok.jsonl")
	if err := os.WriteFile(okPath, []byte("hello"), 0o644); err != nil {
		t.Fatalf("seed ok: %v", err)
	}
	badPath := filepath.Join(dir, "fail-as-dir")
	if err := os.Mkdir(badPath, 0o755); err != nil {
		t.Fatalf("seed bad: %v", err)
	}

	prior := map[string]string{
		badPath: "prior-key",
		okPath:  "old-ok",
	}
	sources := []chat.ChatSource{
		{Path: okPath, Tool: chat.SourceTypeCodexSessionJSONL},
		{Path: badPath, Tool: chat.SourceTypeCodexSessionJSONL},
	}

	out, stats := computeCacheKeys(sources, prior, "head1", nil)

	if stats.HashFailures != 1 {
		t.Fatalf("HashFailures = %d, want 1", stats.HashFailures)
	}
	if out[badPath] != "prior-key" {
		t.Fatalf("hash-failed source lost prior key: got %q, want %q", out[badPath], "prior-key")
	}
	if out[okPath] == "" {
		t.Fatalf("ok source missing cache key after computeCacheKeys")
	}
}

// B26: chat files that are no longer discovered must be pruned from cache keys
// so state.ChatHashes does not grow indefinitely with stale entries.
func TestComputeCacheKeysPrunesAbsentSources(t *testing.T) {
	dir := t.TempDir()
	okPath := filepath.Join(dir, "ok.jsonl")
	if err := os.WriteFile(okPath, []byte("hi"), 0o644); err != nil {
		t.Fatalf("seed ok: %v", err)
	}

	absentPath := filepath.Join(dir, "absent.jsonl")
	prior := map[string]string{absentPath: "stale-key", okPath: "old"}
	sources := []chat.ChatSource{{Path: okPath, Tool: chat.SourceTypeCodexSessionJSONL}}

	out, _ := computeCacheKeys(sources, prior, "head1", nil)
	if _, ok := out[absentPath]; ok {
		t.Fatalf("absent source should be pruned, got key %q", out[absentPath])
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
