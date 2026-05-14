package readers

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// ReadGeminiCLI reads a Gemini CLI session JSONL file and returns the user and
// assistant turns. The Gemini CLI session format is documented at:
//
//   ~/.gemini/tmp/<project-slug>/chats/session-YYYY-MM-DDTHH-mm-<id>.jsonl
//
// The first line is a ConversationRecord with metadata and an initial messages
// array. Subsequent lines are MessageRecord entries with a `type` field of
// "user", "gemini", or noise types ("info", "error", "warning"). Additional
// sentinel records `{"$set":...}` and `{"$rewindTo":...}` are emitted on edits
// and rewinds; both are skipped.
func ReadGeminiCLI(filePath string) ([]ChatMessage, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return nil, fmt.Errorf("open gemini cli session %q: %w", filePath, err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, initialScannerBufferSize), maxScannerBufferSize)

	messages := make([]ChatMessage, 0)
	lineNumber := 0
	for scanner.Scan() {
		lineNumber++
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		var record map[string]any
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			continue
		}
		if isGeminiSentinel(record) {
			continue
		}

		if lineNumber == 1 {
			if nested, ok := record["messages"].([]any); ok {
				for _, item := range nested {
					if message, ok := geminiMessageFromValue(item); ok {
						messages = append(messages, message)
					}
				}
				continue
			}
		}

		if message, ok := geminiMessageFromValue(record); ok {
			messages = append(messages, message)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan gemini cli session %q: %w", filePath, err)
	}

	return messages, nil
}

func isGeminiSentinel(record map[string]any) bool {
	for key := range record {
		if strings.HasPrefix(key, "$") {
			return true
		}
	}
	return false
}

func geminiMessageFromValue(value any) (ChatMessage, bool) {
	record, ok := value.(map[string]any)
	if !ok {
		return ChatMessage{}, false
	}

	role := normalizeGeminiRole(record)
	if role == "" {
		return ChatMessage{}, false
	}

	content := geminiContentFromRecord(record)
	if content == "" {
		return ChatMessage{}, false
	}

	return ChatMessage{
		Role:      role,
		Content:   content,
		Timestamp: timestampFromRecord(record),
	}, true
}

func normalizeGeminiRole(record map[string]any) string {
	recordType, _ := record["type"].(string)
	switch strings.ToLower(strings.TrimSpace(recordType)) {
	case "user":
		return "user"
	case "gemini", "model", "assistant":
		return "assistant"
	case "info", "error", "warning", "":
		// fall through; rely on explicit role field below
	default:
		return ""
	}

	if role := normalizeRole(rawRole(record["role"])); role != "" {
		return role
	}
	return ""
}

func geminiContentFromRecord(record map[string]any) string {
	if value, ok := record["content"]; ok {
		if text := geminiTextFromValue(value, 0); text != "" {
			return text
		}
	}
	for _, key := range []string{"displayContent", "text"} {
		if value, ok := record[key]; ok {
			if text := geminiTextFromValue(value, 0); text != "" {
				return text
			}
		}
	}
	return ""
}

func geminiTextFromValue(value any, depth int) string {
	const maxDepth = 8

	if depth > maxDepth || value == nil {
		return ""
	}

	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case []any:
		parts := make([]string, 0, len(typed))
		for _, item := range typed {
			if text := geminiTextFromValue(item, depth+1); text != "" {
				parts = append(parts, text)
			}
		}
		return strings.TrimSpace(strings.Join(parts, "\n"))
	case map[string]any:
		for _, key := range []string{"text", "content", "value"} {
			if text := geminiTextFromValue(typed[key], depth+1); text != "" {
				return text
			}
		}
	}
	return ""
}
