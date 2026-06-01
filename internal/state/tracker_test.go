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

// B12: state.json with a version newer than what this binary supports must
// be refused rather than silently downgraded.
func TestLoadStateRefusesNewerVersion(t *testing.T) {
	home := t.TempDir()
	setTestHome(t, home)

	statePath, err := PathForProject("", "project-a")
	if err != nil {
		t.Fatalf("PathForProject: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(statePath), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(statePath, []byte(`{"version":999}`), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	_, err = Load("", "project-a")
	if err == nil {
		t.Fatalf("Load must refuse newer state version")
	}
	if !strings.Contains(err.Error(), "version") {
		t.Fatalf("error %q must mention version", err)
	}
}

// B12: before any in-place upgrade Save must produce a backup so the user
// can roll forward and back.
func TestSaveWritesBackupBeforeUpgradeWrite(t *testing.T) {
	home := t.TempDir()
	setTestHome(t, home)

	statePath, err := PathForProject("", "project-a")
	if err != nil {
		t.Fatalf("PathForProject: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(statePath), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	// Seed a pre-existing v1 state.json.
	prior := []byte(`{"version":1,"chat_hashes":{}}`)
	if err := os.WriteFile(statePath, prior, 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// Load + Save: backup must materialize because the prior on-disk
	// content is from a version (1) older than or equal to current.
	loaded, err := Load("", "project-a")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := Save("", "project-a", loaded); err != nil {
		t.Fatalf("Save: %v", err)
	}
	backupPath := statePath + ".v1.bak"
	if _, err := os.Stat(backupPath); err != nil {
		t.Fatalf("expected backup at %q, stat err=%v", backupPath, err)
	}
}

// B27: explicit version=0 is suspicious (corrupted/truncated) and must not
// be silently promoted to current.
func TestLoadStateRefusesExplicitZeroVersion(t *testing.T) {
	home := t.TempDir()
	setTestHome(t, home)
	statePath, err := PathForProject("", "project-a")
	if err != nil {
		t.Fatalf("PathForProject: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(statePath), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(statePath, []byte(`{"version":0,"chat_hashes":{}}`), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	_, err = Load("", "project-a")
	if err == nil {
		t.Fatalf("Load must refuse explicit version=0")
	}
}

// B12 + B21 end-to-end: a v1 state.json gets ChatHashes dropped on Load,
// Save then produces a v2 file and a stable .v1.bak that still contains
// the pre-upgrade content.
func TestLoadSaveV1ToV2MigrationEndToEnd(t *testing.T) {
	home := t.TempDir()
	setTestHome(t, home)
	statePath, err := PathForProject("", "project-a")
	if err != nil {
		t.Fatalf("PathForProject: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(statePath), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	prior := []byte(`{"version":1,"chat_hashes":{"/a":"k1","/b":"k2"},"finding_hashes":["f1","f2"]}`)
	if err := os.WriteFile(statePath, prior, 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	res, err := LoadWithResult("", "project-a")
	if err != nil {
		t.Fatalf("LoadWithResult: %v", err)
	}
	if !res.Migrated {
		t.Fatalf("LoadWithResult.Migrated = false, want true")
	}
	if res.PriorVersion != 1 || res.CurrentVersion != StateVersion {
		t.Fatalf("versions = (%d -> %d), want (1 -> %d)", res.PriorVersion, res.CurrentVersion, StateVersion)
	}
	if len(res.State.ChatHashes) != 0 {
		t.Fatalf("v1 ChatHashes must be dropped on upgrade, got %v", res.State.ChatHashes)
	}
	if len(res.State.FindingHashes) != 2 {
		t.Fatalf("FindingHashes must survive upgrade, got %v", res.State.FindingHashes)
	}

	if err := Save("", "project-a", res.State); err != nil {
		t.Fatalf("Save: %v", err)
	}

	backup, err := os.ReadFile(statePath + ".v1.bak")
	if err != nil {
		t.Fatalf("expected .v1.bak, err=%v", err)
	}
	if string(backup) != string(prior) {
		t.Fatalf("backup mismatch: got %s, want %s", backup, prior)
	}

	// A second Save (same-version v2 -> v2) must NOT overwrite the backup.
	if err := Save("", "project-a", res.State); err != nil {
		t.Fatalf("second Save: %v", err)
	}
	backup2, err := os.ReadFile(statePath + ".v1.bak")
	if err != nil {
		t.Fatalf("backup vanished on same-version save, err=%v", err)
	}
	if string(backup2) != string(prior) {
		t.Fatalf("backup overwritten by same-version save: got %s, want %s", backup2, prior)
	}
}

// B27: a state file with no `version` field is a pre-versioning legacy file
// and must be upgraded to current rather than rejected.
func TestLoadStateAcceptsMissingVersionAsLegacy(t *testing.T) {
	home := t.TempDir()
	setTestHome(t, home)
	statePath, err := PathForProject("", "project-a")
	if err != nil {
		t.Fatalf("PathForProject: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(statePath), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(statePath, []byte(`{"chat_hashes":{"/a":"k"},"finding_hashes":["f1"]}`), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	loaded, err := Load("", "project-a")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.Version != StateVersion {
		t.Fatalf("Version = %d, want %d (after legacy upgrade)", loaded.Version, StateVersion)
	}
	// Legacy files run through all migrations including v1→v2 which
	// clears ChatHashes (cache key derivation changed).
	if len(loaded.ChatHashes) != 0 {
		t.Fatalf("ChatHashes must be cleared during v1→v2 migration, got %v", loaded.ChatHashes)
	}
	// FindingHashes must survive the migration chain.
	if len(loaded.FindingHashes) != 1 || loaded.FindingHashes[0] != "f1" {
		t.Fatalf("FindingHashes lost during upgrade: %v", loaded.FindingHashes)
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

// B21: length-prefix each field so a collision cannot be constructed by
// shifting field boundaries (e.g. path+separator vs separator+fileHash).
func TestChatCacheKeyBoundariesAreUnambiguous(t *testing.T) {
	// A null byte inside a field can shift the field boundary under the
	// previous single-byte-separator scheme. Length-prefixing must keep
	// the boundary unambiguous even with embedded NULs.
	a := ChatCacheKey("a\x00b", "c", "d")
	b := ChatCacheKey("a", "b\x00c", "d")
	if a == b {
		t.Fatalf("split-boundary fields collided: a=%q b=%q (length-prefix required)", a, b)
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
