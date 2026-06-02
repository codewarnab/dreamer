package astcheck

import (
	"testing"

	"golang.org/x/tools/go/analysis/analysistest"
)

func TestImplicitstatus(t *testing.T) {
	testdata := analysistest.TestData()
	analysistest.Run(t, testdata, implicitstatusAnalyzer, "implicitstatustest")
}
