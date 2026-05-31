package atomicwritetest

import "os"

// good uses os APIs that are not WriteFile — should produce zero diagnostics.
func good() {
	_, _ = os.ReadFile("in.txt")
	_ = os.Remove("stale.txt")
	_ = os.MkdirAll("dir", 0o755)
}
