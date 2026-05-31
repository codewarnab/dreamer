package astcheck

import (
	"testing"

	"golang.org/x/tools/go/analysis/analysistest"
)

// TestGoroutinerecover validates that a pipeline-running goroutine without a
// leading deferred recover() is flagged (bad.go) and that one with the guard,
// or one that does not run the pipeline, is silent (good.go).
func TestGoroutinerecover(t *testing.T) {
	testdata := analysistest.TestData()
	analysistest.Run(t, testdata, goroutinerecoverAnalyzer, "goroutinerecovertest")
}
