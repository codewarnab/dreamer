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

	current, err := LoadState("project-a")
	if err != nil {
		t.Fatalf("LoadState returned error: %v", err)
	}
	if current == nil {
		t.Fatalf("LoadState returned nil state")
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

	if err := SaveState("project-a", initial); err != nil {
		t.Fatalf("SaveState returned error: %v", err)
	}

	loaded, err := LoadState("project-a")
	if err != nil {
		t.Fatalf("LoadState returned error: %v", err)
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

	_, err = LoadState("project-a")
	if err == nil {
		t.Fatalf("LoadState expected JSON error")
	}
}

func TestSaveStateRejectsInvalidProjectName(t *testing.T) {
	home := t.TempDir()
	setTestHome(t, home)

	err := SaveState("bad/name", &State{})
	if err == nil {
		t.Fatalf("SaveState expected invalid project name error")
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

func setTestHome(t *testing.T, home string) {
	t.Helper()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
}
