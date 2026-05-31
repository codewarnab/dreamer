package astcheck

import (
	"testing"

	"golang.org/x/tools/go/analysis/analysistest"
)

func TestSettesthome(t *testing.T) {
	testdata := analysistest.TestData()
	analysistest.Run(t, testdata, settesthomeAnalyzer, "settesthometest")
}

func TestCanonicalEnvVarList(t *testing.T) {
	// Verify the canonical list matches CLAUDE.md rule #4 exactly.
	expected := []string{
		"HOME",
		"USERPROFILE",
		"XDG_CONFIG_HOME",
		"CLAUDE_CONFIG_DIR",
		"GEMINI_HOME",
		"OPENCODE_DB",
		"KIRO_CLI_DB",
		"CODEBUFF_CONFIG_DIR",
		"XDG_DATA_HOME",
	}
	if len(canonicalTestEnvVars) != len(expected) {
		t.Fatalf("canonicalTestEnvVars has %d entries, want %d", len(canonicalTestEnvVars), len(expected))
	}
	for i, v := range expected {
		if canonicalTestEnvVars[i] != v {
			t.Errorf("canonicalTestEnvVars[%d] = %q, want %q", i, canonicalTestEnvVars[i], v)
		}
	}
}

func TestIsTestHomeHelper(t *testing.T) {
	tests := []struct {
		name string
		want bool
	}{
		{"setTestHome", true},
		{"setupTestHome", true},
		{"isolateTestEnv", true},
		{"SetTestHome", true},
		{"helper", false},
		{"setUp", false},
	}
	for _, tt := range tests {
		if got := isTestHomeHelper(tt.name); got != tt.want {
			t.Errorf("isTestHomeHelper(%q) = %v, want %v", tt.name, got, tt.want)
		}
	}
}
