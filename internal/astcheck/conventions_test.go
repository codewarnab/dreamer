package astcheck

import (
	"testing"

	"golang.org/x/tools/go/analysis/analysistest"
)

func TestNodirectlog(t *testing.T) {
	testdata := analysistest.TestData()
	analysistest.Run(t, testdata, nodirectlogAnalyzer, "nodirectlogtest")
}

func TestNodirectlogGood(t *testing.T) {
	// good.go has no `// want` markers, so analysistest.Run will fail
	// if any diagnostic is reported there (false positive).
	testdata := analysistest.TestData()
	analysistest.Run(t, testdata, nodirectlogAnalyzer, "nodirectlogtest")
}
