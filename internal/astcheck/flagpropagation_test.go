package astcheck

import (
	"testing"

	"golang.org/x/tools/go/analysis/analysistest"
)

// TestFlagpropagation validates that an unread bool parameter in a cmd
// function is flagged (bad.go) and that params used in conditions or
// forwarded to helpers are silent (good.go).
func TestFlagpropagation(t *testing.T) {
	testdata := analysistest.TestData()
	analysistest.Run(t, testdata, flagpropagationAnalyzer, "flagprop/cmd")
}
