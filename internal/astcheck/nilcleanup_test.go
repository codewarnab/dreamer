package astcheck

import (
	"testing"

	"golang.org/x/tools/go/analysis/analysistest"
)

// TestNilcleanup validates that `return nil, err` from (func(), error)
// functions is flagged (bad.go) and that func(){} stubs, nil-nil success
// returns, and assigned named results are silent (good.go).
func TestNilcleanup(t *testing.T) {
	testdata := analysistest.TestData()
	analysistest.Run(t, testdata, nilcleanupAnalyzer, "nilcleanuptest")
}
