package astcheck

import (
	"testing"

	"golang.org/x/tools/go/analysis/analysistest"
)

// TestPathjoin validates that path.* helpers called on filesystem-derived
// arguments are flagged (bad.go) and that URL/slash-path uses are silent
// (good.go).
func TestPathjoin(t *testing.T) {
	testdata := analysistest.TestData()
	analysistest.Run(t, testdata, pathjoinAnalyzer, "pathjointest")
}
