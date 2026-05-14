package chat

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
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
	codexFile := filepath.Join(homeDir, ".codex", "sessions", "2026", "05", "codex-session.jsonl")
	vscodeJSON := filepath.Join(appDataDir, "Code", "User", "workspaceStorage", "workspace-1", "chatSessions", "alpha", "chat.json")
	vscodeJSONL := filepath.Join(appDataDir, "Code", "User", "workspaceStorage", "workspace-1", "chatSessions", "beta", "chat.jsonl")
	vscodeWorkspaceJSON := filepath.Join(appDataDir, "Code", "User", "workspaceStorage", "workspace-1", "workspace.json")
	claudeJSONL := filepath.Join(claudeConfigDir, "projects", "project-a", "session.jsonl")
	antigravityGlobalPB := filepath.Join(geminiHomeDir, "antigravity", "conversations", "global-session.pb")
	antigravityProjectPB := filepath.Join(projectDir, ".gemini", "antigravity", "conversations", "project-session.pb")
	ignoredFile := filepath.Join(appDataDir, "Code", "User", "workspaceStorage", "workspace-1", "chatSessions", "ignore.txt")

	writeFixtureFile(t, copilotFile, "copilot")
	writeFixtureFile(t, codexFile, fmt.Sprintf(`{"session_meta":{"payload":{"cwd":%q}}}`, filepath.Join(projectDir, "src")))
	writeFixtureFile(t, vscodeJSON, "{}")
	writeFixtureFile(t, vscodeJSONL, "{}")
	writeFixtureFile(t, vscodeWorkspaceJSON, fmt.Sprintf(`{"folder":%q}`, filepath.Join(projectDir, "src")))
	writeFixtureFile(t, claudeJSONL, fmt.Sprintf(`{"cwd":%q}`, filepath.Join(projectDir, "src")))
	writeFixtureFile(t, antigravityGlobalPB, "binary")
	writeFixtureFile(t, antigravityProjectPB, "binary")
	writeFixtureFile(t, ignoredFile, "ignored")

	copilotTime := time.Now().UTC().Add(-3 * time.Hour).Truncate(time.Second)
	codexTime := time.Now().UTC().Add(-150 * time.Minute).Truncate(time.Second)
	vscodeJSONTime := time.Now().UTC().Add(-2 * time.Hour).Truncate(time.Second)
	vscodeJSONLTime := time.Now().UTC().Add(-1 * time.Hour).Truncate(time.Second)
	claudeJSONLTime := time.Now().UTC().Add(-30 * time.Minute).Truncate(time.Second)
	antigravityGlobalTime := time.Now().UTC().Add(-20 * time.Minute).Truncate(time.Second)
	antigravityProjectTime := time.Now().UTC().Truncate(time.Second)
	setModTime(t, copilotFile, copilotTime)
	setModTime(t, codexFile, codexTime)
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
	assertSource(t, byPath, codexFile, SourceTypeCodexSessionJSONL, codexTime)
	assertSource(t, byPath, vscodeJSON, SourceTypeVSCodeChatSession, vscodeJSONTime)
	assertSource(t, byPath, vscodeJSONL, SourceTypeVSCodeChatSession, vscodeJSONLTime)
	assertSource(t, byPath, claudeJSONL, SourceTypeClaudeCodeSession, claudeJSONLTime)
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
	projectDir := t.TempDir()
	setTestHome(t, homeDir)
	t.Setenv("APPDATA", appDataDir)
	t.Setenv("GEMINI_HOME", geminiHomeDir)

	copilotFile := filepath.Join(homeDir, ".copilot", "session-state", "chat.jsonl")
	codexFile := filepath.Join(homeDir, ".codex", "sessions", "2026", "05", "chat.jsonl")
	vscodeFile := filepath.Join(appDataDir, "Code", "User", "workspaceStorage", "workspace-2", "chatSessions", "chat.json")
	vscodeWorkspaceJSON := filepath.Join(appDataDir, "Code", "User", "workspaceStorage", "workspace-2", "workspace.json")
	claudeFile := filepath.Join(homeDir, ".claude", "projects", "project-1", "chat.jsonl")
	antigravityFile := filepath.Join(geminiHomeDir, "antigravity", "conversations", "chat.pb")
	writeFixtureFile(t, copilotFile, "copilot")
	writeFixtureFile(t, codexFile, fmt.Sprintf(`{"session_meta":{"payload":{"cwd":%q}}}`, filepath.Join(projectDir, "workspace")))
	writeFixtureFile(t, vscodeFile, "{}")
	writeFixtureFile(t, vscodeWorkspaceJSON, fmt.Sprintf(`{"folder":%q}`, filepath.Join(projectDir, "workspace")))
	writeFixtureFile(t, claudeFile, fmt.Sprintf(`{"cwd":%q}`, filepath.Join(projectDir, "workspace")))
	writeFixtureFile(t, antigravityFile, "binary")

	sources, err := DiscoverChats(projectDir)
	if err != nil {
		t.Fatalf("DiscoverChats returned error: %v", err)
	}
	if len(sources) != 4 {
		t.Fatalf("expected 4 sources, got %d", len(sources))
	}
}

func TestDiscoverCodexSessionsIncludesArchivedSessions(t *testing.T) {
	homeDir := t.TempDir()
	projectDir := t.TempDir()
	activeSession := filepath.Join(homeDir, ".codex", "sessions", "2026", "05", "active.jsonl")
	archivedSession := filepath.Join(homeDir, ".codex", "archived_sessions", "archived.jsonl")
	writeFixtureFile(t, activeSession, fmt.Sprintf(`{"session_meta":{"payload":{"cwd":%q}}}`, filepath.Join(projectDir, "active")))
	writeFixtureFile(t, archivedSession, fmt.Sprintf(`{"type":"session_meta","payload":{"cwd":%q}}`, filepath.Join(projectDir, "archived")))

	activeTime := time.Now().UTC().Add(-time.Minute).Truncate(time.Second)
	archivedTime := time.Now().UTC().Truncate(time.Second)
	setModTime(t, activeSession, activeTime)
	setModTime(t, archivedSession, archivedTime)

	sources, err := discoverCodexSessions(homeDir, projectDir)
	if err != nil {
		t.Fatalf("discoverCodexSessions returned error: %v", err)
	}
	if len(sources) != 2 {
		t.Fatalf("expected 2 Codex sources, got %d", len(sources))
	}

	byPath := make(map[string]ChatSource, len(sources))
	for _, source := range sources {
		byPath[source.Path] = source
	}
	assertSource(t, byPath, activeSession, SourceTypeCodexSessionJSONL, activeTime)
	assertSource(t, byPath, archivedSession, SourceTypeCodexSessionJSONL, archivedTime)
}

func TestDiscoverCodexSessionsExcludesOutsideProjectAndMissingMetadata(t *testing.T) {
	homeDir := t.TempDir()
	projectDir := t.TempDir()
	otherProjectDir := t.TempDir()

	inProject := filepath.Join(homeDir, ".codex", "sessions", "2026", "05", "in-project.jsonl")
	outOfProject := filepath.Join(homeDir, ".codex", "sessions", "2026", "05", "out-of-project.jsonl")
	missingMetadata := filepath.Join(homeDir, ".codex", "archived_sessions", "missing-metadata.jsonl")

	writeFixtureFile(t, inProject, fmt.Sprintf(`{"session_meta":{"payload":{"cwd":%q}}}`, filepath.Join(projectDir, "workspace")))
	writeFixtureFile(t, outOfProject, fmt.Sprintf(`{"session_meta":{"payload":{"cwd":%q}}}`, filepath.Join(otherProjectDir, "workspace")))
	writeFixtureFile(t, missingMetadata, `{"type":"assistant","message":"no session metadata"}`)

	sources, err := discoverCodexSessions(homeDir, projectDir)
	if err != nil {
		t.Fatalf("discoverCodexSessions returned error: %v", err)
	}
	if len(sources) != 1 {
		t.Fatalf("expected 1 Codex source, got %d", len(sources))
	}
	if got := sources[0].Path; got != inProject {
		t.Fatalf("source path = %q, want %q", got, inProject)
	}
}

func TestDiscoverCodexSessionsWindowsCaseContainment(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("windows-specific containment test")
	}

	homeDir := t.TempDir()
	projectDir := t.TempDir()
	codexFile := filepath.Join(homeDir, ".codex", "sessions", "2026", "05", "session.jsonl")
	cwdWithForwardSlashes := strings.ReplaceAll(filepath.Join(projectDir, "Nested", "Repo"), `\`, "/")
	writeFixtureFile(t, codexFile, fmt.Sprintf(`{"session_meta":{"payload":{"cwd":%q}}}`, cwdWithForwardSlashes))

	projectPathWithDifferentCase := strings.ToUpper(projectDir)
	if projectPathWithDifferentCase == projectDir {
		projectPathWithDifferentCase = strings.ToLower(projectDir)
	}

	sources, err := discoverCodexSessions(homeDir, projectPathWithDifferentCase)
	if err != nil {
		t.Fatalf("discoverCodexSessions returned error: %v", err)
	}
	if len(sources) != 1 {
		t.Fatalf("expected 1 Codex source, got %d", len(sources))
	}
}

func TestDiscoverChatsUsesClaudeConfigDirWhenSet(t *testing.T) {
	homeDir := t.TempDir()
	appDataDir := t.TempDir()
	claudeConfigDir := t.TempDir()
	geminiHomeDir := t.TempDir()
	projectDir := t.TempDir()
	setTestHome(t, homeDir)
	t.Setenv("APPDATA", appDataDir)
	t.Setenv("CLAUDE_CONFIG_DIR", claudeConfigDir)
	t.Setenv("GEMINI_HOME", geminiHomeDir)

	claudeFile := filepath.Join(claudeConfigDir, "projects", "project-override", "session.jsonl")
	antigravityFile := filepath.Join(geminiHomeDir, "antigravity", "conversations", "session.pb")
	writeFixtureFile(t, claudeFile, fmt.Sprintf(`{"cwd":%q}`, filepath.Join(projectDir, "repo")))
	writeFixtureFile(t, antigravityFile, "binary")

	sources, err := DiscoverChats(projectDir)
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

	if _, ok := byPath[antigravityFile]; ok {
		t.Fatalf("global Antigravity source without project evidence should not be discovered: %q", antigravityFile)
	}
}

func TestDiscoverClaudeCodeSessionsIncludesInProjectRoot(t *testing.T) {
	claudeConfigDir := t.TempDir()
	projectDir := t.TempDir()
	claudeFile := filepath.Join(claudeConfigDir, "projects", "project-a", "session.jsonl")
	writeFixtureFile(t, claudeFile, fmt.Sprintf(`{"cwd":%q}`, filepath.Join(projectDir, "nested", "repo")))

	sources, err := discoverClaudeCodeSessions(t.TempDir(), claudeConfigDir, projectDir)
	if err != nil {
		t.Fatalf("discoverClaudeCodeSessions returned error: %v", err)
	}
	if len(sources) != 1 {
		t.Fatalf("expected 1 Claude source, got %d", len(sources))
	}
	if sources[0].Path != claudeFile {
		t.Fatalf("source path = %q, want %q", sources[0].Path, claudeFile)
	}
}

func TestDiscoverClaudeCodeSessionsExcludesOutOfProjectRoot(t *testing.T) {
	claudeConfigDir := t.TempDir()
	projectDir := t.TempDir()
	otherProjectDir := t.TempDir()
	claudeFile := filepath.Join(claudeConfigDir, "projects", "project-a", "session.jsonl")
	writeFixtureFile(t, claudeFile, fmt.Sprintf(`{"cwd":%q}`, filepath.Join(otherProjectDir, "nested", "repo")))

	sources, err := discoverClaudeCodeSessions(t.TempDir(), claudeConfigDir, projectDir)
	if err != nil {
		t.Fatalf("discoverClaudeCodeSessions returned error: %v", err)
	}
	if len(sources) != 0 {
		t.Fatalf("expected no Claude sources, got %d", len(sources))
	}
}

func TestDiscoverClaudeCodeSessionsExcludesMissingCWDEvidence(t *testing.T) {
	claudeConfigDir := t.TempDir()
	projectDir := t.TempDir()
	claudeFile := filepath.Join(claudeConfigDir, "projects", "project-a", "session.jsonl")
	writeFixtureFile(t, claudeFile, `{"type":"assistant","message":"no cwd metadata"}`)

	sources, err := discoverClaudeCodeSessions(t.TempDir(), claudeConfigDir, projectDir)
	if err != nil {
		t.Fatalf("discoverClaudeCodeSessions returned error: %v", err)
	}
	if len(sources) != 0 {
		t.Fatalf("expected no Claude sources, got %d", len(sources))
	}
}

func TestDiscoverClaudeCodeSessionsWindowsCaseContainment(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("windows-specific containment test")
	}

	claudeConfigDir := t.TempDir()
	projectDir := t.TempDir()
	claudeFile := filepath.Join(claudeConfigDir, "projects", "project-a", "session.jsonl")
	cwdWithForwardSlashes := strings.ReplaceAll(filepath.Join(projectDir, "Nested", "Repo"), `\`, "/")
	writeFixtureFile(t, claudeFile, fmt.Sprintf(`{"cwd":%q}`, cwdWithForwardSlashes))

	projectPathWithDifferentCase := strings.ToUpper(projectDir)
	if projectPathWithDifferentCase == projectDir {
		projectPathWithDifferentCase = strings.ToLower(projectDir)
	}

	sources, err := discoverClaudeCodeSessions(t.TempDir(), claudeConfigDir, projectPathWithDifferentCase)
	if err != nil {
		t.Fatalf("discoverClaudeCodeSessions returned error: %v", err)
	}
	if len(sources) != 1 {
		t.Fatalf("expected 1 Claude source, got %d", len(sources))
	}
}

func TestDiscoverVSCodeChatSessionsFiltersByWorkspaceJSONProjectRoot(t *testing.T) {
	appDataDir := t.TempDir()
	projectDir := t.TempDir()
	chatFile := filepath.Join(appDataDir, "Code", "User", "workspaceStorage", "workspace-a", "chatSessions", "chat.json")
	workspaceJSON := filepath.Join(appDataDir, "Code", "User", "workspaceStorage", "workspace-a", "workspace.json")
	writeFixtureFile(t, chatFile, "{}")
	writeFixtureFile(t, workspaceJSON, fmt.Sprintf(`{"folder":%q}`, filepath.Join(projectDir, "nested")))

	sources, err := discoverVSCodeChatSessions(appDataDir, projectDir)
	if err != nil {
		t.Fatalf("discoverVSCodeChatSessions returned error: %v", err)
	}
	if len(sources) != 1 {
		t.Fatalf("expected 1 VS Code source, got %d", len(sources))
	}
	if got := sources[0].Path; got != chatFile {
		t.Fatalf("source path = %q, want %q", got, chatFile)
	}
}

func TestDiscoverVSCodeChatSessionsExcludesOtherWorkspaceRoot(t *testing.T) {
	appDataDir := t.TempDir()
	projectDir := t.TempDir()
	otherProjectDir := t.TempDir()
	chatFile := filepath.Join(appDataDir, "Code", "User", "workspaceStorage", "workspace-b", "chatSessions", "chat.jsonl")
	workspaceJSON := filepath.Join(appDataDir, "Code", "User", "workspaceStorage", "workspace-b", "workspace.json")
	writeFixtureFile(t, chatFile, "{}")
	writeFixtureFile(t, workspaceJSON, fmt.Sprintf(`{"folder":%q}`, filepath.Join(otherProjectDir, "nested")))

	sources, err := discoverVSCodeChatSessions(appDataDir, projectDir)
	if err != nil {
		t.Fatalf("discoverVSCodeChatSessions returned error: %v", err)
	}
	if len(sources) != 0 {
		t.Fatalf("expected no VS Code sources, got %d", len(sources))
	}
}

func TestDiscoverVSCodeChatSessionsExcludesMissingWorkspaceEvidence(t *testing.T) {
	appDataDir := t.TempDir()
	projectDir := t.TempDir()
	chatFile := filepath.Join(appDataDir, "Code", "User", "workspaceStorage", "workspace-c", "chatSessions", "chat.json")
	writeFixtureFile(t, chatFile, "{}")

	sources, err := discoverVSCodeChatSessions(appDataDir, projectDir)
	if err != nil {
		t.Fatalf("discoverVSCodeChatSessions returned error: %v", err)
	}
	if len(sources) != 0 {
		t.Fatalf("expected no VS Code sources, got %d", len(sources))
	}
}

func TestDiscoverChatsFromRootsIsolatesClaudeSessionsPerProject(t *testing.T) {
	homeDir := t.TempDir()
	appDataDir := t.TempDir()
	claudeConfigDir := t.TempDir()
	geminiHomeDir := t.TempDir()
	projectADir := filepath.Join(t.TempDir(), "project-a")
	projectBDir := filepath.Join(t.TempDir(), "project-b")

	claudeA := filepath.Join(claudeConfigDir, "projects", "alpha", "session.jsonl")
	claudeB := filepath.Join(claudeConfigDir, "projects", "beta", "session.jsonl")
	writeFixtureFile(t, claudeA, fmt.Sprintf(`{"cwd":%q}`, filepath.Join(projectADir, "workspace")))
	writeFixtureFile(t, claudeB, fmt.Sprintf(`{"cwd":%q}`, filepath.Join(projectBDir, "workspace")))

	overlapping := time.Date(2026, 5, 8, 12, 0, 0, 0, time.UTC)
	setModTime(t, claudeA, overlapping)
	setModTime(t, claudeB, overlapping)

	sourcesA, err := discoverChatsFromRoots(homeDir, appDataDir, claudeConfigDir, projectADir, geminiHomeDir)
	if err != nil {
		t.Fatalf("discoverChatsFromRoots for project A returned error: %v", err)
	}
	if len(sourcesA) != 1 {
		t.Fatalf("expected 1 source for project A, got %d", len(sourcesA))
	}
	if got := sourcesA[0].Path; got != claudeA {
		t.Fatalf("project A source path = %q, want %q", got, claudeA)
	}

	sourcesB, err := discoverChatsFromRoots(homeDir, appDataDir, claudeConfigDir, projectBDir, geminiHomeDir)
	if err != nil {
		t.Fatalf("discoverChatsFromRoots for project B returned error: %v", err)
	}
	if len(sourcesB) != 1 {
		t.Fatalf("expected 1 source for project B, got %d", len(sourcesB))
	}
	if got := sourcesB[0].Path; got != claudeB {
		t.Fatalf("project B source path = %q, want %q", got, claudeB)
	}
}

func TestDiscoverAntigravityGeminiSessionsFiltersGlobalByProjectEvidence(t *testing.T) {
	geminiHomeDir := t.TempDir()
	projectDir := t.TempDir()
	otherProjectDir := t.TempDir()

	inProject := filepath.Join(geminiHomeDir, "antigravity", "conversations", "in-project.pbtxt")
	outOfProject := filepath.Join(geminiHomeDir, "antigravity", "conversations", "out-of-project.pbtxt")
	writeFixtureFile(t, inProject, fmt.Sprintf("workspacePath: %q\nrole: \"user\"\ntext: \"Project A request\"", filepath.Join(projectDir, "workspace")))
	writeFixtureFile(t, outOfProject, fmt.Sprintf("workspacePath: %q\nrole: \"user\"\ntext: \"Project B request\"", filepath.Join(otherProjectDir, "workspace")))

	sources, err := discoverAntigravityGeminiSessions(t.TempDir(), projectDir, geminiHomeDir)
	if err != nil {
		t.Fatalf("discoverAntigravityGeminiSessions returned error: %v", err)
	}
	if len(sources) != 1 {
		t.Fatalf("expected 1 Antigravity source, got %d", len(sources))
	}
	if got := sources[0].Path; got != inProject {
		t.Fatalf("source path = %q, want %q", got, inProject)
	}
}

func TestDiscoverAntigravityGeminiSessionsExcludesMissingProjectEvidence(t *testing.T) {
	geminiHomeDir := t.TempDir()
	projectDir := t.TempDir()
	chatFile := filepath.Join(geminiHomeDir, "antigravity", "conversations", "missing-evidence.pbtxt")
	writeFixtureFile(t, chatFile, "role: \"user\"\ntext: \"No workspace evidence\"")

	sources, err := discoverAntigravityGeminiSessions(t.TempDir(), projectDir, geminiHomeDir)
	if err != nil {
		t.Fatalf("discoverAntigravityGeminiSessions returned error: %v", err)
	}
	if len(sources) != 0 {
		t.Fatalf("expected no Antigravity sources, got %d", len(sources))
	}
}

func TestDiscoverAntigravityGeminiExtensionsMatchRuntimeSupport(t *testing.T) {
	projectDir := t.TempDir()
	antigravityRoot := filepath.Join(projectDir, ".gemini", "antigravity", "conversations")
	supportedPB := filepath.Join(antigravityRoot, "chat.pb")
	supportedPBTXT := filepath.Join(antigravityRoot, "chat.pbtxt")
	supportedJSONL := filepath.Join(antigravityRoot, "chat.jsonl")
	unsupportedJSON := filepath.Join(antigravityRoot, "chat.json")

	writeFixtureFile(t, supportedPB, "user: hello from pb")
	writeFixtureFile(t, supportedPBTXT, "role: \"user\"\ntext: \"hello from pbtxt\"")
	writeFixtureFile(t, supportedJSONL, `{"role":"user","content":"hello from jsonl"}`)
	writeFixtureFile(t, unsupportedJSON, `{"role":"user","content":"hello from json"}`)

	sources, err := discoverAntigravityGeminiSessions(t.TempDir(), projectDir, t.TempDir())
	if err != nil {
		t.Fatalf("discoverAntigravityGeminiSessions returned error: %v", err)
	}

	extensions := make(map[string]struct{}, len(sources))
	for _, source := range sources {
		extensions[strings.ToLower(filepath.Ext(source.Path))] = struct{}{}
	}
	for _, extension := range []string{".pb", ".pbtxt", ".jsonl"} {
		if _, ok := extensions[extension]; !ok {
			t.Fatalf("expected discovered Antigravity extension %q, got %v", extension, extensions)
		}
	}
	if _, ok := extensions[".json"]; ok {
		t.Fatalf("Antigravity .json should not be discovered until it has a supported reader")
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
	t.Setenv("XDG_DATA_HOME", "")
	t.Setenv("OPENCODE_DB", "")
	t.Setenv("KIRO_CLI_DB", "")
}
