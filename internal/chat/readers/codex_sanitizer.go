package readers

import (
	"strings"
)

const (
	codexMaxCharsPerMessage   = 4000
	codexMaxMessagesPerSource = 250
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
	return sanitizeMessages(messages, normalizeCodexContent, func(_, normalizedLower string) bool {
		return shouldDropCodexMessage(normalizedLower)
	}, codexMaxMessagesPerSource)
}

func normalizeCodexContent(content string) string {
	normalized := ANSIEscapePattern.ReplaceAllString(content, "")
	normalized = WhitespaceBurstRegex.ReplaceAllString(normalized, " ")
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
