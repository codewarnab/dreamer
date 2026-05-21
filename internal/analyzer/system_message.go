package analyzer

import (
	"fmt"
	"strings"
)

// BuildReadOnlySystemMessage returns the canonical read-only sandbox prompt
// applied to every provider session. The working directory is used to scope
// filesystem access; empty means no scoping.
func BuildReadOnlySystemMessage(workingDirectory string) string {
	workingDirectory = strings.TrimSpace(workingDirectory)
	toolGuidance := "For verification tasks: use Grep to search for patterns, Read to examine " +
		"specific files, and Glob to discover file structure. Do not fabricate code references."
	if workingDirectory == "" {
		return "You are in read-only repository analysis mode.\n" +
			"Analyze the provided chat/discussion context first.\n" +
			"If additional evidence is needed, use targeted read/search inspection only.\n" +
			"Allowed actions: read/search operations, read-only shell commands, read-only MCP/custom tools (including embeddings/search), and web URL fetch/search.\n" +
			"Forbidden actions: file create/edit/delete, git history rewriting, branch mutation, or any destructive command.\n" +
			"Do not scan files aimlessly; inspect only relevant files suggested by the chat context.\n" +
			toolGuidance
	}
	return "You are in read-only repository analysis mode.\n" +
		"Analyze the provided chat/discussion context first.\n" +
		"If additional evidence is needed, use targeted read/search inspection only.\n" +
		"Allowed actions: read/search operations, read-only shell commands, read-only MCP/custom tools (including embeddings/search), and web URL fetch/search.\n" +
		fmt.Sprintf("Scope boundary: inspect only files under the project directory %q unless the request is an explicit web fetch.\n", workingDirectory) +
		fmt.Sprintf("Never read files outside the project directory %q.\n", workingDirectory) +
		"Forbidden actions: file create/edit/delete, git history rewriting, branch mutation, or any destructive command.\n" +
		"Do not scan files aimlessly; inspect only relevant files suggested by the chat context.\n" +
		toolGuidance
}
