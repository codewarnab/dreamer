package settesthometest

import "os"

// setTestHomeComplete clears all canonical env vars — should not be flagged.
func setTestHomeComplete() {
	os.Setenv("HOME", "/tmp/test")
	os.Setenv("USERPROFILE", "/tmp/test")
	os.Setenv("XDG_CONFIG_HOME", "/tmp/test/.config")
	os.Unsetenv("CLAUDE_CONFIG_DIR")
	os.Unsetenv("GEMINI_HOME")
	os.Unsetenv("OPENCODE_DB")
	os.Unsetenv("KIRO_CLI_DB")
	os.Unsetenv("CODEBUFF_CONFIG_DIR")
	os.Unsetenv("XDG_DATA_HOME")
}

// notAHelper doesn't match the setTestHome pattern — should be ignored.
func notAHelper() {
	os.Unsetenv("HOME")
}
