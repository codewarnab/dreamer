package astcheck

import (
	"testing"

	"golang.org/x/tools/go/analysis/analysistest"
)

// TestStringconst validates that string literals repeated >= 3 times across
// >= 2 files are flagged (in bad.go + good.go) and that const-backed,
// single-use, format, path, and short strings are silent (good.go).
func TestStringconst(t *testing.T) {
	testdata := analysistest.TestData()
	analysistest.Run(t, testdata, stringconstAnalyzer, "stringconsttest")
}
