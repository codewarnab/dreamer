//go:build windows

package transport

import (
	"os"
	"strings"
	"testing"
)

func TestMergeWithProcessEnv_CaseInsensitiveDeleteOnWindows(t *testing.T) {
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
	// Windows env vars are case-insensitive; the inherited entry may differ
	// in case from the key being deleted.
	os.Setenv("ClaudeCode", "active")
	os.Setenv("KEEP_ME", "value")

	mergedEnv := MergeWithProcessEnv(map[string]string{"CLAUDECODE": ""})

	for _, kv := range mergedEnv {
		if strings.HasPrefix(strings.ToLower(kv), "claudecode=") {
			t.Errorf("ClaudeCode should have been deleted but got %q", kv)
		}
	}

	found := false
	for _, kv := range mergedEnv {
		if strings.HasPrefix(kv, "KEEP_ME=") {
			found = true
		}
	}
	if !found {
		t.Error("KEEP_ME should be preserved")
	}
}
