package chat

import (
	"database/sql"
	"strings"
	"time"

	"dreamer/internal/chat/readers"
)

type SourceType string

const (
	SourceTypeCopilotSessionJSONL SourceType = "copilot-session-jsonl"
	SourceTypeCodexSessionJSONL   SourceType = "codex-session-jsonl"
	SourceTypeVSCodeChatSession   SourceType = "vscode-chat-session"
	SourceTypeClaudeCodeSession   SourceType = "claude-code-session-jsonl"
	SourceTypeAntigravityGemini   SourceType = "antigravity-gemini-session"
	SourceTypeGeminiCLISession    SourceType = "gemini-cli-session-jsonl"
	SourceTypeOpenCodeSession     SourceType = "opencode-session-sqlite"
	SourceTypeKiroCLISession      SourceType = "kiro-cli-session-sqlite"
)

type ChatSource struct {
	Path         string
	Tool         SourceType
	ModifiedTime time.Time
}

// DiscoveryEnvironment captures the OS-derived inputs that drive chat source
// discovery. Tests build this manually; DiscoverChats resolves it from the
// process environment.
type DiscoveryEnvironment struct {
	HomeDir         string
	AppDataDir      string
	DataHomeDir     string
	ClaudeConfigDir string
	GeminiHomeDir   string
	OpenCodeDBPath  string
	KiroCLIDBPath   string
	OpenCodeReader  readers.OpenCodeReader
	KiroReader      readers.KiroReader
}

// sqliteSourcePathSeparator separates the database file path from the
// session/conversation identifier when a ChatSource refers to a single row
// inside a shared SQLite database (opencode, kiro-cli). Discovery encodes
// `<dbPath>#<sessionID>`; the runtime reader splits on this separator.
const sqliteSourcePathSeparator = "#"

// SplitSQLiteSourcePath returns (dbPath, sessionID) for a ChatSource path that
// was produced by SQLite-backed discovery. When the path has no separator the
// raw path is returned with an empty session id.
func SplitSQLiteSourcePath(path string) (string, string) {
	index := strings.LastIndex(path, sqliteSourcePathSeparator)
	if index < 0 {
		return path, ""
	}
	return path[:index], path[index+1:]
}

// defaultSQLiteDriverName mirrors the value from readers.SQLiteReader so the
// availability check stays in one place. Kept private to the package.
const defaultSQLiteDriverName = "sqlite"

// sqliteReaderAvailable reports whether the discovery layer should attempt to
// open a SQLite-backed chat source. When the caller provided a custom Open
// hook or a non-default driver name, discovery trusts them. Otherwise it
// requires the default sqlite3 driver to be registered in the process — this
// keeps discovery silent in builds that have not linked a sqlite driver.
func sqliteReaderAvailable(driverName string, openHook func(string, string) (*sql.DB, error)) bool {
	if openHook != nil {
		return true
	}
	if trimmed := strings.TrimSpace(driverName); trimmed != "" && trimmed != defaultSQLiteDriverName {
		return true
	}
	for _, name := range sql.Drivers() {
		if name == defaultSQLiteDriverName {
			return true
		}
	}
	return false
}

const (
	probeLineLimit         = 200
	probeInitialBufferSize = 64 * 1024
	probeMaxBufferSize     = 8 * 1024 * 1024
)
