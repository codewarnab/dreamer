package readers

import (
	"regexp"
	"strings"
)

var copilotWhitespaceBurstRegex = regexp.MustCompile(`[\t\r\n ]+`)

// SanitizeCopilotSessionMessages normalizes Copilot session JSONL messages and
// removes consecutive duplicates. Copilot event records are filtered at parse
// time, so this sanitizer intentionally stays small and avoids source-wide
// policy outside the reader layer.
func SanitizeCopilotSessionMessages(messages []ChatMessage) []ChatMessage {
	if len(messages) == 0 {
		return nil
	}

	sanitized := make([]ChatMessage, 0, len(messages))
	previousKey := ""
	for _, message := range messages {
		normalizedContent := copilotWhitespaceBurstRegex.ReplaceAllString(message.Content, " ")
		normalizedContent = strings.TrimSpace(normalizedContent)
		if normalizedContent == "" {
			continue
		}

		dedupeKey := message.Role + "\x00" + normalizedContent
		if dedupeKey == previousKey {
			continue
		}

		message.Content = normalizedContent
		sanitized = append(sanitized, message)
		previousKey = dedupeKey
	}

	return sanitized
}
