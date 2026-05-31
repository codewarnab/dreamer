package astcheck

import (
	"testing"

	"golang.org/x/tools/go/analysis/analysistest"
)

// TestNodirectlog validates that log.Print/Printf/Fatal/etc are flagged in
// non-test files (bad.go) and NOT flagged in test files (nodirectlogtest_test.go).
// analysistest.Run fails if any diagnostic appears on a line without `// want`.
func TestNodirectlog(t *testing.T) {
	testdata := analysistest.TestData()
	analysistest.Run(t, testdata, nodirectlogAnalyzer, "nodirectlogtest")
}
