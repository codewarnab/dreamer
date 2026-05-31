package astcheck

import (
	"testing"

	"golang.org/x/tools/go/analysis/analysistest"
)

// TestDiscardederr validates that dropping a curated callee's error into `_`
// is flagged (bad.go) and that checked errors, non-curated callees, and
// deferred Close discards are silent (good.go).
//
// NOTE: this test exercises the curated allowlist in discardederr.go. It will
// fail until that list (the TODO(human) in discardedErrCallees) is populated
// with the callees referenced by bad.go.
func TestDiscardederr(t *testing.T) {
	testdata := analysistest.TestData()
	analysistest.Run(t, testdata, discardederrAnalyzer, "discardederrtest")
}
