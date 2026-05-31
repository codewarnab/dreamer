package astcheck

import (
	"testing"

	"golang.org/x/tools/go/analysis/analysistest"
)

// TestOverlaybool validates that overlay:"merge" fields typed bool are flagged
// (bad.go) and that *bool / untagged / non-bool fields are silent (good.go).
func TestOverlaybool(t *testing.T) {
	testdata := analysistest.TestData()
	analysistest.Run(t, testdata, overlayboolAnalyzer, "overlaybooltest")
}
