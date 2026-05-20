//go:build !notrimpath

package cmd

import (
	"debug/buildinfo"
	"fmt"
	"os"
)

func init() {
	checkTrimPath()
}

// checkTrimPath warns if the binary was built without -trimpath.
// Build with -tags notrimpath to skip this check (e.g. for CI fast-builds).
func checkTrimPath() {
	exe, err := os.Executable()
	if err != nil {
		return
	}
	info, err := buildinfo.ReadFile(exe)
	if err != nil {
		return // can't determine — skip silently
	}

	// When -trimpath is used, GoSettings contain "-trimpath" and paths
	// are relative. Without it, GoSettings are empty and the main module
	// path may contain absolute filesystem paths.
	for _, s := range info.Settings {
		if s.Key == "-trimpath" && s.Value == "true" {
			return // good — trimpath was used
		}
	}

	// No -trimpath found. The binary may leak local paths in stack traces.
	// Only warn once per process lifetime.
	fmt.Fprintln(os.Stderr, "warn: built without -trimpath; stack traces may leak local paths. Rebuild with 'make build' or add -trimpath to go build.")
}
