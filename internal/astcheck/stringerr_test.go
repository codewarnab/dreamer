package astcheck

import (
	"testing"

	"golang.org/x/tools/go/analysis/analysistest"
)

func TestStringerr(t *testing.T) {
	testdata := analysistest.TestData()
	analysistest.Run(t, testdata, stringerrAnalyzer, "stringerrtest")
}
