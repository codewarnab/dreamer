package astcheck

import (
	"testing"

	"golang.org/x/tools/go/analysis/analysistest"
)

// TestLatemutation validates that field writes after a by-value pass with no
// later use are flagged (bad.go) and that legitimate late writes — later
// reuse, loops, closures, pointer aliasing — are silent (good.go).
func TestLatemutation(t *testing.T) {
	testdata := analysistest.TestData()
	analysistest.Run(t, testdata, latemutationAnalyzer, "latemutationtest")
}
