package transport

import (
	"os"
	"runtime"
	"strings"
)

// MergeWithProcessEnv merges extra environment variables with the current
// process environment. Empty values in extra mean "delete the inherited var"
// (used by acpcore to strip variables like CLAUDECODE so nested sessions work).
// Non-empty values override the inherited variable.
//
// Returns a slice of "KEY=VALUE" strings suitable for exec.Cmd.Env.
//
// On Windows environment keys are matched case-insensitively by CreateProcess,
// so key comparisons fold case there. Without folding, deleting CLAUDECODE
// would not remove an inherited ClaudeCode=... entry, and overriding an
// inherited Path with PATH=... would leave two definitions in the child env.
func MergeWithProcessEnv(extra map[string]string) []string {
	deletes := make(map[string]struct{}, len(extra))
	extraKeys := make(map[string]struct{}, len(extra))
	for k, v := range extra {
		nk := normalizeEnvKey(k)
		if v == "" {
			deletes[nk] = struct{}{}
		}
		extraKeys[nk] = struct{}{}
	}

	base := os.Environ()
	env := make([]string, 0, len(base)+len(extra))

	// Copy base environment, skipping deleted keys and keys that extra
	// overrides. Dropping overridden entries guarantees the child sees at
	// most one definition per key regardless of getenv lookup order.
	for _, kv := range base {
		eq := strings.IndexByte(kv, '=')
		if eq <= 0 {
			env = append(env, kv)
			continue
		}
		nk := normalizeEnvKey(kv[:eq])
		if _, drop := deletes[nk]; drop {
			continue
		}
		if _, override := extraKeys[nk]; override {
			continue
		}
		env = append(env, kv)
	}

	// Append non-empty extra values.
	for k, v := range extra {
		if v == "" {
			continue
		}
		env = append(env, k+"="+v)
	}

	return env
}

// normalizeEnvKey folds env keys case-insensitively on Windows.
func normalizeEnvKey(key string) string {
	if runtime.GOOS == "windows" {
		return strings.ToUpper(key)
	}
	return key
}
