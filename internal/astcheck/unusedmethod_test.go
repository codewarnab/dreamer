package astcheck

import (
	"testing"

	"golang.org/x/tools/go/analysis/analysistest"
)

// TestUnusedmethod validates that unexported funcs with zero callers are
// flagged (bad.go), and that funcs called from test files or satisfying a
// package interface are silent (good.go, good_test.go).
// good_test.go covers the "called only from _test.go" path that depends on
// the runner's dedupTestPackages logic.
func TestUnusedmethod(t *testing.T) {
	testdata := analysistest.TestData()
	analysistest.Run(t, testdata, unusedmethodAnalyzer, "unusedmethodtest")
}
