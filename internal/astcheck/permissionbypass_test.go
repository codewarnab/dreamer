package astcheck

import (
	"testing"

	"golang.org/x/tools/go/analysis/analysistest"
)

// TestPermissionbypass validates that SandboxProjectWrite and
// SandboxWritableDirs assignments without a sandbox.Available() check are
// flagged (bad.go) and that guarded assignments or read-only patterns are
// silent (good.go).
func TestPermissionbypass(t *testing.T) {
	testdata := analysistest.TestData()
	analysistest.Run(t, testdata, permissionbypassAnalyzer, "permissionbypasstest")
}
