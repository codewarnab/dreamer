package astcheck

import (
	"testing"

	"golang.org/x/tools/go/analysis/analysistest"
)

// TestUnboundedread validates that io.ReadAll on os.Stdin / http.Request.Body
// is flagged (bad.go) and that io.LimitReader-wrapped or in-memory reads are
// silent (good.go).
func TestUnboundedread(t *testing.T) {
	testdata := analysistest.TestData()
	analysistest.Run(t, testdata, unboundedreadAnalyzer, "unboundedreadtest")
}
