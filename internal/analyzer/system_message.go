package analyzer

import (
	"fmt"
	"strings"
)

// BuildReadOnlySystemMessage returns the canonical read-only sandbox prompt
// applied to every provider session. The working directory is used to scope
// filesystem access; empty means no scoping. When runID is non-empty, a
// <session_id> tag is appended for transcript correlation.
func BuildReadOnlySystemMessage(workingDirectory, runID string) string {
	workingDirectory = strings.TrimSpace(workingDirectory)
	systemMessage := baseReadOnlyMessage()
	if workingDirectory != "" {
		systemMessage += fmt.Sprintf("Scope boundary: inspect only files under the project directory %q unless the request is an explicit web fetch.\n", workingDirectory)
		systemMessage += fmt.Sprintf("Never read files outside the project directory %q.\n", workingDirectory)
	}
	if runID != "" {
		systemMessage += fmt.Sprintf("<session_id>%s</session_id>\n", runID)
	}
	return systemMessage
}

// baseReadOnlyMessage returns the shared read-only system message lines.
func baseReadOnlyMessage() string {
	return "You are in read-only repository analysis mode.\n" +
		"Analyze the provided chat/discussion context first.\n" +
		"If additional evidence is needed, use targeted read/search inspection only.\n" +
		"Allowed actions: read/search operations, read-only shell commands, read-only MCP/custom tools (including embeddings/search), and web URL fetch/search.\n" +
		"Forbidden actions: file create/edit/delete, git history rewriting, branch mutation, or any destructive command.\n" +
		"Do not scan files aimlessly; inspect only relevant files suggested by the chat context.\n" +
		"For verification tasks: use Grep to search for patterns, Read to examine " +
		"specific files, and Glob to discover file structure. Do not fabricate code references."
}
