package chat

import (
	"database/sql"
	"path/filepath"
	"testing"
)

// seedSourceHashDB creates real SQLite databases shaped like the opencode
// and kiro-cli stores. The provider SourceHash implementations always use
// the default "sqlite" driver (linked via internal/chat/readers), so these
// tests cannot use the fixture drivers that discovery tests rely on.
func seedSourceHashOpenCodeDB(t *testing.T) string {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "opencode.db")
	database, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open opencode sqlite: %v", err)
	}
	defer database.Close()

	const schema = `
		CREATE TABLE session (id TEXT PRIMARY KEY, directory TEXT, title TEXT, time_updated INTEGER, parent_id TEXT);
		CREATE TABLE message (id TEXT PRIMARY KEY, session_id TEXT, data TEXT, time_created INTEGER);
		CREATE TABLE part (id TEXT PRIMARY KEY, message_id TEXT, data TEXT, time_created INTEGER);
		INSERT INTO session VALUES ('s1', '/proj', 'alpha', 1700000000, '');
		INSERT INTO session VALUES ('s2', '/proj', 'beta', 1700000005, '');
		INSERT INTO message VALUES ('m1', 's1', '{"role":"user"}', 1700000000);`
	if _, err := database.Exec(schema); err != nil {
		t.Fatalf("seed opencode db: %v", err)
	}
	return dbPath
}

func seedSourceHashKiroDB(t *testing.T) string {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "data.sqlite3")
	database, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open kiro sqlite: %v", err)
	}
	defer database.Close()

	const schema = `
		CREATE TABLE conversations_v2 (key TEXT, conversation_id TEXT, value TEXT, created_at INTEGER, updated_at INTEGER);
		INSERT INTO conversations_v2 VALUES ('/proj', 'c1', '{"history":[1]}', 1700000000, 1700000000);`
	if _, err := database.Exec(schema); err != nil {
		t.Fatalf("seed kiro db: %v", err)
	}
	return dbPath
}

func TestSQLiteBackedProvidersImplementSourceHasher(t *testing.T) {
	for _, tool := range []SourceType{SourceTypeOpenCodeSession, SourceTypeKiroCLISession} {
		provider, ok := ProviderFor(tool)
		if !ok {
			t.Fatalf("no provider registered for %q", tool)
		}
		if _, ok := provider.(SourceHasher); !ok {
			t.Fatalf("provider for %q must implement SourceHasher", tool)
		}
	}
}

func TestFileBackedProvidersDoNotImplementSourceHasher(t *testing.T) {
	for _, tool := range []SourceType{
		SourceTypeCopilotSessionJSONL,
		SourceTypeCodexSessionJSONL,
		SourceTypeClaudeCodeSession,
	} {
		provider, ok := ProviderFor(tool)
		if !ok {
			t.Fatalf("no provider registered for %q", tool)
		}
		if _, ok := provider.(SourceHasher); ok {
			t.Fatalf("file-backed provider for %q must not implement SourceHasher; file sources hash their own path", tool)
		}
	}
}

func TestOpenCodeProviderSourceHashSplitsEncodedPath(t *testing.T) {
	dbPath := seedSourceHashOpenCodeDB(t)
	provider := openCodeProvider{}

	first, err := provider.SourceHash(Source{Path: dbPath + "#s1", Tool: SourceTypeOpenCodeSession})
	if err != nil {
		t.Fatalf("SourceHash(s1) returned error: %v", err)
	}
	second, err := provider.SourceHash(Source{Path: dbPath + "#s1", Tool: SourceTypeOpenCodeSession})
	if err != nil {
		t.Fatalf("SourceHash(s1) second call returned error: %v", err)
	}
	if first == "" || first != second {
		t.Fatalf("digest unstable or empty: %q vs %q", first, second)
	}

	other, err := provider.SourceHash(Source{Path: dbPath + "#s2", Tool: SourceTypeOpenCodeSession})
	if err != nil {
		t.Fatalf("SourceHash(s2) returned error: %v", err)
	}
	if other == first {
		t.Fatal("distinct sessions sharing one database must get distinct digests")
	}

	if _, err := provider.SourceHash(Source{Path: dbPath + "#ghost", Tool: SourceTypeOpenCodeSession}); err == nil {
		t.Fatal("missing session row must return an error so the cache keeps its prior key")
	}
	if _, err := provider.SourceHash(Source{Path: filepath.Join(t.TempDir(), "absent.db") + "#s1", Tool: SourceTypeOpenCodeSession}); err == nil {
		t.Fatal("absent database must return an error")
	}
}

func TestKiroProviderSourceHashSplitsEncodedPath(t *testing.T) {
	dbPath := seedSourceHashKiroDB(t)
	provider := kiroProvider{}

	first, err := provider.SourceHash(Source{Path: dbPath + "#c1", Tool: SourceTypeKiroCLISession})
	if err != nil {
		t.Fatalf("SourceHash(c1) returned error: %v", err)
	}
	second, err := provider.SourceHash(Source{Path: dbPath + "#c1", Tool: SourceTypeKiroCLISession})
	if err != nil {
		t.Fatalf("SourceHash(c1) second call returned error: %v", err)
	}
	if first == "" || first != second {
		t.Fatalf("digest unstable or empty: %q vs %q", first, second)
	}

	if _, err := provider.SourceHash(Source{Path: dbPath + "#ghost", Tool: SourceTypeKiroCLISession}); err == nil {
		t.Fatal("missing conversation row must return an error")
	}
}
