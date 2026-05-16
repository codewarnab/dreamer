package transport

import "strings"

// CapInputBytes shrinks body to <= maxBytes. It preserves the rule prompt
// header before "Chat transcript follows:" verbatim and tail-truncates the
// transcript (keeping the most recent messages). If no marker is present,
// it tail-truncates the whole body.
//
// truncNote is prepended to the truncated content to indicate truncation
// occurred. Callers can customize the message (e.g., "codex input cap" vs
// "agent input cap").
func CapInputBytes(body string, maxBytes int, truncNote string) string {
	if len(body) <= maxBytes {
		return body
	}
	const marker = "\n\nChat transcript follows:\n"
	idx := strings.Index(body, marker)
	if idx < 0 || idx+len(marker) >= maxBytes-len(truncNote) {
		// No marker, or header alone already exceeds budget: tail-truncate body.
		keep := maxBytes - len(truncNote)
		if keep < 0 {
			keep = 0
		}
		return truncNote + body[len(body)-keep:]
	}
	header := body[:idx+len(marker)]
	transcript := body[idx+len(marker):]
	keep := maxBytes - len(header) - len(truncNote)
	if keep < 0 {
		keep = 0
	}
	if keep >= len(transcript) {
		return body
	}
	return header + truncNote + transcript[len(transcript)-keep:]
}
