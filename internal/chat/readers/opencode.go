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
	// ParentID is set when this session is a subagent/child transcript.
	// Empty for top-level sessions.
	ParentID string
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

	sessions, err := reader.listSessionsWithParentID(database, cwdFilter)
	if err != nil {
		// Only fall back when the parent_id column is missing; surface other errors.
		if isMissingParentIDColumn(err) {
			return reader.listSessionsLegacy(database, cwdFilter)
		}
		return nil, err
	}
	return sessions, nil
}

// isMissingParentIDColumn reports whether err looks like a SQLite "no such
// column: parent_id" error from an older opencode schema.
func isMissingParentIDColumn(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "no such column") && strings.Contains(msg, "parent_id")
}

func (reader OpenCodeReader) listSessionsWithParentID(database *sql.DB, cwdFilter string) ([]OpenCodeSession, error) {
	query := "SELECT id, directory, title, time_updated, parent_id FROM session"
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
			parentID  sql.NullString
		)
		if err := rows.Scan(&id, &directory, &title, &updated, &parentID); err != nil {
			return nil, fmt.Errorf("scan opencode session row: %w", err)
		}
		modified, _ := parseTimestamp(updated)
		sessions = append(sessions, OpenCodeSession{
			ID:           id,
			Directory:    directory,
			Title:        title,
			ModifiedTime: modified,
			ParentID:     parentID.String,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate opencode session rows: %w", err)
	}

	return sessions, nil
}

func (reader OpenCodeReader) listSessionsLegacy(database *sql.DB, cwdFilter string) ([]OpenCodeSession, error) {
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
	case "subtask":
		return extractOpenCodeSubtask(record)
	case "tool":
		return extractOpenCodeTool(record)
	}
	return ""
}

// extractOpenCodeSubtask extracts text from a subtask part. The content may
// be nested under "data" (as a JSON string or object) or under "text".
func extractOpenCodeSubtask(record map[string]any) string {
	// Try "text" first (direct content).
	if text, ok := record["text"].(string); ok {
		if trimmed := strings.TrimSpace(text); trimmed != "" {
			return trimmed
		}
	}
	// Try "data" as a string containing JSON or plain text.
	if data, ok := record["data"].(string); ok {
		trimmed := strings.TrimSpace(data)
		if trimmed == "" {
			return ""
		}
		// Attempt to parse as JSON to extract nested text/content fields.
		var nested map[string]any
		if json.Unmarshal([]byte(trimmed), &nested) == nil {
			if text, ok := nested["text"].(string); ok {
				if t := strings.TrimSpace(text); t != "" {
					return t
				}
			}
			if content, ok := nested["content"].(string); ok {
				if t := strings.TrimSpace(content); t != "" {
					return t
				}
			}
		}
		// Fall back to the raw data string.
		return trimmed
	}
	return ""
}

// extractOpenCodeTool extracts a summary from a tool part: tool name plus
// result status when available.
func extractOpenCodeTool(record map[string]any) string {
	name, _ := record["tool"].(string)
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	// Append result/output summary if present.
	if result, ok := record["result"].(string); ok {
		if trimmed := strings.TrimSpace(result); trimmed != "" {
			return name + ": " + trimmed
		}
	}
	if output, ok := record["output"].(string); ok {
		if trimmed := strings.TrimSpace(output); trimmed != "" {
			return name + ": " + trimmed
		}
	}
	return name
}
