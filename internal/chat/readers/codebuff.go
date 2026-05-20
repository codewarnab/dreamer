package readers

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"
)

// ReadCodebuffMessages parses a Codebuff chat-messages.json file and returns
// the conversation as ChatMessage slices. Codebuff stores messages with a
// "variant" field (user/ai/agent/error) rather than "role".
func ReadCodebuffMessages(filePath string) ([]ChatMessage, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("read codebuff chat file %q: %w", filePath, err)
	}

	var raw []codebuffRawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse codebuff chat json %q: %w", filePath, err)
	}

	messages := make([]ChatMessage, 0, len(raw))
	for _, entry := range raw {
		msg, ok := convertCodebuffMessage(entry)
		if !ok {
			continue
		}
		messages = append(messages, msg)
	}
	return messages, nil
}

// codebuffRawMessage mirrors the JSON structure Codebuff writes to disk.
type codebuffRawMessage struct {
	ID        string               `json:"id"`
	Variant   string               `json:"variant"`
	Content   string               `json:"content"`
	Blocks    []codebuffRawBlock   `json:"blocks,omitempty"`
	Timestamp string               `json:"timestamp"`
}

// codebuffRawBlock represents a single content block inside a message.
// Agent blocks may nest child blocks via the Blocks field.
type codebuffRawBlock struct {
	Type    string             `json:"type"`
	Content string             `json:"content,omitempty"`
	Blocks  []codebuffRawBlock `json:"blocks,omitempty"`
}

// convertCodebuffMessage maps a raw Codebuff message to a readers.ChatMessage.
// Returns false for error-variant messages or messages with no extractable text.
func convertCodebuffMessage(raw codebuffRawMessage) (ChatMessage, bool) {
	role := codebuffRoleFromVariant(raw.Variant)
	if role == "" {
		return ChatMessage{}, false
	}

	content := extractCodebuffContent(raw)
	if strings.TrimSpace(content) == "" {
		return ChatMessage{}, false
	}

	timestamp := parseCodebuffTimestamp(raw.Timestamp)

	return ChatMessage{
		Role:      role,
		Content:   strings.TrimSpace(content),
		Timestamp: timestamp,
	}, true
}

// codebuffRoleFromVariant maps Codebuff's variant string to a standard role.
func codebuffRoleFromVariant(variant string) string {
	switch strings.ToLower(strings.TrimSpace(variant)) {
	case "user":
		return "user"
	case "ai", "agent":
		return "assistant"
	default:
		return ""
	}
}

// extractCodebuffContent returns the best text representation of a message.
// It prefers the top-level content field, falling back to walking blocks.
func extractCodebuffContent(raw codebuffRawMessage) string {
	if trimmed := strings.TrimSpace(raw.Content); trimmed != "" {
		return trimmed
	}
	return extractCodebuffBlockText(raw.Blocks)
}

// extractCodebuffBlockText walks content blocks and collects text entries.
// Agent blocks are recursed into: their Content and nested Blocks are both
// extracted so subagent reasoning and output become visible.
func extractCodebuffBlockText(blocks []codebuffRawBlock) string {
	if len(blocks) == 0 {
		return ""
	}
	pieces := make([]string, 0, len(blocks))
	for _, block := range blocks {
		switch block.Type {
		case "text":
			if trimmed := strings.TrimSpace(block.Content); trimmed != "" {
				pieces = append(pieces, trimmed)
			}
		case "agent":
			if trimmed := strings.TrimSpace(block.Content); trimmed != "" {
				pieces = append(pieces, trimmed)
			}
			if nested := extractCodebuffBlockText(block.Blocks); nested != "" {
				pieces = append(pieces, nested)
			}
		}
	}
	return strings.Join(pieces, "\n")
}

// parseCodebuffTimestamp parses an ISO 8601 timestamp string.
// Returns zero time on failure.
func parseCodebuffTimestamp(raw string) time.Time {
	if trimmed := strings.TrimSpace(raw); trimmed != "" {
		if t, err := time.Parse(time.RFC3339Nano, trimmed); err == nil {
			return t
		}
		if t, err := time.Parse(time.RFC3339, trimmed); err == nil {
			return t
		}
	}
	return time.Time{}
}
