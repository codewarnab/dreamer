package readers

import (
	"regexp"
	"strings"
)

const (
	codexMaxCharsPerMessage   = 4000
	codexMaxMessagesPerSource = 250
)

var (
	codexANSIEscapePattern    = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]`)
	codexWhitespaceBurstRegex = regexp.MustCompile(`[\t\r\n ]+`)
)

type codexDropRule struct {
	name               string
	normalizedTokens   []string
	normalizedPrefixes []string
}

var codexDropRules = []codexDropRule{
	{
		name:               "repo-instruction-bootstrap",
		normalizedPrefixes: []string{"# agents.md instructions"},
	},
	{
		name:             "codex-runtime-bootstrap",
		normalizedTokens: []string{"<permissions instructions>", "<environment_context>", "<collaboration_mode>"},
	},
}

// SanitizeCodexMessages removes Codex session bootstrap text while preserving
// the human and assistant conversation that is useful for project analysis.
//
// Codex stores repository instructions and runtime context as role-bearing
// messages near the start of a session. Those records are useful to Codex while
// working, but they become repetitive noise when dreamer asks the analyzer to
// infer actionable project todos from prior chats.
func SanitizeCodexMessages(messages []ChatMessage) []ChatMessage {
	if len(messages) == 0 {
		return nil
	}

	sanitizedMessages := make([]ChatMessage, 0, min(len(messages), codexMaxMessagesPerSource))
	previousKey := ""

	for _, message := range messages {
		normalizedContent := normalizeCodexContent(message.Content)
		if normalizedContent == "" {
			continue
		}

		normalizedLower := strings.ToLower(normalizedContent)
		if shouldDropCodexMessage(normalizedLower) {
			continue
		}

		dedupeKey := message.Role + "\x00" + normalizedContent
		if dedupeKey == previousKey {
			continue
		}

		message.Content = normalizedContent
		sanitizedMessages = append(sanitizedMessages, message)
		previousKey = dedupeKey

		if codexMaxMessagesPerSource > 0 && len(sanitizedMessages) >= codexMaxMessagesPerSource {
			break
		}
	}

	return sanitizedMessages
}

func normalizeCodexContent(content string) string {
	normalized := codexANSIEscapePattern.ReplaceAllString(content, "")
	normalized = codexWhitespaceBurstRegex.ReplaceAllString(normalized, " ")
	normalized = strings.TrimSpace(normalized)
	if normalized == "" {
		return ""
	}

	runes := []rune(normalized)
	if len(runes) > codexMaxCharsPerMessage {
		normalized = strings.TrimSpace(string(runes[:codexMaxCharsPerMessage]))
	}

	return normalized
}

func shouldDropCodexMessage(normalizedLower string) bool {
	for _, rule := range codexDropRules {
		if containsAny(normalizedLower, rule.normalizedTokens...) {
			return true
		}
		if containsAnyPrefix(normalizedLower, rule.normalizedPrefixes...) {
			return true
		}
	}
	return false
}
