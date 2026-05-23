package readers

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// KiroConversation describes a Kiro CLI conversation row. The Kiro CLI stores
// each project's conversations in `conversations_v2(key TEXT, conversation_id
// TEXT, value TEXT, created_at, updated_at)` where `key` is the cwd and
// `value` is a JSON blob containing the conversation history.
type KiroConversation struct {
	ConversationID string
	Directory      string
	ModifiedTime   time.Time
}

type KiroReader struct {
	DriverName string
	Open       func(driverName string, dataSourceName string) (*sql.DB, error)
}

// ListKiroConversations returns conversations whose `key` (cwd) matches the
// provided filter. Empty filter returns all conversations.
func ListKiroConversations(dbPath string, cwdFilter string) ([]KiroConversation, error) {
	return KiroReader{}.ListConversations(dbPath, cwdFilter)
}

// ReadKiroConversation loads the user/assistant turns for a single Kiro CLI
// conversation row.
func ReadKiroConversation(dbPath string, conversationID string) ([]ChatMessage, error) {
	return KiroReader{}.ReadConversation(dbPath, conversationID)
}

func (reader KiroReader) ListConversations(dbPath string, cwdFilter string) ([]KiroConversation, error) {
	database, err := reader.openDatabase(dbPath)
	if err != nil {
		return nil, err
	}
	defer database.Close()

	query := "SELECT key, conversation_id, updated_at FROM conversations_v2"
	args := []any{}
	if trimmed := strings.TrimSpace(cwdFilter); trimmed != "" {
		query += " WHERE key = ?"
		args = append(args, trimmed)
	}

	rows, err := database.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("query kiro conversations: %w", err)
	}
	defer rows.Close()

	conversations := make([]KiroConversation, 0)
	for rows.Next() {
		var (
			key            string
			conversationID string
			updated        any
		)
		if err := rows.Scan(&key, &conversationID, &updated); err != nil {
			return nil, fmt.Errorf("scan kiro conversation row: %w", err)
		}
		modified, _ := parseTimestamp(updated)
		conversations = append(conversations, KiroConversation{
			ConversationID: conversationID,
			Directory:      key,
			ModifiedTime:   modified,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate kiro conversation rows: %w", err)
	}

	return conversations, nil
}

func (reader KiroReader) ReadConversation(dbPath string, conversationID string) ([]ChatMessage, error) {
	conversationID = strings.TrimSpace(conversationID)
	if conversationID == "" {
		return nil, fmt.Errorf("kiro conversation id is required")
	}

	database, err := reader.openDatabase(dbPath)
	if err != nil {
		return nil, err
	}
	defer database.Close()

	row := database.QueryRow("SELECT value FROM conversations_v2 WHERE conversation_id = ?", conversationID)
	var value string
	if err := row.Scan(&value); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("scan kiro conversation value: %w", err)
	}

	return parseKiroConversationValue(value), nil
}

func (reader KiroReader) openDatabase(dbPath string) (*sql.DB, error) {
	return openSQLDatabase(reader.DriverName, reader.Open, dbPath, "kiro")
}

// ConversationSizes returns sizes for many conversations in one DB-open.
// Missing rows are omitted from the result.
func (reader KiroReader) ConversationSizes(dbPath string, conversationIDs []string) (map[string]int64, error) {
	result := make(map[string]int64, len(conversationIDs))
	if len(conversationIDs) == 0 {
		return result, nil
	}
	database, err := reader.openDatabase(dbPath)
	if err != nil {
		return nil, err
	}
	defer database.Close()

	wanted := make(map[string]struct{}, len(conversationIDs))
	deduped := make([]string, 0, len(conversationIDs))
	for _, id := range conversationIDs {
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
		query := "SELECT conversation_id, length(value) FROM conversations_v2 WHERE conversation_id IN (" + strings.Join(placeholders, ",") + ")"
		rows, err := database.Query(query, args...)
		if err != nil {
			return nil, fmt.Errorf("query kiro conversation sizes: %w", err)
		}
		if err := scanKiroSizes(rows, result); err != nil {
			return nil, err
		}
	}
	return result, nil
}

func scanKiroSizes(rows *sql.Rows, accumulator map[string]int64) error {
	defer rows.Close()
	for rows.Next() {
		var id string
		var size sql.NullInt64
		if err := rows.Scan(&id, &size); err != nil {
			return fmt.Errorf("scan kiro conversation size row: %w", err)
		}
		accumulator[id] = size.Int64
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate kiro conversation size rows: %w", err)
	}
	return nil
}

// ConversationSize returns length(value) for one conversation row.
func (reader KiroReader) ConversationSize(dbPath string, conversationID string) (int64, error) {
	conversationID = strings.TrimSpace(conversationID)
	if conversationID == "" {
		return 0, fmt.Errorf("kiro conversation id is required")
	}
	database, err := reader.openDatabase(dbPath)
	if err != nil {
		return 0, err
	}
	defer database.Close()
	var size sql.NullInt64
	if err := database.QueryRow("SELECT length(value) FROM conversations_v2 WHERE conversation_id = ?", conversationID).Scan(&size); err != nil {
		if err == sql.ErrNoRows {
			return 0, nil
		}
		return 0, fmt.Errorf("query kiro conversation size: %w", err)
	}
	return size.Int64, nil
}

// DeleteKiroConversation removes one row from `conversations_v2`. No error
// when the row is absent.
func DeleteKiroConversation(dbPath string, conversationID string) error {
	return KiroReader{}.DeleteConversation(dbPath, conversationID)
}

func (reader KiroReader) DeleteConversation(dbPath string, conversationID string) error {
	conversationID = strings.TrimSpace(conversationID)
	if conversationID == "" {
		return fmt.Errorf("kiro conversation id is required")
	}
	database, err := reader.openDatabase(dbPath)
	if err != nil {
		return err
	}
	defer database.Close()
	if _, err := database.Exec("DELETE FROM conversations_v2 WHERE conversation_id = ?", conversationID); err != nil {
		return fmt.Errorf("delete kiro conversation: %w", err)
	}
	return nil
}

func parseKiroConversationValue(value string) []ChatMessage {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return nil
	}

	var record map[string]any
	if err := json.Unmarshal([]byte(trimmed), &record); err != nil {
		return nil
	}

	historyValue, ok := record["history"].([]any)
	if !ok {
		return nil
	}

	messages := make([]ChatMessage, 0, len(historyValue)*2)
	for _, item := range historyValue {
		turn, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if message, ok := kiroUserTurnMessage(turn); ok {
			messages = append(messages, message)
		}
		if message, ok := kiroAssistantTurnMessage(turn); ok {
			messages = append(messages, message)
		}
	}
	return messages
}

func kiroUserTurnMessage(turn map[string]any) (ChatMessage, bool) {
	userRecord, ok := turn["user"].(map[string]any)
	if !ok {
		return ChatMessage{}, false
	}
	content, ok := userRecord["content"].(map[string]any)
	if !ok {
		return ChatMessage{}, false
	}
	prompt, ok := content["Prompt"].(map[string]any)
	if !ok {
		return ChatMessage{}, false
	}
	text, _ := prompt["prompt"].(string)
	text = strings.TrimSpace(text)
	if text == "" {
		return ChatMessage{}, false
	}
	timestamp := time.Time{}
	if parsed, ok := parseTimestamp(userRecord["timestamp"]); ok {
		timestamp = parsed
	}
	return ChatMessage{Role: "user", Content: text, Timestamp: timestamp}, true
}

func kiroAssistantTurnMessage(turn map[string]any) (ChatMessage, bool) {
	assistantRecord, ok := turn["assistant"].(map[string]any)
	if !ok {
		return ChatMessage{}, false
	}
	response, ok := assistantRecord["Response"].(map[string]any)
	if !ok {
		return ChatMessage{}, false
	}
	text, _ := response["content"].(string)
	text = strings.TrimSpace(text)
	if text == "" {
		return ChatMessage{}, false
	}
	return ChatMessage{Role: "assistant", Content: text}, true
}
