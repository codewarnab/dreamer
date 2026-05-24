package chat

import "fmt"

// DreamerMarker is prepended to every prompt sent by dreamer's analyzer
// providers. The marker appears in session JSONL files as part of the user
// message content, allowing discovery to filter out dreamer-created sessions
// and break the analysis feedback loop.
//
// The string is an HTML comment — harmless to LLMs, invisible in rendered output.
const DreamerMarker = "<!-- dreamer-analysis-marker -->"

// PrependMarker returns prompt with DreamerMarker prepended. When runID is
// non-empty, the marker includes it for transcript correlation:
// <!-- dreamer-analysis-marker run=abc12345 -->.
// The discovery filter checks for the substring "dreamer-analysis-marker",
// so the run= suffix is harmless.
func PrependMarker(prompt, runID string) string {
	if runID != "" {
		return fmt.Sprintf("<!-- dreamer-analysis-marker run=%s --> %s", runID, prompt)
	}
	return DreamerMarker + " " + prompt
}
