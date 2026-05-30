//go:build windows

package fsutil

import "testing"

// Verify ExecPathsMatch handles case differences on Windows.
func TestExecPathsMatch_CaseInsensitive(t *testing.T) {
	a := "C:\\Users\\test\\dreamer.exe"
	b := "c:\\users\\test\\dreamer.exe"
	if !ExecPathsMatch(a, b) {
		t.Errorf("ExecPathsMatch should be case-insensitive on Windows: %q vs %q", a, b)
	}
}
