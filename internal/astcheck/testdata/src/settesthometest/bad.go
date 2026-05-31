package settesthometest

import "os"

// setTestHomeMissing clears only some env vars — should be flagged.
func setTestHomeMissing() { // want "setTestHome helper .* missing env vars:.*"
	os.Unsetenv("HOME")
	os.Unsetenv("USERPROFILE")
	os.Unsetenv("XDG_CONFIG_HOME")
	// Missing: CLAUDE_CONFIG_DIR, GEMINI_HOME, OPENCODE_DB, KIRO_CLI_DB,
	// CODEBUFF_CONFIG_DIR, XDG_DATA_HOME
}
