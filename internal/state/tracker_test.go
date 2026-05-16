package state

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestLoadStateReturnsDefaultWhenMissing(t *testing.T) {
	home := t.TempDir()
	setTestHome(t, home)

	current, err := Load("", "project-a")
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if current == nil {
		t.Fatalf("Load returned nil state")
	}
	if len(current.ChatHashes) != 0 {
		t.Fatalf("ChatHashes should be empty, got %v", current.ChatHashes)
	}
	if len(current.FindingHashes) != 0 {
		t.Fatalf("FindingHashes should be empty, got %v", current.FindingHashes)
	}
	if current.Version != StateVersion {
		t.Fatalf("Version = %d, want %d", current.Version, StateVersion)
	}
}

func TestSaveAndLoadStateRoundTrip(t *testing.T) {
	home := t.TempDir()
	setTestHome(t, home)

	lastRun := time.Now().UTC().Truncate(time.Second)
	initial := &State{
		LastRunUTC:  lastRun,
		RepoHeadSHA: "abc123",
		ChatHashes: map[string]string{
			"/path/a.jsonl": "hash-a",
			"/path/b.jsonl": "hash-b",
		},
		FindingHashes: []string{"hash-1", "hash-2"},
		ProviderUsage: map[string]ProviderUsage{
			"copilot-sdk": {Runs: 3, TotalTokens: 100},
		},
		UsageStats: map[string]int64{"tokens_prompt": 1000},
	}

	if err := Save("", "project-a", initial); err != nil {
		t.Fatalf("Save returned error: %v", err)
	}

	loaded, err := Load("", "project-a")
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}

	if !loaded.LastRunUTC.Equal(lastRun) {
		t.Fatalf("LastRunUTC = %s, want %s", loaded.LastRunUTC, lastRun)
	}
	if loaded.RepoHeadSHA != initial.RepoHeadSHA {
		t.Fatalf("RepoHeadSHA = %q, want %q", loaded.RepoHeadSHA, initial.RepoHeadSHA)
	}
	if !reflect.DeepEqual(loaded.ChatHashes, initial.ChatHashes) {
		t.Fatalf("ChatHashes = %v, want %v", loaded.ChatHashes, initial.ChatHashes)
	}
	if !reflect.DeepEqual(loaded.FindingHashes, initial.FindingHashes) {
		t.Fatalf("FindingHashes = %v, want %v", loaded.FindingHashes, initial.FindingHashes)
	}
	if !reflect.DeepEqual(loaded.ProviderUsage, initial.ProviderUsage) {
		t.Fatalf("ProviderUsage = %v, want %v", loaded.ProviderUsage, initial.ProviderUsage)
	}
}

func TestLoadStateInvalidJSONReturnsError(t *testing.T) {
	home := t.TempDir()
	setTestHome(t, home)

	statePath, err := PathForProject("", "project-a")
	if err != nil {
		t.Fatalf("PathForProject error: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(statePath), 0o755); err != nil {
		t.Fatalf("MkdirAll returned error: %v", err)
	}
	if err := os.WriteFile(statePath, []byte("{invalid-json"), 0o644); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}

	_, err = Load("", "project-a")
	if err == nil {
		t.Fatalf("Load expected JSON error")
	}
}

func TestSaveStateRejectsInvalidProjectName(t *testing.T) {
	home := t.TempDir()
	setTestHome(t, home)

	err := Save("", "bad/name", &State{})
	if err == nil {
		t.Fatalf("Save expected invalid project name error")
	}
	if !strings.Contains(err.Error(), "invalid path separator") {
		t.Fatalf("error = %q, want invalid path separator", err)
	}
}

func TestChatCacheKeyDeterministic(t *testing.T) {
	a := ChatCacheKey("/x/a.jsonl", "fh1", "head1")
	b := ChatCacheKey("/x/a.jsonl", "fh1", "head1")
	if a != b {
		t.Fatalf("expected stable cache key, got %q vs %q", a, b)
	}
	c := ChatCacheKey("/x/a.jsonl", "fh2", "head1")
	if a == c {
		t.Fatalf("cache key should change when file hash changes")
	}
	d := ChatCacheKey("/x/a.jsonl", "fh1", "head2")
	if a == d {
		t.Fatalf("cache key should change when repo head changes")
	}
}

func TestTruncateError(t *testing.T) {
	if got := TruncateError(""); got != "" {
		t.Fatalf("empty in -> empty out, got %q", got)
	}
	short := "boom: oh no"
	if got := TruncateError(short); got != short {
		t.Fatalf("short string should pass through, got %q", got)
	}
	long := strings.Repeat("a", maxErrorLen+50)
	got := TruncateError(long)
	gotRunes := []rune(got)
	if len(gotRunes) != maxErrorLen+1 {
		t.Fatalf("truncated length = %d runes, want %d", len(gotRunes), maxErrorLen+1)
	}
	if gotRunes[len(gotRunes)-1] != '…' {
		t.Fatalf("truncated string should end with ellipsis, got %q", string(gotRunes[len(gotRunes)-3:]))
	}
}

func TestSaveAtomicCleansUpStaleTempFile(t *testing.T) {
	home := t.TempDir()
	setTestHome(t, home)

	statePath, err := PathForProject("", "project-a")
	if err != nil {
		t.Fatalf("PathForProject error: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(statePath), 0o755); err != nil {
		t.Fatalf("MkdirAll error: %v", err)
	}
	// Simulate an interrupted prior Save by pre-populating .tmp with garbage.
	if err := os.WriteFile(statePath+".tmp", []byte("garbage from a prior crash"), 0o644); err != nil {
		t.Fatalf("seed tmp file: %v", err)
	}

	want := &State{LastRunUTC: time.Now().UTC().Truncate(time.Second), RepoHeadSHA: "sha"}
	if err := Save("", "project-a", want); err != nil {
		t.Fatalf("Save error: %v", err)
	}

	loaded, err := Load("", "project-a")
	if err != nil {
		t.Fatalf("Load error: %v", err)
	}
	if loaded.RepoHeadSHA != "sha" {
		t.Fatalf("RepoHeadSHA = %q, want sha", loaded.RepoHeadSHA)
	}
	if _, err := os.Stat(statePath + ".tmp"); !os.IsNotExist(err) {
		t.Fatalf("stale .tmp should be replaced by rename; stat err=%v", err)
	}
}

func TestSaveRoundTripsLastRunPerCategoryAndProviderHealth(t *testing.T) {
	home := t.TempDir()
	setTestHome(t, home)

	now := time.Now().UTC().Truncate(time.Second)
	initial := &State{
		ProviderUsage: map[string]ProviderUsage{
			"copilot-sdk": {
				Runs:           1,
				LastSuccessUTC: now,
				LastError:      "rate limited",
			},
		},
		LastRunPerCategory: map[string]time.Time{
			"lint-rule": now,
			"test":      now.Add(-time.Hour),
		},
	}
	if err := Save("", "project-b", initial); err != nil {
		t.Fatalf("Save: %v", err)
	}
	loaded, err := Load("", "project-b")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	got := loaded.ProviderUsage["copilot-sdk"]
	if !got.LastSuccessUTC.Equal(now) {
		t.Fatalf("LastSuccessUTC round-trip mismatch: got %s want %s", got.LastSuccessUTC, now)
	}
	if got.LastError != "rate limited" {
		t.Fatalf("LastError = %q, want %q", got.LastError, "rate limited")
	}
	if !loaded.LastRunPerCategory["lint-rule"].Equal(now) {
		t.Fatalf("LastRunPerCategory[lint-rule] round-trip mismatch")
	}
	if !loaded.LastRunPerCategory["test"].Equal(now.Add(-time.Hour)) {
		t.Fatalf("LastRunPerCategory[test] round-trip mismatch")
	}
}

func setTestHome(t *testing.T, home string) {
	t.Helper()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
}
