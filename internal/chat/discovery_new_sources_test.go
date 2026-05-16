package chat

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"dreamer/internal/chat/readers"
)

func TestDiscoverGeminiCLISessionsMatchesProject(t *testing.T) {
	homeDir := t.TempDir()
	projectDir := t.TempDir()
	otherDir := t.TempDir()

	matching := filepath.Join(homeDir, ".gemini", "tmp", "proj-slug", "chats", "session-1.jsonl")
	notInChats := filepath.Join(homeDir, ".gemini", "tmp", "proj-slug", "session-2.jsonl")
	outOfProject := filepath.Join(homeDir, ".gemini", "tmp", "other-slug", "chats", "session-3.jsonl")

	writeFixtureFile(t, matching, fmt.Sprintf(`{"sessionId":"a","directories":[%q]}`, projectDir))
	writeFixtureFile(t, notInChats, fmt.Sprintf(`{"sessionId":"b","directories":[%q]}`, projectDir))
	writeFixtureFile(t, outOfProject, fmt.Sprintf(`{"sessionId":"c","directories":[%q]}`, otherDir))

	sources, err := discoverGeminiCLISessions(homeDir, "", projectDir)
	if err != nil {
		t.Fatalf("discoverGeminiCLISessions returned error: %v", err)
	}
	if len(sources) != 1 {
		t.Fatalf("expected 1 gemini-cli source, got %d", len(sources))
	}
	if sources[0].Path != matching {
		t.Errorf("source path = %q, want %q", sources[0].Path, matching)
	}
	if sources[0].Tool != SourceTypeGeminiCLISession {
		t.Errorf("tool = %q, want %q", sources[0].Tool, SourceTypeGeminiCLISession)
	}
}

func TestDiscoverGeminiCLISessionsRespectsGeminiHomeOverride(t *testing.T) {
	overrideDir := t.TempDir()
	projectDir := t.TempDir()

	sessionFile := filepath.Join(overrideDir, "tmp", "slug", "chats", "session.jsonl")
	writeFixtureFile(t, sessionFile, fmt.Sprintf(`{"directories":[%q]}`, projectDir))

	sources, err := discoverGeminiCLISessions(t.TempDir(), overrideDir, projectDir)
	if err != nil {
		t.Fatalf("discoverGeminiCLISessions returned error: %v", err)
	}
	if len(sources) != 1 {
		t.Fatalf("expected 1 source, got %d", len(sources))
	}
}

func TestDiscoverOpenCodeSessionsFiltersByDirectory(t *testing.T) {
	dataHome := t.TempDir()
	projectDir := t.TempDir()
	otherDir := t.TempDir()

	dbPath := filepath.Join(dataHome, "opencode", "opencode.db")
	writeFixtureFile(t, dbPath, "fixture")

	openReader := newFixtureOpenCodeReader(map[string][]readers.OpenCodeSession{
		dbPath: {
			{ID: "s1", Directory: projectDir, Title: "in", ModifiedTime: time.Unix(1_700_000_000, 0).UTC()},
			{ID: "s2", Directory: otherDir, Title: "out", ModifiedTime: time.Unix(1_700_000_010, 0).UTC()},
		},
	})

	environment := DiscoveryEnvironment{
		HomeDir:        t.TempDir(),
		AppDataDir:     t.TempDir(),
		DataHomeDir:    dataHome,
		OpenCodeReader: openReader,
	}
	sources, err := discoverOpenCodeSessions(environment, projectDir)
	if err != nil {
		t.Fatalf("discoverOpenCodeSessions returned error: %v", err)
	}
	if len(sources) != 1 {
		t.Fatalf("expected 1 opencode source, got %d", len(sources))
	}
	dbPathOut, sessionID := SplitSQLiteSourcePath(sources[0].Path)
	if dbPathOut != dbPath || sessionID != "s1" {
		t.Errorf("source path split = (%q, %q), want (%q, s1)", dbPathOut, sessionID, dbPath)
	}
	if !sources[0].ModifiedTime.Equal(time.Unix(1_700_000_000, 0).UTC()) {
		t.Errorf("modified = %v", sources[0].ModifiedTime)
	}
}

func TestDiscoverOpenCodeSessionsNoDatabase(t *testing.T) {
	environment := DiscoveryEnvironment{
		HomeDir:     t.TempDir(),
		AppDataDir:  t.TempDir(),
		DataHomeDir: filepath.Join(t.TempDir(), "missing"),
	}
	sources, err := discoverOpenCodeSessions(environment, t.TempDir())
	if err != nil {
		t.Fatalf("discoverOpenCodeSessions returned error: %v", err)
	}
	if len(sources) != 0 {
		t.Fatalf("expected no sources, got %d", len(sources))
	}
}

func TestDiscoverKiroCLISessionsFiltersByDirectory(t *testing.T) {
	dataHome := t.TempDir()
	projectDir := t.TempDir()
	otherDir := t.TempDir()

	dbPath := filepath.Join(dataHome, "kiro-cli", "data.sqlite3")
	writeFixtureFile(t, dbPath, "fixture")

	kiroReader := newFixtureKiroReader(map[string][]readers.KiroConversation{
		dbPath: {
			{ConversationID: "c1", Directory: projectDir, ModifiedTime: time.Unix(1_700_000_100, 0).UTC()},
			{ConversationID: "c2", Directory: otherDir, ModifiedTime: time.Unix(1_700_000_200, 0).UTC()},
		},
	})

	environment := DiscoveryEnvironment{
		HomeDir:     t.TempDir(),
		AppDataDir:  t.TempDir(),
		DataHomeDir: dataHome,
		KiroReader:  kiroReader,
	}
	sources, err := discoverKiroCLISessions(environment, projectDir)
	if err != nil {
		t.Fatalf("discoverKiroCLISessions returned error: %v", err)
	}
	if len(sources) != 1 {
		t.Fatalf("expected 1 kiro source, got %d", len(sources))
	}
	dbPathOut, conversationID := SplitSQLiteSourcePath(sources[0].Path)
	if dbPathOut != dbPath || conversationID != "c1" {
		t.Errorf("source path split = (%q, %q), want (%q, c1)", dbPathOut, conversationID, dbPath)
	}
}

// newFixtureOpenCodeReader returns an OpenCodeReader whose Open hook serves the
// provided sessions for the requested database path. The reader still goes
// through the database/sql plumbing; results are injected via an in-memory
// driver scoped to the dataset.
func newFixtureOpenCodeReader(datasets map[string][]readers.OpenCodeSession) readers.OpenCodeReader {
	return readers.OpenCodeReader{
		DriverName: "dreamer-fixture-discover-opencode",
		Open: func(_ string, dsn string) (*sql.DB, error) {
			sessions, ok := datasets[dsn]
			if !ok {
				return nil, fmt.Errorf("missing opencode dataset for %q", dsn)
			}
			driverName := registerDiscoverOpenCodeDriver(sessions)
			return sql.Open(driverName, dsn)
		},
	}
}

func newFixtureKiroReader(datasets map[string][]readers.KiroConversation) readers.KiroReader {
	return readers.KiroReader{
		DriverName: "dreamer-fixture-discover-kiro",
		Open: func(_ string, dsn string) (*sql.DB, error) {
			conversations, ok := datasets[dsn]
			if !ok {
				return nil, fmt.Errorf("missing kiro dataset for %q", dsn)
			}
			driverName := registerDiscoverKiroDriver(conversations)
			return sql.Open(driverName, dsn)
		},
	}
}

// Test-only fixture infrastructure for discovery-level testing of SQLite
// sources lives in fixture_sqlite_drivers_test.go to keep this file focused on
// the discovery contracts.
var _ = strings.Contains
