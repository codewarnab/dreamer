package astcheck

import (
	"testing"

	"golang.org/x/tools/go/analysis/analysistest"
)

func TestSymlinkresolve(t *testing.T) {
	testdata := analysistest.TestData()
	analysistest.Run(t, testdata, symlinkresolveAnalyzer, "symlinkresolvetest")
}
