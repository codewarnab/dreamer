package transport

import (
	"os"
	"runtime"
	"strings"
	"testing"
)

func TestMergeWithProcessEnv(t *testing.T) {
	// Save and restore original environment
	origEnv := os.Environ()
	defer func() {
		os.Clearenv()
		for _, kv := range origEnv {
			parts := strings.SplitN(kv, "=", 2)
			if len(parts) == 2 {
				os.Setenv(parts[0], parts[1])
			}
		}
	}()

	tests := []struct {
		name     string
		setup    map[string]string // environment to set before test
		extra    map[string]string // extra to merge
		wantKeys map[string]string // expected key-value pairs
		wantDel  []string          // keys that should be deleted
	}{
		{
			name:  "append new variables",
			setup: map[string]string{"EXISTING": "value1"},
			extra: map[string]string{"NEW_VAR": "value2"},
			wantKeys: map[string]string{
				"EXISTING": "value1",
				"NEW_VAR":  "value2",
			},
		},
		{
			name:  "delete via empty value",
			setup: map[string]string{"TO_DELETE": "value", "KEEP": "value"},
			extra: map[string]string{"TO_DELETE": ""},
			wantKeys: map[string]string{
				"KEEP": "value",
			},
			wantDel: []string{"TO_DELETE"},
		},
		{
			name:  "override existing",
			setup: map[string]string{"VAR": "old"},
			extra: map[string]string{"VAR": "new"},
			wantKeys: map[string]string{
				"VAR": "new",
			},
		},
		{
			name:  "preserve order and multiple operations",
			setup: map[string]string{"A": "1", "B": "2", "C": "3"},
			extra: map[string]string{"B": "", "D": "4"},
			wantKeys: map[string]string{
				"A": "1",
				"C": "3",
				"D": "4",
			},
			wantDel: []string{"B"},
		},
		{
			name:     "empty extra",
			setup:    map[string]string{"VAR": "value"},
			extra:    map[string]string{},
			wantKeys: map[string]string{"VAR": "value"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup environment
			os.Clearenv()
			for k, v := range tt.setup {
				os.Setenv(k, v)
			}

			// Run merge
			mergedEnv := MergeWithProcessEnv(tt.extra)

			// Convert result to map for easier checking
			resultMap := make(map[string]string)
			for _, kv := range mergedEnv {
				parts := strings.SplitN(kv, "=", 2)
				if len(parts) == 2 {
					resultMap[parts[0]] = parts[1]
				}
			}

			// Check expected keys are present with correct values
			for k, wantV := range tt.wantKeys {
				gotV, ok := resultMap[k]
				if !ok {
					t.Errorf("expected key %q not found in result", k)
					continue
				}
				if gotV != wantV {
					t.Errorf("key %q: got value %q, want %q", k, gotV, wantV)
				}
			}

			// Check deleted keys are absent
			for _, k := range tt.wantDel {
				if _, ok := resultMap[k]; ok {
					t.Errorf("key %q should have been deleted but is present", k)
				}
			}
		})
	}
}

func TestMergeWithProcessEnv_EmptyValueSemantics(t *testing.T) {
	// Save and restore original environment
	origEnv := os.Environ()
	defer func() {
		os.Clearenv()
		for _, kv := range origEnv {
			parts := strings.SplitN(kv, "=", 2)
			if len(parts) == 2 {
				os.Setenv(parts[0], parts[1])
			}
		}
	}()

	os.Clearenv()
	os.Setenv("CLAUDECODE", "active")
	os.Setenv("KEEP_ME", "value")

	// Empty value should delete CLAUDECODE
	mergedEnv := MergeWithProcessEnv(map[string]string{
		"CLAUDECODE": "",
		"NEW_VAR":    "new",
	})

	resultMap := make(map[string]string)
	for _, kv := range mergedEnv {
		parts := strings.SplitN(kv, "=", 2)
		if len(parts) == 2 {
			resultMap[parts[0]] = parts[1]
		}
	}

	if _, exists := resultMap["CLAUDECODE"]; exists {
		t.Error("CLAUDECODE should have been deleted")
	}
	if resultMap["KEEP_ME"] != "value" {
		t.Error("KEEP_ME should be preserved")
	}
	if resultMap["NEW_VAR"] != "new" {
		t.Error("NEW_VAR should be added")
	}
}

func TestMergeWithProcessEnv_OverrideLeavesSingleDefinition(t *testing.T) {
	origEnv := os.Environ()
	defer func() {
		os.Clearenv()
		for _, kv := range origEnv {
			parts := strings.SplitN(kv, "=", 2)
			if len(parts) == 2 {
				os.Setenv(parts[0], parts[1])
			}
		}
	}()

	os.Clearenv()
	os.Setenv("ANTHROPIC_BASE_URL", "https://default.example")

	mergedEnv := MergeWithProcessEnv(map[string]string{
		"ANTHROPIC_BASE_URL": "https://override.example",
	})

	count := 0
	for _, kv := range mergedEnv {
		if strings.HasPrefix(kv, "ANTHROPIC_BASE_URL=") {
			count++
			if kv != "ANTHROPIC_BASE_URL=https://override.example" {
				t.Errorf("got %q, want override value", kv)
			}
		}
	}
	if count != 1 {
		t.Errorf("expected exactly 1 ANTHROPIC_BASE_URL definition, got %d", count)
	}
}

func TestNormalizeEnvKey(t *testing.T) {
	if runtime.GOOS == "windows" {
		if normalizeEnvKey("ClaudeCode") != "CLAUDECODE" {
			t.Error("expected case folding to upper on windows")
		}
		return
	}
	if normalizeEnvKey("ClaudeCode") != "ClaudeCode" {
		t.Error("expected keys to be preserved verbatim on non-windows")
	}
}
