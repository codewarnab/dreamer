package astcheck

import (
	"testing"

	"golang.org/x/tools/go/analysis/analysistest"
)

// TestLockskip validates that exported methods on mutex-protected structs
// calling unexported helpers without holding the lock are flagged (bad.go),
// and that correctly locked methods or structs without mutexes are silent
// (good.go).
func TestLockskip(t *testing.T) {
	testdata := analysistest.TestData()
	analysistest.Run(t, testdata, lockskipAnalyzer, "lockskiptest")
}
