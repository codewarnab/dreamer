// Package transport provides shared utilities for analyzer provider implementations.
package transport

import "strings"

// IsRateLimitMessage returns true if msg contains well-known quota/rate-limit
// phrases from various LLM providers (Anthropic, OpenAI, etc.). This is the
// union of patterns from codexcli and acpcore.
func IsRateLimitMessage(msg string) bool {
	if msg == "" {
		return false
	}
	lower := strings.ToLower(msg)
	patterns := []string{
		"usage_limit_exceeded",
		"usage limit",
		"hit your usage limit",
		"rate limit",
		"rate_limit",
		"quota exceeded",
		"too many requests",
		"overloaded", // Anthropic-specific
	}
	for _, p := range patterns {
		if strings.Contains(lower, p) {
			return true
		}
	}
	return false
}
