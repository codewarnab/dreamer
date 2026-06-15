package chat

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"dreamer/internal/chat/readers"
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

	sources, err := discoverChatsFromEnvironment(DiscoveryEnvironment{
		HomeDir:         homeDir,
		AppDataDir:      appDataDir,
		ClaudeConfigDir: claudeConfigDir,
		GeminiHomeDir:   geminiHomeDir,
	}, projectDir)
	if err != nil {
		t.Fatalf("discoverChatsFromEnvironment returned error: %v", err)
	}
	if len(sources) != 6 {
		t.Fatalf("expected 6 sources, got %d", len(sources))
	}

	byPath := make(map[string]Source, len(sources))
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
	sources, err := discoverChatsFromEnvironment(DiscoveryEnvironment{
		HomeDir:         filepath.Join(t.TempDir(), "missing-home"),
		AppDataDir:      filepath.Join(t.TempDir(), "missing-appdata"),
		ClaudeConfigDir: filepath.Join(t.TempDir(), "missing-claude"),
		GeminiHomeDir:   filepath.Join(t.TempDir(), "missing-gemini"),
	}, filepath.Join(t.TempDir(), "missing-project"))
	if err != nil {
		t.Fatalf("discoverChatsFromEnvironment returned error: %v", err)
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

func TestDiscoverChatsSkipsFailedProviderWhenOthersSucceed(t *testing.T) {
	projectDir := t.TempDir()
	foundPath := filepath.Join(projectDir, "chat.jsonl")
	modifiedTime := time.Now().UTC().Truncate(time.Second)
	withRegisteredProviders(t,
		fakeDiscoveryProvider{
			sourceType: SourceTypeCopilotSessionJSONL,
			sources: []Source{{
				Path:         foundPath,
				Tool:         SourceTypeCopilotSessionJSONL,
				ModifiedTime: modifiedTime,
			}},
		},
		fakeDiscoveryProvider{
			sourceType: SourceTypeClaudeCodeSession,
			err:        errors.New("permission denied"),
		},
	)

	sources, err := discoverChatsFromEnvironment(DiscoveryEnvironment{}, projectDir)
	if err != nil {
		t.Fatalf("discoverChatsFromEnvironment returned error: %v", err)
	}
	if len(sources) != 1 {
		t.Fatalf("expected one source from successful provider, got %d", len(sources))
	}
	assertSource(t, map[string]Source{foundPath: sources[0]}, foundPath, SourceTypeCopilotSessionJSONL, modifiedTime)
}

func TestDiscoverChatsFailsWhenEveryProviderFails(t *testing.T) {
	withRegisteredProviders(t,
		fakeDiscoveryProvider{
			sourceType: SourceTypeCopilotSessionJSONL,
			err:        errors.New("copilot unreadable"),
		},
		fakeDiscoveryProvider{
			sourceType: SourceTypeClaudeCodeSession,
			err:        errors.New("claude unreadable"),
		},
	)

	sources, err := discoverChatsFromEnvironment(DiscoveryEnvironment{}, t.TempDir())
	if err == nil {
		t.Fatal("expected error when every provider fails")
	}
	if len(sources) != 0 {
		t.Fatalf("expected no sources on full discovery failure, got %d", len(sources))
	}
	if !strings.Contains(err.Error(), "copilot") || !strings.Contains(err.Error(), "claude") {
		t.Fatalf("error = %q, want both provider errors", err.Error())
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

	byPath := make(map[string]Source, len(sources))
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

	byPath := make(map[string]Source, len(sources))
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

	envBase := DiscoveryEnvironment{
		HomeDir:         homeDir,
		AppDataDir:      appDataDir,
		ClaudeConfigDir: claudeConfigDir,
		GeminiHomeDir:   geminiHomeDir,
	}

	sourcesA, err := discoverChatsFromEnvironment(envBase, projectADir)
	if err != nil {
		t.Fatalf("discoverChatsFromEnvironment for project A returned error: %v", err)
	}
	if len(sourcesA) != 1 {
		t.Fatalf("expected 1 source for project A, got %d", len(sourcesA))
	}
	if got := sourcesA[0].Path; got != claudeA {
		t.Fatalf("project A source path = %q, want %q", got, claudeA)
	}

	sourcesB, err := discoverChatsFromEnvironment(envBase, projectBDir)
	if err != nil {
		t.Fatalf("discoverChatsFromEnvironment for project B returned error: %v", err)
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

func TestDiscoverClaudeCodeSessionsSetsParentIDForSubagent(t *testing.T) {
	claudeConfigDir := t.TempDir()
	projectDir := t.TempDir()
	sessionID := "sess-abc123"
	agentID := "agent-001"
	subagentFile := filepath.Join(claudeConfigDir, "projects", "project-a", sessionID, "subagents", agentID+".jsonl")
	writeFixtureFile(t, subagentFile, fmt.Sprintf(`{"cwd":%q}`, filepath.Join(projectDir, "repo")))

	sources, err := discoverClaudeCodeSessions(t.TempDir(), claudeConfigDir, projectDir)
	if err != nil {
		t.Fatalf("discoverClaudeCodeSessions returned error: %v", err)
	}
	if len(sources) != 1 {
		t.Fatalf("expected 1 Claude source, got %d", len(sources))
	}
	if sources[0].ParentID != sessionID {
		t.Errorf("ParentID = %q, want %q", sources[0].ParentID, sessionID)
	}
}

func TestDiscoverClaudeCodeSessionsTopLevelHasNoParentID(t *testing.T) {
	claudeConfigDir := t.TempDir()
	projectDir := t.TempDir()
	claudeFile := filepath.Join(claudeConfigDir, "projects", "project-a", "session.jsonl")
	writeFixtureFile(t, claudeFile, fmt.Sprintf(`{"cwd":%q}`, filepath.Join(projectDir, "repo")))

	sources, err := discoverClaudeCodeSessions(t.TempDir(), claudeConfigDir, projectDir)
	if err != nil {
		t.Fatalf("discoverClaudeCodeSessions returned error: %v", err)
	}
	if len(sources) != 1 {
		t.Fatalf("expected 1 Claude source, got %d", len(sources))
	}
	if sources[0].ParentID != "" {
		t.Errorf("ParentID = %q, want empty", sources[0].ParentID)
	}
}

func TestDiscoverGeminiCLISessionsTopLevelHasNoParentID(t *testing.T) {
	homeDir := t.TempDir()
	projectDir := t.TempDir()

	sessionFile := filepath.Join(homeDir, ".gemini", "tmp", "proj-slug", "chats", "session-1.jsonl")
	writeFixtureFile(t, sessionFile, fmt.Sprintf(`{"sessionId":"a","directories":[%q]}`, projectDir))

	sources, err := discoverGeminiCLISessions(homeDir, "", projectDir)
	if err != nil {
		t.Fatalf("discoverGeminiCLISessions returned error: %v", err)
	}
	if len(sources) != 1 {
		t.Fatalf("expected 1 source, got %d", len(sources))
	}
	if sources[0].ParentID != "" {
		t.Errorf("ParentID = %q, want empty for top-level session", sources[0].ParentID)
	}
}

func TestDiscoverGeminiCLISessionsNestedSetsParentID(t *testing.T) {
	homeDir := t.TempDir()
	projectDir := t.TempDir()

	nestedFile := filepath.Join(homeDir, ".gemini", "tmp", "proj-slug", "chats", "parent-session-id", "agent-001.jsonl")
	writeFixtureFile(t, nestedFile, fmt.Sprintf(`{"sessionId":"b","directories":[%q]}`, projectDir))

	sources, err := discoverGeminiCLISessions(homeDir, "", projectDir)
	if err != nil {
		t.Fatalf("discoverGeminiCLISessions returned error: %v", err)
	}
	if len(sources) != 1 {
		t.Fatalf("expected 1 source, got %d", len(sources))
	}
	if sources[0].ParentID != "parent-session-id" {
		t.Errorf("ParentID = %q, want %q", sources[0].ParentID, "parent-session-id")
	}
}

func TestExtractClaudeParentID(t *testing.T) {
	tests := []struct {
		name string
		path string
		want string
	}{
		{"subagent", filepath.Join("root", "sess123", "subagents", "agent-001.jsonl"), "sess123"},
		{"top-level", filepath.Join("root", "project", "session.jsonl"), ""},
		{"deep nesting", filepath.Join("a", "b", "parent-id", "subagents", "child.jsonl"), "parent-id"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractClaudeParentID(tt.path)
			if got != tt.want {
				t.Errorf("extractClaudeParentID(%q) = %q, want %q", tt.path, got, tt.want)
			}
		})
	}
}

func assertSource(t *testing.T, byPath map[string]Source, path string, expectedTool SourceType, expectedModTime time.Time) {
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
	t.Setenv("XDG_CONFIG_HOME", "")
	// Clear all chat-discovery env vars so tests don't pick up the developer's
	// real chat sources. cmd/commands_test.go:setTestHome mirrors this list.
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Setenv("GEMINI_HOME", "")
	t.Setenv("OPENCODE_DB", "")
	t.Setenv("KIRO_CLI_DB", "")
	t.Setenv("CODEBUFF_CONFIG_DIR", "")
	t.Setenv("XDG_DATA_HOME", "")
}

// ---------------------------------------------------------------------------
// PrependMarker
// ---------------------------------------------------------------------------

func TestPrependMarkerWithoutRunID(t *testing.T) {
	got := PrependMarker("hello world", "")
	want := "<!-- dreamer-analysis-marker --> hello world"
	if got != want {
		t.Errorf("PrependMarker(%q, %q) = %q, want %q", "hello world", "", got, want)
	}
}

func TestPrependMarkerWithRunID(t *testing.T) {
	got := PrependMarker("analyze this", "abc123")
	want := "<!-- dreamer-analysis-marker run=abc123 --> analyze this"
	if got != want {
		t.Errorf("PrependMarker(%q, %q) = %q, want %q", "analyze this", "abc123", got, want)
	}
}

// ---------------------------------------------------------------------------
// normalizeDiscoveryKey
// ---------------------------------------------------------------------------

func TestNormalizeDiscoveryKey(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{"plain lowercase", "cwd", "cwd"},
		{"mixed case", "CWD", "cwd"},
		{"with underscores", "working_directory", "workingdirectory"},
		{"with hyphens", "working-directory", "workingdirectory"},
		{"with spaces and underscores", " Working_Directory ", "workingdirectory"},
		{"empty", "", ""},
		{"only whitespace", "   ", ""},
		{"CamelCase with underscores", "Current_Working_Directory", "currentworkingdirectory"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := normalizeDiscoveryKey(tt.raw)
			if got != tt.want {
				t.Errorf("normalizeDiscoveryKey(%q) = %q, want %q", tt.raw, got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// valueForNormalizedKey
// ---------------------------------------------------------------------------

func TestValueForNormalizedKey(t *testing.T) {
	record := map[string]any{
		"Working_Directory": "/home/user/project",
		"CWD":               "/tmp",
		"count":             42,
	}

	t.Run("case and underscore insensitive match", func(t *testing.T) {
		val, ok := valueForNormalizedKey(record, "workingdirectory")
		if !ok {
			t.Fatal("expected match for workingdirectory")
		}
		if val != "/home/user/project" {
			t.Errorf("got %v, want /home/user/project", val)
		}
	})

	t.Run("plain key", func(t *testing.T) {
		val, ok := valueForNormalizedKey(record, "cwd")
		if !ok {
			t.Fatal("expected match for cwd")
		}
		if val != "/tmp" {
			t.Errorf("got %v, want /tmp", val)
		}
	})

	t.Run("non-existent key", func(t *testing.T) {
		_, ok := valueForNormalizedKey(record, "notfound")
		if ok {
			t.Error("expected no match for notfound")
		}
	})

	t.Run("non-string value found", func(t *testing.T) {
		val, ok := valueForNormalizedKey(record, "count")
		if !ok {
			t.Fatal("expected match for count")
		}
		if val != 42 {
			t.Errorf("got %v, want 42", val)
		}
	})
}

// ---------------------------------------------------------------------------
// stringValueForNormalizedKey
// ---------------------------------------------------------------------------

func TestStringValueForNormalizedKey(t *testing.T) {
	record := map[string]any{
		"Working_Directory": "/home/user/project",
		"count":             42,
		"empty_field":       "   ",
	}

	t.Run("string value", func(t *testing.T) {
		val, ok := stringValueForNormalizedKey(record, "workingdirectory")
		if !ok {
			t.Fatal("expected match")
		}
		if val != "/home/user/project" {
			t.Errorf("got %q, want /home/user/project", val)
		}
	})

	t.Run("non-string value returns false", func(t *testing.T) {
		_, ok := stringValueForNormalizedKey(record, "count")
		if ok {
			t.Error("expected false for non-string value")
		}
	})

	t.Run("whitespace-only string returns false", func(t *testing.T) {
		_, ok := stringValueForNormalizedKey(record, "emptyfield")
		if ok {
			t.Error("expected false for whitespace-only value")
		}
	})

	t.Run("missing key returns false", func(t *testing.T) {
		_, ok := stringValueForNormalizedKey(record, "notfound")
		if ok {
			t.Error("expected false for missing key")
		}
	})
}

// ---------------------------------------------------------------------------
// extractPathValue
// ---------------------------------------------------------------------------

func TestExtractPathValue(t *testing.T) {
	tests := []struct {
		name  string
		value any
		depth int
		want  string
	}{
		{"nil value", nil, 0, ""},
		{"empty string", "", 0, ""},
		{"whitespace-only string", "   ", 0, ""},
		{"plain string", "/home/user/project", 0, "/home/user/project"},
		{"string with leading/trailing spaces", "  /tmp  ", 0, "/tmp"},
		{"[]byte value", []byte("/bytes/path"), 0, "/bytes/path"},
		{"[]byte with spaces", []byte("  /bytes  "), 0, "/bytes"},
		{"map with path key", map[string]any{"path": "/map/path"}, 0, "/map/path"},
		{"map with cwd key", map[string]any{"cwd": "/map/cwd"}, 0, "/map/cwd"},
		{"map with value key", map[string]any{"value": "/map/value"}, 0, "/map/value"},
		{"map with root key", map[string]any{"root": "/map/root"}, 0, "/map/root"},
		{"map with workingDirectory key", map[string]any{"workingDirectory": "/map/wd"}, 0, "/map/wd"},
		{"map with projectPath key", map[string]any{"projectPath": "/map/pp"}, 0, "/map/pp"},
		{"map with workspacePath key", map[string]any{"workspacePath": "/map/wp"}, 0, "/map/wp"},
		{"map with currentWorkingDirectory key", map[string]any{"currentWorkingDirectory": "/map/cwd"}, 0, "/map/cwd"},
		{"map with nested path", map[string]any{"path": map[string]any{"path": "/nested"}}, 0, "/nested"},
		{"map with no matching keys", map[string]any{"foo": "bar"}, 0, ""},
		{"array with string element", []any{"/arr/first", "/arr/second"}, 0, "/arr/first"},
		{"array with nil element", []any{nil, "/arr/second"}, 0, "/arr/second"},
		{"empty array", []any{}, 0, ""},
		{"depth exceeded", "/path", 7, ""},
		{"integer value (unsupported type)", 42, 0, ""},
		{"bool value (unsupported type)", true, 0, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractPathValue(tt.value, tt.depth)
			if got != tt.want {
				t.Errorf("extractPathValue(%v, %d) = %q, want %q", tt.value, tt.depth, got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// recursiveExtract
// ---------------------------------------------------------------------------

func TestRecursiveExtract(t *testing.T) {
	evidenceKeys := map[string]struct{}{
		"cwd":              {},
		"workingdirectory": {},
		"workspacepath":    {},
	}

	t.Run("flat map with matching key", func(t *testing.T) {
		value := map[string]any{"cwd": "/project/root"}
		got := recursiveExtract(value, evidenceKeys, 10)
		if got != "/project/root" {
			t.Errorf("got %q, want /project/root", got)
		}
	})

	t.Run("nested map finds evidence key", func(t *testing.T) {
		value := map[string]any{
			"session_meta": map[string]any{
				"payload": map[string]any{
					"cwd": "/nested/path",
				},
			},
		}
		got := recursiveExtract(value, evidenceKeys, 10)
		if got != "/nested/path" {
			t.Errorf("got %q, want /nested/path", got)
		}
	})

	t.Run("array containing matching map", func(t *testing.T) {
		value := []any{
			map[string]any{"cwd": "/array/path"},
		}
		got := recursiveExtract(value, evidenceKeys, 10)
		if got != "/array/path" {
			t.Errorf("got %q, want /array/path", got)
		}
	})

	t.Run("nil returns empty", func(t *testing.T) {
		got := recursiveExtract(nil, evidenceKeys, 10)
		if got != "" {
			t.Errorf("got %q, want empty", got)
		}
	})

	t.Run("no matching keys returns empty", func(t *testing.T) {
		value := map[string]any{"foo": "bar", "baz": 42}
		got := recursiveExtract(value, evidenceKeys, 10)
		if got != "" {
			t.Errorf("got %q, want empty", got)
		}
	})

	t.Run("max depth exceeded returns empty", func(t *testing.T) {
		value := map[string]any{
			"level1": map[string]any{
				"cwd": "/deep/path",
			},
		}
		got := recursiveExtract(value, evidenceKeys, 0)
		if got != "" {
			t.Errorf("got %q, want empty (depth exceeded)", got)
		}
	})

	t.Run("key normalization matches underscores and hyphens", func(t *testing.T) {
		value := map[string]any{
			"working_directory": "/normalized/path",
		}
		got := recursiveExtract(value, evidenceKeys, 10)
		if got != "/normalized/path" {
			t.Errorf("got %q, want /normalized/path", got)
		}
	})
}

// ---------------------------------------------------------------------------
// containsDreamerMarker
// ---------------------------------------------------------------------------

func TestContainsDreamerMarker(t *testing.T) {
	t.Run("file with marker in first lines", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "marked.jsonl")
		content := `{"role":"user","content":"<!-- dreamer-analysis-marker --> analyze this"}
{"role":"assistant","content":"result"}`
		writeFixtureFile(t, path, content)

		if !containsDreamerMarker(path) {
			t.Error("expected true for file containing DreamerMarker")
		}
	})

	t.Run("file without marker", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "clean.jsonl")
		content := `{"role":"user","content":"hello"}
{"role":"assistant","content":"hi"}`
		writeFixtureFile(t, path, content)

		if containsDreamerMarker(path) {
			t.Error("expected false for file not containing DreamerMarker")
		}
	})

	t.Run("non-existent file returns false", func(t *testing.T) {
		if containsDreamerMarker(filepath.Join(t.TempDir(), "missing.jsonl")) {
			t.Error("expected false for missing file")
		}
	})

	t.Run("empty file returns false", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "empty.jsonl")
		writeFixtureFile(t, path, "")
		if containsDreamerMarker(path) {
			t.Error("expected false for empty file")
		}
	})

	t.Run("marker on line 10 is found", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "late-marker.jsonl")
		lines := ""
		for i := 0; i < 9; i++ {
			lines += `{"line":` + string(rune('0'+i)) + "}\n"
		}
		lines += `{"content":"<!-- dreamer-analysis-marker -->"}`
		writeFixtureFile(t, path, lines)
		if !containsDreamerMarker(path) {
			t.Error("expected true when marker is on line 10")
		}
	})
}

// ---------------------------------------------------------------------------
// isJSONLExtension
// ---------------------------------------------------------------------------

func TestIsJSONLExtension(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		{"session.jsonl", true},
		{"session.JSONL", true},
		{"session.json", false},
		{"session.txt", false},
		{"session", false},
		{"/full/path/to/session.jsonl", true},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			got := isJSONLExtension(tt.path)
			if got != tt.want {
				t.Errorf("isJSONLExtension(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// splitDiscoveryField
// ---------------------------------------------------------------------------

func TestSplitDiscoveryField(t *testing.T) {
	tests := []struct {
		name    string
		line    string
		wantKey string
		wantVal string
		wantOk  bool
	}{
		{"simple kv", "workspacePath: /home/user/project", "workspacePath", "/home/user/project", true},
		{"quoted value", `role: "user"`, "role", "user", true},
		{"no colon", "nocolon", "", "", false},
		{"empty key", ": value", "", "", false},
		{"empty value", "key: ", "", "", false},
		{"colon at start", ":value", "", "", false},
		{"spaces around", "  key  :  value  ", "key", "value", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			key, val, ok := splitDiscoveryField(tt.line)
			if ok != tt.wantOk {
				t.Fatalf("splitDiscoveryField(%q) ok = %v, want %v", tt.line, ok, tt.wantOk)
			}
			if ok {
				if key != tt.wantKey || val != tt.wantVal {
					t.Errorf("splitDiscoveryField(%q) = (%q, %q), want (%q, %q)", tt.line, key, val, tt.wantKey, tt.wantVal)
				}
			}
		})
	}
}

// ---------------------------------------------------------------------------
// probeJSONLForCWD
// ---------------------------------------------------------------------------

func TestProbeJSONLForCWD(t *testing.T) {
	t.Run("extracts from first matching line", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "probe.jsonl")
		content := `{"other":"data"}
{"cwd":"/found/path"}
{"cwd":"/ignored/path"}`
		writeFixtureFile(t, path, content)

		extract := func(record map[string]any) string {
			if v, ok := record["cwd"].(string); ok {
				return v
			}
			return ""
		}
		got, ok := probeJSONLForCWD(path, 200, extract)
		if !ok {
			t.Fatal("expected ok=true")
		}
		if got != "/found/path" {
			t.Errorf("got %q, want /found/path", got)
		}
	})

	t.Run("respects maxLines", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "probe-limit.jsonl")
		content := `{"other":"data"}
{"other":"data"}
{"cwd":"/beyond/limit"}`
		writeFixtureFile(t, path, content)

		extract := func(record map[string]any) string {
			if v, ok := record["cwd"].(string); ok {
				return v
			}
			return ""
		}
		_, ok := probeJSONLForCWD(path, 2, extract)
		if ok {
			t.Error("expected ok=false when cwd is beyond maxLines")
		}
	})

	t.Run("non-existent file returns false", func(t *testing.T) {
		extract := func(record map[string]any) string { return "" }
		_, ok := probeJSONLForCWD(filepath.Join(t.TempDir(), "missing.jsonl"), 100, extract)
		if ok {
			t.Error("expected ok=false for missing file")
		}
	})

	t.Run("invalid JSON lines are skipped", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "probe-bad.jsonl")
		content := `not json
also not json
{"cwd":"/valid/path"}`
		writeFixtureFile(t, path, content)

		extract := func(record map[string]any) string {
			if v, ok := record["cwd"].(string); ok {
				return v
			}
			return ""
		}
		got, ok := probeJSONLForCWD(path, 100, extract)
		if !ok {
			t.Fatal("expected ok=true")
		}
		if got != "/valid/path" {
			t.Errorf("got %q, want /valid/path", got)
		}
	})

	t.Run("extractor returns empty for all lines", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "probe-no-match.jsonl")
		writeFixtureFile(t, path, `{"key":"val"}`)

		extract := func(record map[string]any) string { return "" }
		_, ok := probeJSONLForCWD(path, 100, extract)
		if ok {
			t.Error("expected ok=false when extractor never returns a value")
		}
	})
}

// ---------------------------------------------------------------------------
// walkChatFiles
// ---------------------------------------------------------------------------

func TestWalkChatFiles(t *testing.T) {
	t.Run("empty root returns nil", func(t *testing.T) {
		sources, err := walkChatFiles("", SourceTypeCopilotSessionJSONL, map[string]struct{}{".jsonl": {}}, nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if sources != nil {
			t.Errorf("expected nil, got %v", sources)
		}
	})

	t.Run("non-existent root returns nil", func(t *testing.T) {
		sources, err := walkChatFiles(filepath.Join(t.TempDir(), "nope"), SourceTypeCopilotSessionJSONL, map[string]struct{}{".jsonl": {}}, nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if sources != nil {
			t.Errorf("expected nil, got %v", sources)
		}
	})

	t.Run("finds matching extensions", func(t *testing.T) {
		root := t.TempDir()
		writeFixtureFile(t, filepath.Join(root, "a.jsonl"), "content")
		writeFixtureFile(t, filepath.Join(root, "b.txt"), "content")
		writeFixtureFile(t, filepath.Join(root, "c.jsonl"), "content")

		sources, err := walkChatFiles(root, SourceTypeCopilotSessionJSONL, map[string]struct{}{".jsonl": {}}, nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(sources) != 2 {
			t.Fatalf("expected 2 sources, got %d", len(sources))
		}
	})

	t.Run("skip filter excludes files", func(t *testing.T) {
		root := t.TempDir()
		writeFixtureFile(t, filepath.Join(root, "keep.jsonl"), "content")
		writeFixtureFile(t, filepath.Join(root, "skip.jsonl"), "content")

		skip := func(path string) bool {
			return filepath.Base(path) == "skip.jsonl"
		}
		sources, err := walkChatFiles(root, SourceTypeCopilotSessionJSONL, map[string]struct{}{".jsonl": {}}, skip)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(sources) != 1 {
			t.Fatalf("expected 1 source, got %d", len(sources))
		}
		if filepath.Base(sources[0].Path) != "keep.jsonl" {
			t.Errorf("expected keep.jsonl, got %s", sources[0].Path)
		}
	})

	t.Run("whitespace-only root returns nil", func(t *testing.T) {
		sources, err := walkChatFiles("   ", SourceTypeCopilotSessionJSONL, map[string]struct{}{".jsonl": {}}, nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if sources != nil {
			t.Errorf("expected nil, got %v", sources)
		}
	})

	t.Run("root is a file, not a directory", func(t *testing.T) {
		file := filepath.Join(t.TempDir(), "file.txt")
		writeFixtureFile(t, file, "content")
		sources, err := walkChatFiles(file, SourceTypeCopilotSessionJSONL, map[string]struct{}{".jsonl": {}}, nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if sources != nil {
			t.Errorf("expected nil, got %v", sources)
		}
	})
}

// ---------------------------------------------------------------------------
// skipDreamerMarkedFiles
// ---------------------------------------------------------------------------

func TestSkipDreamerMarkedFiles(t *testing.T) {
	t.Run("skips jsonl with marker", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "marked.jsonl")
		writeFixtureFile(t, path, `{"content":"<!-- dreamer-analysis-marker -->"}`)
		if !skipDreamerMarkedFiles(path) {
			t.Error("expected true for jsonl with marker")
		}
	})

	t.Run("does not skip jsonl without marker", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "clean.jsonl")
		writeFixtureFile(t, path, `{"content":"hello"}`)
		if skipDreamerMarkedFiles(path) {
			t.Error("expected false for jsonl without marker")
		}
	})

	t.Run("does not skip non-jsonl file with marker", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "marked.json")
		writeFixtureFile(t, path, `{"content":"<!-- dreamer-analysis-marker -->"}`)
		if skipDreamerMarkedFiles(path) {
			t.Error("expected false for non-jsonl file")
		}
	})
}

// ---------------------------------------------------------------------------
// pathWithinNormalizedRoot
// ---------------------------------------------------------------------------

func TestPathWithinNormalizedRoot(t *testing.T) {
	t.Run("path inside root", func(t *testing.T) {
		root := t.TempDir()
		child := filepath.Join(root, "subdir", "file.txt")
		if !pathWithinNormalizedRoot(child, root) {
			t.Error("expected true for path inside root")
		}
	})

	t.Run("path outside root", func(t *testing.T) {
		root := t.TempDir()
		other := filepath.Join(t.TempDir(), "other", "file.txt")
		if pathWithinNormalizedRoot(other, root) {
			t.Error("expected false for path outside root")
		}
	})

	t.Run("path equals root returns true", func(t *testing.T) {
		root := t.TempDir()
		if !pathWithinNormalizedRoot(root, root) {
			t.Error("expected true when path equals root")
		}
	})

	t.Run("case mismatch on Windows", func(t *testing.T) {
		if runtime.GOOS != "windows" {
			t.Skip("windows-specific case test")
		}
		root := t.TempDir()
		upper := strings.ToUpper(root)
		lower := strings.ToLower(root)
		if upper == lower {
			t.Skip("filesystem is case-insensitive and paths are already same")
		}
		child := filepath.Join(upper, "subdir", "file.txt")
		if !pathWithinNormalizedRoot(child, lower) {
			t.Error("expected true for case-mismatched path inside root on Windows")
		}
	})

	t.Run("empty path returns false", func(t *testing.T) {
		root := t.TempDir()
		if pathWithinNormalizedRoot("", root) {
			t.Error("expected false for empty path")
		}
	})

	t.Run("empty root returns false", func(t *testing.T) {
		root := t.TempDir()
		child := filepath.Join(root, "file.txt")
		if pathWithinNormalizedRoot(child, "") {
			t.Error("expected false for empty root")
		}
	})
}

// ---------------------------------------------------------------------------
// deleteSourceFile
// ---------------------------------------------------------------------------

func TestDeleteSourceFile(t *testing.T) {
	t.Run("deletes existing file", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "to-delete.jsonl")
		writeFixtureFile(t, path, "content")
		if err := deleteSourceFile(path); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Error("expected file to be deleted")
		}
	})

	t.Run("missing file is idempotent", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "missing.jsonl")
		if err := deleteSourceFile(path); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("empty path returns error", func(t *testing.T) {
		if err := deleteSourceFile(""); err == nil {
			t.Error("expected error for empty path")
		}
	})
}

// ---------------------------------------------------------------------------
// statSourceSize
// ---------------------------------------------------------------------------

func TestStatSourceSize(t *testing.T) {
	t.Run("existing file returns size", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "size-test.jsonl")
		writeFixtureFile(t, path, "hello world")
		size, err := statSourceSize(path)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if size != 11 {
			t.Errorf("size = %d, want 11", size)
		}
	})

	t.Run("missing file returns 0, nil", func(t *testing.T) {
		size, err := statSourceSize(filepath.Join(t.TempDir(), "missing.jsonl"))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if size != 0 {
			t.Errorf("size = %d, want 0", size)
		}
	})

	t.Run("empty path returns 0, nil", func(t *testing.T) {
		size, err := statSourceSize("")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if size != 0 {
			t.Errorf("size = %d, want 0", size)
		}
	})
}

// ---------------------------------------------------------------------------
// SplitSQLiteSourcePath
// ---------------------------------------------------------------------------

func TestSplitSQLiteSourcePath(t *testing.T) {
	t.Run("path with separator", func(t *testing.T) {
		dbPath, sessionID := SplitSQLiteSourcePath("/data/db.sqlite#sess-123")
		if dbPath != "/data/db.sqlite" {
			t.Errorf("dbPath = %q, want /data/db.sqlite", dbPath)
		}
		if sessionID != "sess-123" {
			t.Errorf("sessionID = %q, want sess-123", sessionID)
		}
	})

	t.Run("path without separator", func(t *testing.T) {
		dbPath, sessionID := SplitSQLiteSourcePath("/data/db.sqlite")
		if dbPath != "/data/db.sqlite" {
			t.Errorf("dbPath = %q, want /data/db.sqlite", dbPath)
		}
		if sessionID != "" {
			t.Errorf("sessionID = %q, want empty", sessionID)
		}
	})

	t.Run("empty path", func(t *testing.T) {
		dbPath, sessionID := SplitSQLiteSourcePath("")
		if dbPath != "" || sessionID != "" {
			t.Errorf("got (%q, %q), want empty", dbPath, sessionID)
		}
	})

	t.Run("path with multiple separators splits on last", func(t *testing.T) {
		dbPath, sessionID := SplitSQLiteSourcePath("/data#db#sess")
		if dbPath != "/data#db" {
			t.Errorf("dbPath = %q, want /data#db", dbPath)
		}
		if sessionID != "sess" {
			t.Errorf("sessionID = %q, want sess", sessionID)
		}
	})
}

// ---------------------------------------------------------------------------
// ProviderFor
// ---------------------------------------------------------------------------

func TestProviderFor(t *testing.T) {
	t.Run("known source type returns provider", func(t *testing.T) {
		provider, ok := ProviderFor(SourceTypeCopilotSessionJSONL)
		if !ok {
			t.Fatal("expected copilot provider to be registered")
		}
		if provider.Type() != SourceTypeCopilotSessionJSONL {
			t.Errorf("provider type = %q, want %q", provider.Type(), SourceTypeCopilotSessionJSONL)
		}
	})

	t.Run("unknown source type returns false", func(t *testing.T) {
		_, ok := ProviderFor(SourceType("nonexistent-type"))
		if ok {
			t.Error("expected false for unknown source type")
		}
	})
}

// ---------------------------------------------------------------------------
// Providers
// ---------------------------------------------------------------------------

func TestProvidersReturnsRegisteredProviders(t *testing.T) {
	providers := Providers()
	if len(providers) == 0 {
		t.Fatal("expected at least one registered provider")
	}

	seen := make(map[SourceType]bool)
	for _, p := range providers {
		seen[p.Type()] = true
	}
	for _, expected := range []SourceType{
		SourceTypeCopilotSessionJSONL,
		SourceTypeCodexSessionJSONL,
		SourceTypeVSCodeChatSession,
		SourceTypeClaudeCodeSession,
		SourceTypeAntigravityGemini,
		SourceTypeGeminiCLISession,
	} {
		if !seen[expected] {
			t.Errorf("expected provider for %q to be registered", expected)
		}
	}
}

// ---------------------------------------------------------------------------
// codebuffProjectMatches
// ---------------------------------------------------------------------------

func TestCodebuffProjectMatches(t *testing.T) {
	tests := []struct {
		name        string
		dirName     string
		projectBase string
		want        bool
	}{
		{"exact match", "myproject", "myproject", true},
		{"case insensitive", "MyProject", "myproject", true},
		{"trimmed spaces", " myproject ", "myproject", true},
		{"mismatch", "other", "myproject", false},
		{"empty both", "", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := codebuffProjectMatches(tt.dirName, tt.projectBase)
			if got != tt.want {
				t.Errorf("codebuffProjectMatches(%q, %q) = %v, want %v", tt.dirName, tt.projectBase, got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// DiscoverChatsWithEnvironment
// ---------------------------------------------------------------------------

func TestDiscoverChatsWithEnvironmentReturnsSorted(t *testing.T) {
	homeDir := t.TempDir()
	projectDir := t.TempDir()

	copilotFile := filepath.Join(homeDir, ".copilot", "session-state", "chat.jsonl")
	writeFixtureFile(t, copilotFile, "copilot")

	olderTime := time.Now().UTC().Add(-2 * time.Hour).Truncate(time.Second)
	setModTime(t, copilotFile, olderTime)

	sources, err := DiscoverChatsWithEnvironment(DiscoveryEnvironment{
		HomeDir: homeDir,
	}, projectDir)
	if err != nil {
		t.Fatalf("DiscoverChatsWithEnvironment returned error: %v", err)
	}
	// Copilot is always included (no project scoping), so at least 1.
	if len(sources) < 1 {
		t.Fatalf("expected at least 1 source, got %d", len(sources))
	}
}

// ---------------------------------------------------------------------------
// normalizeDiscoveryPathWithOptions (requireAbsolute=true path)
// ---------------------------------------------------------------------------

func TestNormalizeDiscoveryEvidencePathRejectsRelative(t *testing.T) {
	got, ok := normalizeDiscoveryEvidencePath("relative/path")
	if ok {
		t.Errorf("expected ok=false for relative path, got ok=true path=%q", got)
	}
}

func TestNormalizeDiscoveryEvidencePathAcceptsAbsolute(t *testing.T) {
	root := t.TempDir()
	got, ok := normalizeDiscoveryEvidencePath(root)
	if !ok {
		t.Fatal("expected ok=true for absolute path")
	}
	if got == "" {
		t.Error("expected non-empty path")
	}
}

func TestNormalizeDiscoveryPathEmptyReturnsFalse(t *testing.T) {
	_, ok := normalizeDiscoveryPath("")
	if ok {
		t.Error("expected false for empty path")
	}
}

func TestNormalizeDiscoveryPathWhitespaceReturnsFalse(t *testing.T) {
	_, ok := normalizeDiscoveryPath("   ")
	if ok {
		t.Error("expected false for whitespace-only path")
	}
}

// ---------------------------------------------------------------------------
// sqliteReaderAvailable
// ---------------------------------------------------------------------------

func TestSqliteReaderAvailable(t *testing.T) {
	t.Run("non-nil open hook returns true", func(t *testing.T) {
		hook := func(string, string) (*sql.DB, error) { return nil, nil }
		if !sqliteReaderAvailable("", hook) {
			t.Error("expected true when openHook is non-nil")
		}
	})

	t.Run("custom driver name returns true", func(t *testing.T) {
		if !sqliteReaderAvailable("custom-driver", nil) {
			t.Error("expected true for non-default driver name")
		}
	})

	t.Run("default driver name without hook depends on sql.Drivers", func(t *testing.T) {
		// This test verifies it doesn't panic; result depends on whether
		// the sqlite driver is registered in this test binary.
		_ = sqliteReaderAvailable("sqlite", nil)
	})

	t.Run("empty driver name without hook depends on sql.Drivers", func(t *testing.T) {
		_ = sqliteReaderAvailable("", nil)
	})
}

// ---------------------------------------------------------------------------
// Provider Type() methods - exercised through ProviderFor
// ---------------------------------------------------------------------------

func TestAllProviderTypesRegistered(t *testing.T) {
	expectedTypes := []SourceType{
		SourceTypeCopilotSessionJSONL,
		SourceTypeCodexSessionJSONL,
		SourceTypeVSCodeChatSession,
		SourceTypeClaudeCodeSession,
		SourceTypeAntigravityGemini,
		SourceTypeGeminiCLISession,
		SourceTypeOpenCodeSession,
		SourceTypeKiroCLISession,
		SourceTypeCodebuffSession,
	}
	for _, st := range expectedTypes {
		provider, ok := ProviderFor(st)
		if !ok {
			t.Errorf("ProviderFor(%q) not found", st)
			continue
		}
		if provider.Type() != st {
			t.Errorf("provider.Type() = %q, want %q", provider.Type(), st)
		}
	}
}

// ---------------------------------------------------------------------------
// discoverCodebuffSessions
// ---------------------------------------------------------------------------

func TestDiscoverCodebuffSessionsFindsChat(t *testing.T) {
	homeDir := t.TempDir()
	projectDir := t.TempDir()
	projectBase := filepath.Base(projectDir)

	// Build the codebuff project structure: projects/<name>/chats/<id>/chat-messages.json
	chatDir := filepath.Join(homeDir, ".config", "manicode", "projects", projectBase, "chats", "chat-1")
	writeFixtureFile(t, filepath.Join(chatDir, "chat-messages.json"), `[{"role":"user","content":"hi"}]`)

	sources, err := discoverCodebuffSessions(homeDir, "", projectDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(sources) != 1 {
		t.Fatalf("expected 1 source, got %d", len(sources))
	}
	if sources[0].Tool != SourceTypeCodebuffSession {
		t.Errorf("tool = %q, want %q", sources[0].Tool, SourceTypeCodebuffSession)
	}
}

func TestDiscoverCodebuffSessionsUsesConfigDirOverride(t *testing.T) {
	projectDir := t.TempDir()
	projectBase := filepath.Base(projectDir)

	configDir := t.TempDir()
	chatDir := filepath.Join(configDir, "projects", projectBase, "chats", "chat-1")
	writeFixtureFile(t, filepath.Join(chatDir, "chat-messages.json"), `[]`)

	sources, err := discoverCodebuffSessions("/no/home", configDir, projectDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(sources) != 1 {
		t.Fatalf("expected 1 source, got %d", len(sources))
	}
}

func TestDiscoverCodebuffSessionsNoProjectsDir(t *testing.T) {
	sources, err := discoverCodebuffSessions(t.TempDir(), "", t.TempDir())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(sources) != 0 {
		t.Errorf("expected 0 sources, got %d", len(sources))
	}
}

func TestDiscoverCodebuffSessionsSkipsNonDirEntries(t *testing.T) {
	projectDir := t.TempDir()
	projectBase := filepath.Base(projectDir)

	homeDir := t.TempDir()
	projectsDir := filepath.Join(homeDir, ".config", "manicode", "projects", projectBase)
	// Put a regular file (not dir) at the project level
	writeFixtureFile(t, filepath.Join(projectsDir, "not-a-dir.txt"), "")

	sources, err := discoverCodebuffSessions(homeDir, "", projectDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(sources) != 0 {
		t.Errorf("expected 0 sources, got %d", len(sources))
	}
}

func TestDiscoverCodebuffSessionsSkipsProjectMismatch(t *testing.T) {
	projectDir := t.TempDir()
	homeDir := t.TempDir()

	// Create a different project name
	chatDir := filepath.Join(homeDir, ".config", "manicode", "projects", "otherproject", "chats", "chat-1")
	writeFixtureFile(t, filepath.Join(chatDir, "chat-messages.json"), `[]`)

	sources, err := discoverCodebuffSessions(homeDir, "", projectDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(sources) != 0 {
		t.Errorf("expected 0 sources, got %d", len(sources))
	}
}

func TestDiscoverCodebuffSessionsSkipsNonDirChats(t *testing.T) {
	projectDir := t.TempDir()
	projectBase := filepath.Base(projectDir)
	homeDir := t.TempDir()

	chatDir := filepath.Join(homeDir, ".config", "manicode", "projects", projectBase, "chats")
	writeFixtureFile(t, filepath.Join(chatDir, "chat-1"), "not-a-dir")

	sources, err := discoverCodebuffSessions(homeDir, "", projectDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(sources) != 0 {
		t.Errorf("expected 0 sources, got %d", len(sources))
	}
}

// ---------------------------------------------------------------------------
// Provider SizeBytes and DeleteSource via file-backed providers
// ---------------------------------------------------------------------------

func TestCopilotProviderSizeBytes(t *testing.T) {
	provider, _ := ProviderFor(SourceTypeCopilotSessionJSONL)
	path := filepath.Join(t.TempDir(), "test.jsonl")
	writeFixtureFile(t, path, "hello")
	size, err := provider.SizeBytes(Source{Path: path, Tool: SourceTypeCopilotSessionJSONL})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if size != 5 {
		t.Errorf("size = %d, want 5", size)
	}
}

func TestCopilotProviderDeleteSource(t *testing.T) {
	provider, _ := ProviderFor(SourceTypeCopilotSessionJSONL)
	path := filepath.Join(t.TempDir(), "to-delete.jsonl")
	writeFixtureFile(t, path, "content")
	err := provider.DeleteSource(Source{Path: path, Tool: SourceTypeCopilotSessionJSONL})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
		t.Error("expected file to be deleted")
	}
}

func TestClaudeProviderDeleteSource(t *testing.T) {
	provider, _ := ProviderFor(SourceTypeClaudeCodeSession)
	path := filepath.Join(t.TempDir(), "to-delete.jsonl")
	writeFixtureFile(t, path, "content")
	err := provider.DeleteSource(Source{Path: path, Tool: SourceTypeClaudeCodeSession})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
		t.Error("expected file to be deleted")
	}
}

func TestCodebuffProviderSizeBytes(t *testing.T) {
	provider, _ := ProviderFor(SourceTypeCodebuffSession)
	path := filepath.Join(t.TempDir(), "test.json")
	writeFixtureFile(t, path, `[]`)
	size, err := provider.SizeBytes(Source{Path: path, Tool: SourceTypeCodebuffSession})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if size != 2 {
		t.Errorf("size = %d, want 2", size)
	}
}

func TestCodebuffProviderDeleteSource(t *testing.T) {
	provider, _ := ProviderFor(SourceTypeCodebuffSession)
	path := filepath.Join(t.TempDir(), "to-delete.json")
	writeFixtureFile(t, path, `[]`)
	err := provider.DeleteSource(Source{Path: path, Tool: SourceTypeCodebuffSession})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
		t.Error("expected file to be deleted")
	}
}

type fakeDiscoveryProvider struct {
	sourceType SourceType
	sources    []Source
	err        error
}

func (p fakeDiscoveryProvider) Type() SourceType { return p.sourceType }

func (p fakeDiscoveryProvider) Discover(DiscoveryEnvironment, string) ([]Source, error) {
	return p.sources, p.err
}

func (fakeDiscoveryProvider) ReadMessages(Source) ([]readers.ChatMessage, error) {
	return nil, errors.New("not implemented")
}

func (fakeDiscoveryProvider) DeleteSource(Source) error { return nil }

func (fakeDiscoveryProvider) SizeBytes(Source) (int64, error) { return 0, nil }

func withRegisteredProviders(t *testing.T, providers ...SourceProvider) {
	t.Helper()
	originalProviders := registeredProviders
	registeredProviders = providers
	t.Cleanup(func() {
		registeredProviders = originalProviders
	})
}
