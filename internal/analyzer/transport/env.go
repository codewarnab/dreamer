package transport

import (
	"os"
	"strings"
)

// MergeWithProcessEnv merges extra environment variables with the current
// process environment. Empty values in extra mean "delete the inherited var"
// (used by acpcore to strip variables like CLAUDECODE so nested sessions work).
//
// Returns a slice of "KEY=VALUE" strings suitable for exec.Cmd.Env.
func MergeWithProcessEnv(extra map[string]string) []string {
	// Build set of keys to delete (empty value means delete).
	deletes := make(map[string]struct{})
	for k, v := range extra {
		if v == "" {
			deletes[k] = struct{}{}
		}
	}

	base := os.Environ()
	env := make([]string, 0, len(base)+len(extra))

	// Copy base environment, skipping deleted keys.
	for _, kv := range base {
		eq := strings.IndexByte(kv, '=')
		if eq <= 0 {
			env = append(env, kv)
			continue
		}
		if _, drop := deletes[kv[:eq]]; drop {
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
