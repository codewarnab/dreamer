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
	return openSQLDatabase(reader.DriverName, reader.Open, dbPath, "opencode")
}

// SessionSize returns the approximate on-disk footprint (bytes) of a single
// opencode session: sum of message.data + part.data lengths. Cheap proxy for
// "how big is this chat" without summing arbitrary blob overhead. One round
// trip via UNION ALL.
func (reader OpenCodeReader) SessionSize(dbPath string, sessionID string) (int64, error) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return 0, fmt.Errorf("opencode session id is required")
	}
	database, err := reader.openDatabase(dbPath)
	if err != nil {
		return 0, err
	}
	defer database.Close()
	const query = `
		SELECT COALESCE(SUM(length(m.data)), 0)
		     + COALESCE(
		         (SELECT SUM(length(p.data))
		            FROM part p
		            JOIN message m2 ON p.message_id = m2.id
		           WHERE m2.session_id = ?), 0)
		  FROM message m
		 WHERE m.session_id = ?`
	var total sql.NullInt64
	if err := database.QueryRow(query, sessionID, sessionID).Scan(&total); err != nil {
		return 0, fmt.Errorf("sum opencode session bytes: %w", err)
	}
	return total.Int64, nil
}

// SessionSizes returns sizes for many sessions in a single DB-open. Sessions
// not present in the DB are simply absent from the returned map; callers
// should treat that as 0. Empty input returns an empty map without opening
// the database.
func (reader OpenCodeReader) SessionSizes(dbPath string, sessionIDs []string) (map[string]int64, error) {
	result := make(map[string]int64, len(sessionIDs))
	if len(sessionIDs) == 0 {
		return result, nil
	}
	database, err := reader.openDatabase(dbPath)
	if err != nil {
		return nil, err
	}
	defer database.Close()

	wanted := make(map[string]struct{}, len(sessionIDs))
	deduped := make([]string, 0, len(sessionIDs))
	for _, id := range sessionIDs {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if _, ok := wanted[id]; ok {
			continue
		}
		wanted[id] = struct{}{}
		deduped = append(deduped, id)
	}
	if len(deduped) == 0 {
		return result, nil
	}

	// SQLite's default SQLITE_MAX_VARIABLE_NUMBER is 999; chunk to leave headroom.
	const chunkSize = 500
	for start := 0; start < len(deduped); start += chunkSize {
		end := start + chunkSize
		if end > len(deduped) {
			end = len(deduped)
		}
		chunk := deduped[start:end]
		placeholders := make([]string, len(chunk))
		args := make([]any, len(chunk))
		for i, id := range chunk {
			placeholders[i] = "?"
			args[i] = id
		}
		inList := strings.Join(placeholders, ",")
		msgQuery := "SELECT session_id, COALESCE(SUM(length(data)), 0) FROM message WHERE session_id IN (" + inList + ") GROUP BY session_id"
		if err := scanSessionSizes(database, msgQuery, args, result); err != nil {
			return nil, fmt.Errorf("sum opencode message bytes batch: %w", err)
		}
		partQuery := "SELECT m.session_id, COALESCE(SUM(length(p.data)), 0) FROM part p JOIN message m ON p.message_id = m.id WHERE m.session_id IN (" + inList + ") GROUP BY m.session_id"
		if err := scanSessionSizes(database, partQuery, args, result); err != nil {
			return nil, fmt.Errorf("sum opencode part bytes batch: %w", err)
		}
	}
	return result, nil
}

func scanSessionSizes(database *sql.DB, query string, args []any, accumulator map[string]int64) error {
	rows, err := database.Query(query, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var sessionID string
		var size sql.NullInt64
		if err := rows.Scan(&sessionID, &size); err != nil {
			return err
		}
		accumulator[sessionID] += size.Int64
	}
	return rows.Err()
}

// DeleteOpenCodeSession removes a single session and its messages/parts from
// the opencode database. Returns nil even when the session does not exist.
func DeleteOpenCodeSession(dbPath string, sessionID string) error {
	return OpenCodeReader{}.DeleteSession(dbPath, sessionID)
}

// DeleteSession removes a session row plus its `message` and `part` children
// from the opencode database. Wrapped in a transaction so a partial failure
// leaves the DB unchanged.
func (reader OpenCodeReader) DeleteSession(dbPath string, sessionID string) error {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return fmt.Errorf("opencode session id is required")
	}
	database, err := reader.openDatabase(dbPath)
	if err != nil {
		return err
	}
	defer database.Close()

	tx, err := database.Begin()
	if err != nil {
		return fmt.Errorf("begin opencode delete tx: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.Exec("DELETE FROM part WHERE message_id IN (SELECT id FROM message WHERE session_id = ?)", sessionID); err != nil {
		return fmt.Errorf("delete opencode parts: %w", err)
	}
	if _, err := tx.Exec("DELETE FROM message WHERE session_id = ?", sessionID); err != nil {
		return fmt.Errorf("delete opencode messages: %w", err)
	}
	if _, err := tx.Exec("DELETE FROM session WHERE id = ?", sessionID); err != nil {
		return fmt.Errorf("delete opencode session: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit opencode delete tx: %w", err)
	}
	return nil
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
