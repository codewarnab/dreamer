package astcheck

import (
	"testing"

	"golang.org/x/tools/go/analysis/analysistest"
)

// TestLockorder validates that read operations before fsutil.AcquireLock are
// flagged (bad.go) and that reads after the lock or without a lock are silent
// (good.go).
func TestLockorder(t *testing.T) {
	testdata := analysistest.TestData()
	analysistest.Run(t, testdata, lockorderAnalyzer, "lockordertest")
}
