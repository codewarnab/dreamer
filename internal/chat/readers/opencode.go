package readers

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// OpenCodeSession describes an opencode session on disk: a single
// `opencode.db` SQLite database holds many sessions (`session` table) plus
// their messages (`message` table) and parts (`part` table).
type OpenCodeSession struct {
	ID           string
	Directory    string
	Title        string
	ModifiedTime time.Time
}

type OpenCodeReader struct {
	DriverName string
	Open       func(driverName string, dataSourceName string) (*sql.DB, error)
}

// ListOpenCodeSessions returns every session in the opencode database whose
// `directory` matches the provided cwd filter. When cwdFilter is empty all
// sessions are returned. Callers can post-filter on the returned Directory.
func ListOpenCodeSessions(dbPath string, cwdFilter string) ([]OpenCodeSession, error) {
	return OpenCodeReader{}.ListSessions(dbPath, cwdFilter)
}

// ReadOpenCodeMessages loads ordered user/assistant turns for a single session.
func ReadOpenCodeMessages(dbPath string, sessionID string) ([]ChatMessage, error) {
	return OpenCodeReader{}.ReadMessages(dbPath, sessionID)
}

func (reader OpenCodeReader) ListSessions(dbPath string, cwdFilter string) ([]OpenCodeSession, error) {
	database, err := reader.openDatabase(dbPath)
	if err != nil {
		return nil, err
	}
	defer database.Close()

	query := "SELECT id, directory, title, time_updated FROM session"
	args := []any{}
	if trimmed := strings.TrimSpace(cwdFilter); trimmed != "" {
		query += " WHERE directory = ?"
		args = append(args, trimmed)
	}

	rows, err := database.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("query opencode sessions: %w", err)
	}
	defer rows.Close()

	sessions := make([]OpenCodeSession, 0)
	for rows.Next() {
		var (
			id        string
			directory string
			title     string
			updated   any
		)
		if err := rows.Scan(&id, &directory, &title, &updated); err != nil {
			return nil, fmt.Errorf("scan opencode session row: %w", err)
		}
		modified, _ := parseTimestamp(updated)
		sessions = append(sessions, OpenCodeSession{
			ID:           id,
			Directory:    directory,
			Title:        title,
			ModifiedTime: modified,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate opencode session rows: %w", err)
	}

	return sessions, nil
}

func (reader OpenCodeReader) ReadMessages(dbPath string, sessionID string) ([]ChatMessage, error) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return nil, fmt.Errorf("opencode session id is required")
	}

	database, err := reader.openDatabase(dbPath)
	if err != nil {
		return nil, err
	}
	defer database.Close()

	rows, err := database.Query("SELECT id, data, time_created FROM message WHERE session_id = ? ORDER BY time_created, id", sessionID)
	if err != nil {
		return nil, fmt.Errorf("query opencode messages: %w", err)
	}

	type messageRow struct {
		ID      string
		Data    string
		Created any
	}
	messageRows := make([]messageRow, 0)
	for rows.Next() {
		var row messageRow
		if err := rows.Scan(&row.ID, &row.Data, &row.Created); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan opencode message row: %w", err)
		}
		messageRows = append(messageRows, row)
	}
	rows.Close()

	messages := make([]ChatMessage, 0, len(messageRows))
	for _, row := range messageRows {
		role := openCodeRoleFromMessageData(row.Data)
		if role == "" {
			continue
		}
		content, err := readOpenCodePartsContent(database, row.ID)
		if err != nil {
			return nil, err
		}
		if content == "" {
			continue
		}

		timestamp, _ := parseTimestamp(row.Created)
		messages = append(messages, ChatMessage{
			Role:      role,
			Content:   content,
			Timestamp: timestamp,
		})
	}

	return messages, nil
}

func (reader OpenCodeReader) openDatabase(dbPath string) (*sql.DB, error) {
	path := strings.TrimSpace(dbPath)
	if path == "" {
		return nil, fmt.Errorf("opencode database path is required")
	}

	driverName := strings.TrimSpace(reader.DriverName)
	if driverName == "" {
		driverName = defaultSQLiteDriverName
	}
	openDB := reader.Open
	if openDB == nil {
		openDB = sql.Open
	}

	database, err := openDB(driverName, path)
	if err != nil {
		return nil, fmt.Errorf("open opencode database %q with driver %q: %w", path, driverName, err)
	}
	return database, nil
}

func openCodeRoleFromMessageData(data string) string {
	trimmed := strings.TrimSpace(data)
	if trimmed == "" {
		return ""
	}
	var record map[string]any
	if err := json.Unmarshal([]byte(trimmed), &record); err != nil {
		return ""
	}
	raw, _ := record["role"].(string)
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "user":
		return "user"
	case "assistant":
		return "assistant"
	default:
		return ""
	}
}

func readOpenCodePartsContent(database *sql.DB, messageID string) (string, error) {
	rows, err := database.Query("SELECT data FROM part WHERE message_id = ? ORDER BY time_created, id", messageID)
	if err != nil {
		return "", fmt.Errorf("query opencode parts for message %q: %w", messageID, err)
	}
	defer rows.Close()

	pieces := make([]string, 0)
	for rows.Next() {
		var data string
		if err := rows.Scan(&data); err != nil {
			return "", fmt.Errorf("scan opencode part: %w", err)
		}
		if text := openCodePartText(data); text != "" {
			pieces = append(pieces, text)
		}
	}
	if err := rows.Err(); err != nil {
		return "", fmt.Errorf("iterate opencode parts: %w", err)
	}
	return strings.TrimSpace(strings.Join(pieces, "\n")), nil
}

func openCodePartText(data string) string {
	trimmed := strings.TrimSpace(data)
	if trimmed == "" {
		return ""
	}
	var record map[string]any
	if err := json.Unmarshal([]byte(trimmed), &record); err != nil {
		return ""
	}
	partType, _ := record["type"].(string)
	switch strings.ToLower(strings.TrimSpace(partType)) {
	case "text", "reasoning":
		if text, ok := record["text"].(string); ok {
			return strings.TrimSpace(text)
		}
	}
	return ""
}
