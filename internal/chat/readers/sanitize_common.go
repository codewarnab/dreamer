package readers

import (
	"regexp"
	"strings"
)

// Common regex patterns shared across all chat source sanitizers.
// Compiled once at init time; referenced by claude, codex, copilot,
// antigravity, and vscode sanitizers.
var (
	// ANSIEscapePattern matches ANSI escape sequences (CSI format).
	ANSIEscapePattern = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]`)

	// WhitespaceBurstRegex collapses runs of whitespace to a single space.
	WhitespaceBurstRegex = regexp.MustCompile(`[\t\r\n ]+`)
)

// sanitizeMessages is the shared sanitizer loop: normalize → drop-check →
// deduplicate consecutive duplicates → cap at maxMessages. Each sanitizer
// provides its own normalize function and optional shouldDrop predicate.
//
// shouldDrop receives the original raw content and the normalized content
// (both lowercased by the caller when needed). Pass nil when the sanitizer
// has no drop rules.
func sanitizeMessages(
	messages []ChatMessage,
	normalize func(content string) string,
	shouldDrop func(rawContent, normalizedContent string) bool,
	maxMessages int,
) []ChatMessage {
	if len(messages) == 0 {
		return nil
	}

	capacity := len(messages)
	if maxMessages > 0 && capacity > maxMessages {
		capacity = maxMessages
	}
	sanitized := make([]ChatMessage, 0, capacity)
	previousRole := ""
	previousContent := ""

	for _, message := range messages {
		normalizedContent := normalize(message.Content)
		if normalizedContent == "" {
			continue
		}

		if shouldDrop != nil {
			rawLower := strings.ToLower(message.Content)
			normalizedLower := strings.ToLower(normalizedContent)
			if shouldDrop(rawLower, normalizedLower) {
				continue
			}
		}

		// Deduplicate consecutive identical messages without building a
		// composite key string on every iteration.
		if message.Role == previousRole && normalizedContent == previousContent {
			continue
		}

		message.Content = normalizedContent
		sanitized = append(sanitized, message)
		previousRole = message.Role
		previousContent = normalizedContent

		if maxMessages > 0 && len(sanitized) >= maxMessages {
			break
		}
	}

	return sanitized
}
