package readers

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	initialScannerBufferSize = 64 * 1024
	maxScannerBufferSize     = 8 * 1024 * 1024
)

type ChatMessage struct {
	Role      string
	Content   string
	Timestamp time.Time
}

type JSONLReadOptions struct {
	SanitizeClaude         bool
	SanitizeCodex          bool
	SanitizeCopilotSession bool
}

func ReadJSONL(filePath string) ([]ChatMessage, error) {
	return ReadJSONLWithOptions(filePath, JSONLReadOptions{})
}

func ReadJSONLWithOptions(filePath string, options JSONLReadOptions) ([]ChatMessage, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return nil, fmt.Errorf("open jsonl file %q: %w", filePath, err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, initialScannerBufferSize), maxScannerBufferSize)

	messages := make([]ChatMessage, 0)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		message, ok := parseJSONLRecord(line)
		if !ok {
			continue
		}
		messages = append(messages, message)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan jsonl file %q: %w", filePath, err)
	}

	if options.SanitizeClaude {
		messages = SanitizeClaudeMessages(messages)
	}
	if options.SanitizeCodex {
		messages = SanitizeCodexMessages(messages)
	}
	if options.SanitizeCopilotSession {
		messages = SanitizeCopilotSessionMessages(messages)
	}

	return messages, nil
}

func parseJSONLRecord(line string) (ChatMessage, bool) {
	var record map[string]any
	if err := json.Unmarshal([]byte(line), &record); err != nil {
		return ChatMessage{}, false
	}

	if message, ok := copilotSessionMessageFromRecord(record); ok {
		return message, true
	}

	if message, ok := messageFromMap(record, record); ok {
		return message, true
	}

	for _, nestedValue := range nestedMessageCandidates(record) {
		nestedRecord, ok := nestedValue.(map[string]any)
		if !ok {
			continue
		}

		if message, ok := messageFromMap(nestedRecord, record); ok {
			return message, true
		}
	}

	return ChatMessage{}, false
}

func copilotSessionMessageFromRecord(record map[string]any) (ChatMessage, bool) {
	recordType, ok := record["type"].(string)
	if !ok {
		return ChatMessage{}, false
	}

	var role string
	switch strings.TrimSpace(recordType) {
	case "user.message":
		role = "user"
	case "assistant.message":
		role = "assistant"
	default:
		return ChatMessage{}, false
	}

	data, ok := record["data"].(map[string]any)
	if !ok {
		return ChatMessage{}, false
	}

	content := textFromValue(data["content"], 0)
	if content == "" {
		return ChatMessage{}, false
	}

	return ChatMessage{
		Role:      role,
		Content:   content,
		Timestamp: timestampFromRecord(record),
	}, true
}

func nestedMessageCandidates(record map[string]any) []any {
	candidates := make([]any, 0, 8)

	for _, key := range []string{"message", "event", "data", "payload", "request", "response"} {
		if value, ok := record[key]; ok {
			candidates = append(candidates, value)
		}
	}

	if values, ok := record["messages"].([]any); ok {
		candidates = append(candidates, values...)
	}

	return candidates
}

func messageFromMap(record map[string]any, root map[string]any) (ChatMessage, bool) {
	role := roleFromRecord(record)
	if role == "" {
		return ChatMessage{}, false
	}

	content := contentFromRecord(record)
	if content == "" {
		return ChatMessage{}, false
	}

	timestamp := timestampFromRecord(record)
	if timestamp.IsZero() {
		timestamp = timestampFromRecord(root)
	}

	return ChatMessage{
		Role:      role,
		Content:   content,
		Timestamp: timestamp,
	}, true
}

func roleFromRecord(record map[string]any) string {
	for _, key := range []string{"role", "sender", "author"} {
		if role := normalizeRole(rawRole(record[key])); role != "" {
			return role
		}
	}

	if authorRecord, ok := record["author"].(map[string]any); ok {
		for _, key := range []string{"role", "type", "kind"} {
			if role := normalizeRole(rawRole(authorRecord[key])); role != "" {
				return role
			}
		}
	}

	return ""
}

func rawRole(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case map[string]any:
		for _, key := range []string{"role", "type", "kind", "name"} {
			if role := rawRole(typed[key]); role != "" {
				return role
			}
		}
	}
	return ""
}

func normalizeRole(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "user", "human", "prompt":
		return "user"
	case "assistant", "model", "ai", "copilot", "bot":
		return "assistant"
	default:
		return ""
	}
}

func contentFromRecord(record map[string]any) string {
	for _, key := range []string{"content", "text", "body", "message", "prompt", "response"} {
		if value, ok := record[key]; ok {
			if text := textFromValue(value, 0); text != "" {
				return text
			}
		}
	}

	return ""
}

func textFromValue(value any, depth int) string {
	const maxDepth = 8

	if depth > maxDepth || value == nil {
		return ""
	}

	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case []byte:
		return strings.TrimSpace(string(typed))
	case json.RawMessage:
		return strings.TrimSpace(string(typed))
	case []any:
		parts := make([]string, 0, len(typed))
		for _, item := range typed {
			if text := textFromValue(item, depth+1); text != "" {
				parts = append(parts, text)
			}
		}
		return strings.TrimSpace(strings.Join(parts, "\n"))
	case map[string]any:
		for _, key := range []string{"text", "value", "content", "body", "message"} {
			if text := textFromValue(typed[key], depth+1); text != "" {
				return text
			}
		}
		if parts, ok := typed["parts"].([]any); ok {
			if text := textFromValue(parts, depth+1); text != "" {
				return text
			}
		}
	}

	return ""
}

func timestampFromRecord(record map[string]any) time.Time {
	for _, key := range []string{"timestamp", "createdAt", "created_at", "time", "updatedAt", "updated_at"} {
		if parsed, ok := parseTimestamp(record[key]); ok {
			return parsed
		}
	}
	return time.Time{}
}

func parseTimestamp(value any) (time.Time, bool) {
	switch typed := value.(type) {
	case time.Time:
		return typed.UTC(), true
	case json.Number:
		if unix, err := typed.Int64(); err == nil {
			return unixTimestamp(unix), true
		}
		if floatValue, err := typed.Float64(); err == nil {
			return unixTimestamp(int64(floatValue)), true
		}
	case string:
		trimmed := strings.TrimSpace(typed)
		if trimmed == "" {
			return time.Time{}, false
		}

		if unix, err := strconv.ParseInt(trimmed, 10, 64); err == nil {
			return unixTimestamp(unix), true
		}
		if floatValue, err := strconv.ParseFloat(trimmed, 64); err == nil {
			return unixTimestamp(int64(floatValue)), true
		}

		for _, layout := range []string{
			time.RFC3339Nano,
			time.RFC3339,
			"2006-01-02 15:04:05",
			"2006-01-02T15:04:05",
		} {
			parsed, err := time.Parse(layout, trimmed)
			if err == nil {
				return parsed.UTC(), true
			}
		}
	case float64:
		return unixTimestamp(int64(typed)), true
	case int64:
		return unixTimestamp(typed), true
	case int:
		return unixTimestamp(int64(typed)), true
	case uint64:
		if typed > uint64(^uint64(0)>>1) {
			return time.Time{}, false
		}
		return unixTimestamp(int64(typed)), true
	case []byte:
		return parseTimestamp(string(typed))
	}

	return time.Time{}, false
}

func unixTimestamp(raw int64) time.Time {
	absolute := raw
	if absolute < 0 {
		absolute = -absolute
	}

	switch {
	case absolute > 1_000_000_000_000_000_000:
		return time.Unix(0, raw).UTC()
	case absolute > 1_000_000_000_000_000:
		return time.UnixMicro(raw).UTC()
	case absolute > 1_000_000_000_000:
		return time.UnixMilli(raw).UTC()
	default:
		return time.Unix(raw, 0).UTC()
	}
}
