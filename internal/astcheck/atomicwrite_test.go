package astcheck

import (
	"testing"

	"golang.org/x/tools/go/analysis/analysistest"
)

// TestAtomicwrite validates that os.WriteFile is flagged in non-test code
// (bad.go) and that non-WriteFile os calls are silent (good.go).
func TestAtomicwrite(t *testing.T) {
	testdata := analysistest.TestData()
	analysistest.Run(t, testdata, atomicwriteAnalyzer, "atomicwritetest")
}
