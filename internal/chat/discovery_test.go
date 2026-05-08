package chat

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDiscoverChatsFromRootsFindsCopilotVSCodeAndClaudeChats(t *testing.T) {
	homeDir := t.TempDir()
	appDataDir := t.TempDir()
	claudeConfigDir := t.TempDir()
	projectDir := t.TempDir()
	geminiHomeDir := t.TempDir()

	copilotFile := filepath.Join(homeDir, ".copilot", "session-state", "a", "b", "session.jsonl")
	vscodeJSON := filepath.Join(appDataDir, "Code", "User", "workspaceStorage", "workspace-1", "chatSessions", "alpha", "chat.json")
	vscodeJSONL := filepath.Join(appDataDir, "Code", "User", "workspaceStorage", "workspace-1", "chatSessions", "beta", "chat.jsonl")
	claudeJSONL := filepath.Join(claudeConfigDir, "projects", "project-a", "session.jsonl")
	antigravityGlobalPB := filepath.Join(geminiHomeDir, "antigravity", "conversations", "global-session.pb")
	antigravityProjectPB := filepath.Join(projectDir, ".gemini", "antigravity", "conversations", "project-session.pb")
	ignoredFile := filepath.Join(appDataDir, "Code", "User", "workspaceStorage", "workspace-1", "chatSessions", "ignore.txt")

	writeFixtureFile(t, copilotFile, "copilot")
	writeFixtureFile(t, vscodeJSON, "{}")
	writeFixtureFile(t, vscodeJSONL, "{}")
	writeFixtureFile(t, claudeJSONL, "{}")
	writeFixtureFile(t, antigravityGlobalPB, "binary")
	writeFixtureFile(t, antigravityProjectPB, "binary")
	writeFixtureFile(t, ignoredFile, "ignored")

	copilotTime := time.Now().UTC().Add(-3 * time.Hour).Truncate(time.Second)
	vscodeJSONTime := time.Now().UTC().Add(-2 * time.Hour).Truncate(time.Second)
	vscodeJSONLTime := time.Now().UTC().Add(-1 * time.Hour).Truncate(time.Second)
	claudeJSONLTime := time.Now().UTC().Add(-30 * time.Minute).Truncate(time.Second)
	antigravityGlobalTime := time.Now().UTC().Add(-20 * time.Minute).Truncate(time.Second)
	antigravityProjectTime := time.Now().UTC().Truncate(time.Second)
	setModTime(t, copilotFile, copilotTime)
	setModTime(t, vscodeJSON, vscodeJSONTime)
	setModTime(t, vscodeJSONL, vscodeJSONLTime)
	setModTime(t, claudeJSONL, claudeJSONLTime)
	setModTime(t, antigravityGlobalPB, antigravityGlobalTime)
	setModTime(t, antigravityProjectPB, antigravityProjectTime)

	sources, err := discoverChatsFromRoots(homeDir, appDataDir, claudeConfigDir, projectDir, geminiHomeDir)
	if err != nil {
		t.Fatalf("discoverChatsFromRoots returned error: %v", err)
	}
	if len(sources) != 6 {
		t.Fatalf("expected 6 sources, got %d", len(sources))
	}

	byPath := make(map[string]ChatSource, len(sources))
	for _, source := range sources {
		byPath[source.Path] = source
	}

	assertSource(t, byPath, copilotFile, SourceTypeCopilotSessionJSONL, copilotTime)
	assertSource(t, byPath, vscodeJSON, SourceTypeVSCodeChatSession, vscodeJSONTime)
	assertSource(t, byPath, vscodeJSONL, SourceTypeVSCodeChatSession, vscodeJSONLTime)
	assertSource(t, byPath, claudeJSONL, SourceTypeClaudeCodeSession, claudeJSONLTime)
	assertSource(t, byPath, antigravityGlobalPB, SourceTypeAntigravityGemini, antigravityGlobalTime)
	assertSource(t, byPath, antigravityProjectPB, SourceTypeAntigravityGemini, antigravityProjectTime)
}

func TestDiscoverChatsFromRootsMissingDirectories(t *testing.T) {
	sources, err := discoverChatsFromRoots(
		filepath.Join(t.TempDir(), "missing-home"),
		filepath.Join(t.TempDir(), "missing-appdata"),
		filepath.Join(t.TempDir(), "missing-claude"),
		filepath.Join(t.TempDir(), "missing-project"),
		filepath.Join(t.TempDir(), "missing-gemini"),
	)
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
	geminiHomeDir := t.TempDir()
	setTestHome(t, homeDir)
	t.Setenv("APPDATA", appDataDir)
	t.Setenv("GEMINI_HOME", geminiHomeDir)

	copilotFile := filepath.Join(homeDir, ".copilot", "session-state", "chat.jsonl")
	vscodeFile := filepath.Join(appDataDir, "Code", "User", "workspaceStorage", "workspace-2", "chatSessions", "chat.json")
	claudeFile := filepath.Join(homeDir, ".claude", "projects", "project-1", "chat.jsonl")
	antigravityFile := filepath.Join(geminiHomeDir, "antigravity", "conversations", "chat.pb")
	writeFixtureFile(t, copilotFile, "copilot")
	writeFixtureFile(t, vscodeFile, "{}")
	writeFixtureFile(t, claudeFile, "{}")
	writeFixtureFile(t, antigravityFile, "binary")

	sources, err := DiscoverChats("unused-project-path")
	if err != nil {
		t.Fatalf("DiscoverChats returned error: %v", err)
	}
	if len(sources) != 4 {
		t.Fatalf("expected 4 sources, got %d", len(sources))
	}
}

func TestDiscoverChatsUsesClaudeConfigDirWhenSet(t *testing.T) {
	homeDir := t.TempDir()
	appDataDir := t.TempDir()
	claudeConfigDir := t.TempDir()
	geminiHomeDir := t.TempDir()
	setTestHome(t, homeDir)
	t.Setenv("APPDATA", appDataDir)
	t.Setenv("CLAUDE_CONFIG_DIR", claudeConfigDir)
	t.Setenv("GEMINI_HOME", geminiHomeDir)

	claudeFile := filepath.Join(claudeConfigDir, "projects", "project-override", "session.jsonl")
	antigravityFile := filepath.Join(geminiHomeDir, "antigravity", "conversations", "session.pb")
	writeFixtureFile(t, claudeFile, "{}")
	writeFixtureFile(t, antigravityFile, "binary")

	sources, err := DiscoverChats("unused-project-path")
	if err != nil {
		t.Fatalf("DiscoverChats returned error: %v", err)
	}

	byPath := make(map[string]ChatSource, len(sources))
	for _, source := range sources {
		byPath[source.Path] = source
	}

	source, ok := byPath[claudeFile]
	if !ok {
		t.Fatalf("expected Claude source %q to be discovered", claudeFile)
	}
	if source.Tool != SourceTypeClaudeCodeSession {
		t.Fatalf("tool for %q = %q, want %q", claudeFile, source.Tool, SourceTypeClaudeCodeSession)
	}

	antigravitySource, ok := byPath[antigravityFile]
	if !ok {
		t.Fatalf("expected Antigravity source %q to be discovered", antigravityFile)
	}
	if antigravitySource.Tool != SourceTypeAntigravityGemini {
		t.Fatalf("tool for %q = %q, want %q", antigravityFile, antigravitySource.Tool, SourceTypeAntigravityGemini)
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
