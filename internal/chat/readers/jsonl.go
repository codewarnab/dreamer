package readers

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	initialScannerBufferSize = 64 * 1024
	maxScannerBufferSize     = 8 * 1024 * 1024
)

type ChatMessage struct {
	Role      string
	Content   string
	Timestamp time.Time
	ToolName  string // optional: "Bash", "Read", "Edit", etc. (empty for non-tool messages)
}

type JSONLReadOptions struct {
	Sanitizer func([]ChatMessage) []ChatMessage
	Budget    ReadBudget
}

// ReadBudget caps the decoded transcript bytes retained from one source.
// Chunking happens after all sources have already been decoded and formatted,
// so this guard must live in the reader layer to keep lifetime scans from
// accumulating an unbounded corpus in daemon memory.
type ReadBudget struct {
	MaxBytes int
}

type ReadResult struct {
	Messages  []ChatMessage
	Truncated bool
	Bytes     int
}

func ReadJSONL(filePath string) ([]ChatMessage, error) {
	result, err := ReadJSONLWithOptionsResult(filePath, JSONLReadOptions{})
	return result.Messages, err
}

func ReadJSONLWithOptions(filePath string, options JSONLReadOptions) ([]ChatMessage, error) {
	result, err := ReadJSONLWithOptionsResult(filePath, options)
	return result.Messages, err
}

func ReadJSONLWithOptionsResult(filePath string, options JSONLReadOptions) (ReadResult, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return ReadResult{}, fmt.Errorf("open jsonl file %q: %w", filePath, err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, initialScannerBufferSize), maxScannerBufferSize)

	messages := make([]ChatMessage, 0)
	droppedRecords := 0
	budget := newMessageBudget(options.Budget.MaxBytes)
	for scanner.Scan() {
		// Bytes() is allocation-free but only valid until the next Scan call.
		// Safe here: parseJSONLRecordBytes immediately calls json.Unmarshal,
		// which copies all data it needs before this function returns.
		raw := bytes.TrimSpace(scanner.Bytes())
		if len(raw) == 0 {
			continue
		}

		message, parseErr, ok := parseJSONLRecordBytes(raw)
		if parseErr != nil {
			droppedRecords++
			continue
		}
		if !ok {
			continue
		}
		message, ok = budget.keep(message)
		if !ok {
			break
		}
		messages = append(messages, message)
		if budget.truncated {
			break
		}
	}
	if err := scanner.Err(); err != nil {
		return ReadResult{}, fmt.Errorf("scan jsonl file %q: %w", filePath, err)
	}
	logDroppedParseRecords("jsonl reader", filePath, droppedRecords)

	if options.Sanitizer != nil {
		messages = options.Sanitizer(messages)
	}

	return ReadResult{
		Messages:  messages,
		Truncated: budget.truncated,
		Bytes:     budget.bytes,
	}, nil
}

type messageBudget struct {
	maxBytes  int
	bytes     int
	truncated bool
}

func newMessageBudget(maxBytes int) *messageBudget {
	return &messageBudget{maxBytes: maxBytes}
}

func (b *messageBudget) keep(message ChatMessage) (ChatMessage, bool) {
	overhead := retainedMessageOverhead(message)
	if b.maxBytes <= 0 {
		b.bytes += overhead + len(message.Content)
		return message, true
	}
	remaining := b.maxBytes - b.bytes - overhead
	if remaining <= 0 {
		b.truncated = true
		return ChatMessage{}, false
	}
	if len(message.Content) <= remaining {
		b.bytes += overhead + len(message.Content)
		return message, true
	}
	message.Content = truncateStringBytes(message.Content, remaining)
	b.bytes += overhead + len(message.Content)
	b.truncated = true
	return message, strings.TrimSpace(message.Content) != ""
}

func retainedMessageOverhead(message ChatMessage) int {
	// Mirrors the pipeline's transcript line shape closely enough for budget
	// enforcement without importing pipeline formatting into the reader layer.
	overhead := len(message.Role) + len(": \n")
	if !message.Timestamp.IsZero() {
		overhead += len("[] ") + len(time.RFC3339)
	}
	return overhead
}

func truncateStringBytes(value string, maxBytes int) string {
	if maxBytes <= 0 || len(value) <= maxBytes {
		if maxBytes <= 0 {
			return ""
		}
		return value
	}
	for maxBytes > 0 && !utf8.ValidString(value[:maxBytes]) {
		maxBytes--
	}
	return strings.TrimSpace(value[:maxBytes])
}

func parseJSONLRecord(line string) (ChatMessage, bool) {
	message, _, ok := parseJSONLRecordBytes([]byte(line))
	return message, ok
}

func parseJSONLRecordBytes(raw []byte) (ChatMessage, error, bool) {
	var record map[string]any
	if err := json.Unmarshal(raw, &record); err != nil {
		return ChatMessage{}, err, false
	}

	if message, ok := copilotSessionMessageFromRecord(record); ok {
		return message, nil, true
	}

	if message, ok := messageFromMap(record, record); ok {
		return message, nil, true
	}

	for _, nestedValue := range nestedMessageCandidates(record) {
		nestedRecord, ok := nestedValue.(map[string]any)
		if !ok {
			continue
		}

		if message, ok := messageFromMap(nestedRecord, record); ok {
			return message, nil, true
		}
	}

	return ChatMessage{}, nil, false
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
	if src := rawRole(record["source"]); src != "" {
		if role := normalizeRole(src); role != "" {
			if strings.EqualFold(role, "assistant") {
				if tp, ok := record["type"].(string); ok && strings.TrimSpace(tp) != "" {
					if !strings.EqualFold(strings.TrimSpace(tp), "planner_response") {
						return ""
					}
				}
			}
			return role
		}
		return ""
	}

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

func rawRole(roleCandidate any) string {
	switch typed := roleCandidate.(type) {
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
	case "user", "human", "prompt", "user_explicit":
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

// MaxDepth is the recursion limit for JSON value extraction functions that
// walk nested structures looking for text content. Also used by the parent
// chat package for probe-style evidence extraction.
const MaxDepth = 8

func textFromValue(value any, depth int) string {
	if depth > MaxDepth || value == nil {
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
		var sb strings.Builder
		for _, item := range typed {
			text := textFromValue(item, depth+1)
			if text == "" {
				continue
			}
			if sb.Len() > 0 {
				sb.WriteByte('\n')
			}
			sb.WriteString(text)
		}
		return strings.TrimSpace(sb.String())
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

func ParseTimestamp(value any) (time.Time, bool) {
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
			"2006-01-02 15:04:05.999999999-07:00",
			"2006-01-02 15:04:05-07:00",
			"2006-01-02 15:04:05.999999999",
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
		return ParseTimestamp(string(typed))
	}

	return time.Time{}, false
}

func parseTimestamp(value any) (time.Time, bool) {
	return ParseTimestamp(value)
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
