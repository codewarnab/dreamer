package chat

// DreamerMarker is prepended to every prompt sent by dreamer's analyzer
// providers. The marker appears in session JSONL files as part of the user
// message content, allowing discovery to filter out dreamer-created sessions
// and break the analysis feedback loop.
//
// The string is an HTML comment — harmless to LLMs, invisible in rendered output.
const DreamerMarker = "<!-- dreamer-analysis-marker -->"

// PrependMarker returns prompt with DreamerMarker prepended. All providers
// call this to ensure consistent marker injection.
func PrependMarker(prompt string) string {
	return DreamerMarker + " " + prompt
}
