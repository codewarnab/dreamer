package astcheck

import (
	"testing"

	"golang.org/x/tools/go/analysis/analysistest"
)

func TestErrverbatim(t *testing.T) {
	testdata := analysistest.TestData()
	analysistest.Run(t, testdata, errverbatimAnalyzer, "errverbatimtest")
}
