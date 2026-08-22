// npm-shim resolution for the opencode-server auto-start path.
//
// On Windows, npm installs expose global binaries as generated shim
// scripts (`opencode.CMD`, `opencode.ps1`, plus an extensionless bash
// script), not as Win32 executables. Spawning such shims via os/exec goes
// through cmd.exe, which adds argument-quoting fragility and an extra
// process hop. Both shim flavors embed the relative path of the real
// target ("...\node_modules\<pkg>\bin\<name>"), so the real binary can be
// resolved statically before spawning.
package opencodehttp

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// npmShimTargetRe matches the node_modules-relative target path embedded in
// npm-generated shims, e.g. `%dp0%\node_modules\opencode-ai\bin\opencode`
// (cmd) or `$basedir/node_modules/opencode-ai/bin/opencode` (ps1/bash).
// The match is anchored at "node_modules" so shim-specific prefixes
// (%~dp0, $basedir) and drive letters never leak into the capture. The
// capture excludes whitespace, quotes, and cmd metacharacters so quoted
// targets with spaces in the parent dirs terminate correctly.
var npmShimTargetRe = regexp.MustCompile(`(?i)node_modules[\\/]+([^\s"'%*]+)`)

// shimReplacement returns the real executable behind an npm-style shim.
// It reports ok=false when no better binary than the input can be found,
// in which case callers must keep spawning the original entry — behavior
// then matches pre-resolution releases exactly.
//
// Handled inputs:
//   - ".exe" entries: rejected immediately (no file read; already native).
//   - .cmd/.bat/.ps1 shims and extensionless bash shims: parsed for their
//     node_modules-relative target; forward or back slashes both work.
//   - Shims whose parsed target is missing: a conventional fallback
//     (<shimdir>/node_modules/opencode-ai/bin/opencode.exe) is probed.
//   - Unreadable files, directories at candidate paths, partial installs:
//     ok=false; the caller falls back to the original spawn path.
//
// The result is a plain absolute path; callers own its lifecycle. The
// function performs filesystem reads only — it never spawns processes.
func shimReplacement(shimPath string) (string, bool) {
	if strings.EqualFold(filepath.Ext(shimPath), ".exe") {
		return "", false
	}
	data, err := os.ReadFile(shimPath)
	if err != nil {
		return "", false
	}
	shimDir := filepath.Dir(shimPath)

	var candidates []string
	if m := npmShimTargetRe.FindSubmatch(data); m != nil {
		target := filepath.Clean(filepath.FromSlash(string(m[1])))
		parsed := filepath.Join(shimDir, "node_modules", target)
		candidates = append(candidates, parsed)
		if !strings.EqualFold(filepath.Ext(parsed), ".exe") {
			// npm bin entries often omit the extension; the native build
			// usually sits next to the launcher under the same name.
			candidates = append(candidates, parsed+".exe")
		}
	}
	// Conventional layout of the opencode-ai package when the shim does
	// not parse (future npm template changes, minified wrappers).
	fallback := filepath.Join(shimDir, "node_modules", "opencode-ai", "bin", "opencode.exe")
	candidates = append(candidates, fallback)

	for _, cand := range candidates {
		if info, err := os.Stat(cand); err == nil && info.Mode().IsRegular() {
			return cand, true
		}
	}
	return "", false
}
