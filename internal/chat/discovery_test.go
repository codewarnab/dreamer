package chat

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDiscoverChatsFromRootsFindsCopilotAndVSCodeChats(t *testing.T) {
	homeDir := t.TempDir()
	appDataDir := t.TempDir()

	copilotFile := filepath.Join(homeDir, ".copilot", "session-state", "a", "b", "session.jsonl")
	vscodeJSON := filepath.Join(appDataDir, "Code", "User", "workspaceStorage", "workspace-1", "chatSessions", "alpha", "chat.json")
	vscodeJSONL := filepath.Join(appDataDir, "Code", "User", "workspaceStorage", "workspace-1", "chatSessions", "beta", "chat.jsonl")
	ignoredFile := filepath.Join(appDataDir, "Code", "User", "workspaceStorage", "workspace-1", "chatSessions", "ignore.txt")

	writeFixtureFile(t, copilotFile, "copilot")
	writeFixtureFile(t, vscodeJSON, "{}")
	writeFixtureFile(t, vscodeJSONL, "{}")
	writeFixtureFile(t, ignoredFile, "ignored")

	copilotTime := time.Now().UTC().Add(-2 * time.Hour).Truncate(time.Second)
	vscodeJSONTime := time.Now().UTC().Add(-1 * time.Hour).Truncate(time.Second)
	vscodeJSONLTime := time.Now().UTC().Truncate(time.Second)
	setModTime(t, copilotFile, copilotTime)
	setModTime(t, vscodeJSON, vscodeJSONTime)
	setModTime(t, vscodeJSONL, vscodeJSONLTime)

	sources, err := discoverChatsFromRoots(homeDir, appDataDir)
	if err != nil {
		t.Fatalf("discoverChatsFromRoots returned error: %v", err)
	}
	if len(sources) != 3 {
		t.Fatalf("expected 3 sources, got %d", len(sources))
	}

	byPath := make(map[string]ChatSource, len(sources))
	for _, source := range sources {
		byPath[source.Path] = source
	}

	assertSource(t, byPath, copilotFile, SourceTypeCopilotSessionJSONL, copilotTime)
	assertSource(t, byPath, vscodeJSON, SourceTypeVSCodeChatSession, vscodeJSONTime)
	assertSource(t, byPath, vscodeJSONL, SourceTypeVSCodeChatSession, vscodeJSONLTime)
}

func TestDiscoverChatsFromRootsMissingDirectories(t *testing.T) {
	sources, err := discoverChatsFromRoots(filepath.Join(t.TempDir(), "missing-home"), filepath.Join(t.TempDir(), "missing-appdata"))
	if err != nil {
		t.Fatalf("discoverChatsFromRoots returned error: %v", err)
	}
	if len(sources) != 0 {
		t.Fatalf("expected no sources, got %d", len(sources))
	}
}

func TestDiscoverChatsReadsDefaultRoots(t *testing.T) {
	homeDir := t.TempDir()
	appDataDir := t.TempDir()
	setTestHome(t, homeDir)
	t.Setenv("APPDATA", appDataDir)

	copilotFile := filepath.Join(homeDir, ".copilot", "session-state", "chat.jsonl")
	vscodeFile := filepath.Join(appDataDir, "Code", "User", "workspaceStorage", "workspace-2", "chatSessions", "chat.json")
	writeFixtureFile(t, copilotFile, "copilot")
	writeFixtureFile(t, vscodeFile, "{}")

	sources, err := DiscoverChats("unused-project-path")
	if err != nil {
		t.Fatalf("DiscoverChats returned error: %v", err)
	}
	if len(sources) != 2 {
		t.Fatalf("expected 2 sources, got %d", len(sources))
	}
}

func assertSource(t *testing.T, byPath map[string]ChatSource, path string, expectedTool SourceType, expectedModTime time.Time) {
	t.Helper()

	source, ok := byPath[path]
	if !ok {
		t.Fatalf("missing source for path %q", path)
	}
	if source.Tool != expectedTool {
		t.Fatalf("tool for %q = %q, want %q", path, source.Tool, expectedTool)
	}
	if !source.ModifiedTime.Equal(expectedModTime) {
		t.Fatalf("modified time for %q = %s, want %s", path, source.ModifiedTime, expectedModTime)
	}
}

func writeFixtureFile(t *testing.T, path string, content string) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll returned error: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}
}

func setModTime(t *testing.T, path string, modTime time.Time) {
	t.Helper()

	if err := os.Chtimes(path, modTime, modTime); err != nil {
		t.Fatalf("Chtimes returned error: %v", err)
	}
}

func setTestHome(t *testing.T, home string) {
	t.Helper()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
}
