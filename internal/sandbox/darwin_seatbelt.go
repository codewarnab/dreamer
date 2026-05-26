//go:build darwin

package sandbox

import (
	"fmt"
	"path/filepath"
	"strings"
)

// sandboxExecPath is the full path to sandbox-exec. Hardcoded to defend
// against PATH injection (matching Codex's approach). If this binary has
// been tampered with, the attacker already has root.
//
// Tests that mutate this var must NOT use t.Parallel().
var sandboxExecPath = "/usr/bin/sandbox-exec"

// maxWritableDirs caps the number of WRITABLE_N params to prevent SBPL
// profile bloat from misconfigured or adversarial configs.
const maxWritableDirs = 64

// validateSBPLPath rejects paths containing SBPL metacharacters that
// would corrupt profile parsing. Legitimate paths with these characters
// are rare but legal on macOS (HFS+/APFS).
func validateSBPLPath(path string) error {
	for _, c := range path {
		switch c {
		case '(', ')', '"', '\\', '\n', '\r', ';', '\x00':
			return fmt.Errorf("sandbox: path %q contains SBPL metacharacter %q", path, string(c))
		}
	}
	return nil
}

// buildSeatbeltProfile dynamically generates the SBPL profile string.
// The static base denies file writes and hard links, then re-allows them
// for parameterized writable directories. Only rules NOT covered by
// (allow default) are load-bearing; defensive rules are annotated.
func buildSeatbeltProfile(writableDirs []string) string {
	var b strings.Builder

	// Header and base policy.
	// THREAT MODEL: This sandbox provides write-protection only, not
	// full containment. Under (allow default), processes can read any
	// file on the system (including secrets), use the network, and
	// execute any binary. Network isolation is a future opt-in enhancement.
	b.WriteString(`(version 1)
(allow default)

;; Deny all file writes (default-deny for mutation)
;; Covers: file-write-data, file-write-create, file-write-unlink,
;; file-write-rename (requires create+unlink), file-write-xattr, etc.
(deny file-write*)

;; Deny hard-link creation — SEPARATE from file-write*.
;; Without this, a process can link() files into the project dir.
;; Chromium and Codex also deny file-link separately.
(deny file-link)

;; Re-allow writes to parameterized writable directories (NOT project dir)
(allow file-write*
`)

	// Dynamic WRITABLE_N entries. Uses a separate counter so indices
	// match buildSandboxArgs (which also deduplicates via a separate idx).
	idx := 0
	for _, dir := range writableDirs {
		if idx >= maxWritableDirs {
			break
		}
		if dir == "" {
			continue
		}
		fmt.Fprintf(&b, "  (subpath (param \"WRITABLE_%d\"))\n", idx)
		idx++
	}

	// Always-writable system paths.
	b.WriteString(`  (subpath "/private/tmp")
  (literal "/dev/null")
  (literal "/dev/stdout")
  (literal "/dev/stderr"))

;; Hard links denied entirely — no re-allow in writable dirs.
;; SBPL's file-link checks the destination path, so re-allowing in
;; writable dirs would let a process hard-link project files into
;; writable dirs and bypass write protection. Chromium and Codex
;; also deny file-link without re-allow.

;; Allow /dev/tty for interactive terminal support
;; NOTE: defensive — (allow default) already covers these.
;; Kept for documentation and forward-compat if base policy changes.
;; SECURITY: Full /dev/tty write enables terminal escape sequence injection
;; (e.g., OSC 52 clipboard exfiltration). Providers use stdin/stdout pipes,
;; not interactive terminals. Tighten to read-only if base policy changes.
(allow file-read* file-write* (literal "/dev/tty"))
(allow file-read* file-write* file-ioctl (literal "/dev/ptmx"))
(allow file-read* file-write* (regex "^/dev/ttys[0-9]+"))
(allow file-ioctl (regex "^/dev/ttys[0-9]+"))

;; Process execution and forking
;; NOTE: defensive — (allow default) already covers these.
(allow process-exec)
(allow process-fork)
(allow signal (target same-sandbox))
(allow process-info* (target same-sandbox))

;; PTY allocation for interactive tools
;; NOTE: defensive — (allow default) already covers this.
(allow pseudo-tty)

;; User preferences (locale, etc.)
;; NOTE: defensive — (allow default) already covers this.
(allow user-preference-read)

;; Sysctl reads needed by Go runtime and common tools
;; NOTE: (allow default) covers sysctl-read, but explicit list serves
;; as documentation of which sysctls are known-good for sandboxed processes.
(allow sysctl-read
  (sysctl-name "hw.activecpu")
  (sysctl-name "hw.memsize")
  (sysctl-name "hw.ncpu")
  (sysctl-name "kern.osrelease")
  (sysctl-name "kern.osversion")
  (sysctl-name "kern.argmax")
  (sysctl-name "kern.maxfilesperproc")
  (sysctl-name "kern.maxproc")
  (sysctl-name "machdep.cpu.brand_string"))
`)

	return b.String()
}

// buildSandboxArgs constructs the full sandbox-exec argument list:
//
//	/usr/bin/sandbox-exec -p <profile> -D KEY=VALUE ... -- <binary> <args...>
//
// Writable dirs are deduplicated after symlink resolution.
func buildSandboxArgs(profile string, writableDirs []string, originalBinary string, originalArgs []string) []string {
	args := []string{sandboxExecPath, "-p", profile}

	// Writable dir params — deduplicate by resolved path.
	seen := make(map[string]bool)
	idx := 0
	for _, wdir := range writableDirs {
		if wdir == "" || idx >= maxWritableDirs {
			continue
		}
		resolved, err := filepath.EvalSymlinks(wdir)
		if err != nil {
			resolved = wdir
		}
		if seen[resolved] {
			continue
		}
		seen[resolved] = true
		args = append(args, "-D", fmt.Sprintf("WRITABLE_%d=%s", idx, resolved))
		idx++
	}

	// Separator + original command.
	args = append(args, "--", originalBinary)
	args = append(args, originalArgs...)
	return args
}
