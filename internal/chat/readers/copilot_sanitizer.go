package readers

import (
	"strings"
)


// SanitizeCopilotSessionMessages normalizes Copilot session JSONL messages and
// removes consecutive duplicates. Copilot event records are filtered at parse
// time, so this sanitizer intentionally stays small and avoids source-wide
// policy outside the reader layer.
func SanitizeCopilotSessionMessages(messages []ChatMessage) []ChatMessage {
	return sanitizeMessages(messages, normalizeCopilotContent, nil, 0)
}

func normalizeCopilotContent(content string) string {
	normalized := WhitespaceBurstRegex.ReplaceAllString(content, " ")
	return strings.TrimSpace(normalized)
}
