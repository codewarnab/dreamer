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
	if len(current.AnalyzedChatIDs) != 0 {
		t.Fatalf("AnalyzedChatIDs should be empty, got %v", current.AnalyzedChatIDs)
	}
	if len(current.UsageStats) != 0 {
		t.Fatalf("UsageStats should be empty, got %v", current.UsageStats)
	}
}

func TestSaveAndLoadStateRoundTrip(t *testing.T) {
	home := t.TempDir()
	setTestHome(t, home)

	lastRun := time.Now().UTC().Truncate(time.Second)
	initial := &State{
		LastRun:            lastRun,
		AnalyzedChatIDs:    []string{"chat-1", "chat-2"},
		ResumableSessionID: "session-123",
		UsageStats: map[string]int64{
			"tokens_prompt": 1000,
			"tokens_output": 300,
		},
	}

	if err := SaveState("project-a", initial); err != nil {
		t.Fatalf("SaveState returned error: %v", err)
	}

	loaded, err := LoadState("project-a")
	if err != nil {
		t.Fatalf("LoadState returned error: %v", err)
	}

	if !loaded.LastRun.Equal(lastRun) {
		t.Fatalf("LastRun = %s, want %s", loaded.LastRun, lastRun)
	}
	if !reflect.DeepEqual(loaded.AnalyzedChatIDs, initial.AnalyzedChatIDs) {
		t.Fatalf("AnalyzedChatIDs = %v, want %v", loaded.AnalyzedChatIDs, initial.AnalyzedChatIDs)
	}
	if loaded.ResumableSessionID != initial.ResumableSessionID {
		t.Fatalf("ResumableSessionID = %q, want %q", loaded.ResumableSessionID, initial.ResumableSessionID)
	}
	if !reflect.DeepEqual(loaded.UsageStats, initial.UsageStats) {
		t.Fatalf("UsageStats = %v, want %v", loaded.UsageStats, initial.UsageStats)
	}
}

func TestLoadStateInvalidJSONReturnsError(t *testing.T) {
	home := t.TempDir()
	setTestHome(t, home)

	target := filepath.Join(home, rootDirName, "project-a")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatalf("MkdirAll returned error: %v", err)
	}
	if err := os.WriteFile(filepath.Join(target, stateFile), []byte("{invalid-json"), 0o644); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}

	_, err := LoadState("project-a")
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

func setTestHome(t *testing.T, home string) {
	t.Helper()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
}
