//go:build darwin

// macOS sandbox using sandbox-exec(1) and Seatbelt (SBPL) profiles.
//
// DEPRECATION: sandbox-exec is deprecated by Apple and may be removed in
// a future macOS version. If that happens, this backend must fall back to
// provider-native sandboxing (ModeAuto). The --seatbelt-profile flag used
// here is not part of the public sandbox-exec API.

package sandbox

import (
	"fmt"
	"os"
	"strings"
)

// sandboxExecLocator returns the path to sandbox-exec, or "" if not found.
// Tests can replace this variable to simulate missing sandbox-exec.
// Hardcoded to defend against PATH injection (matching Codex's approach).
// If this binary has been tampered with, the attacker already has root.
var sandboxExecLocator = func() string {
	_, err := os.Stat("/usr/bin/sandbox-exec")
	if err != nil {
		return ""
	}
	return "/usr/bin/sandbox-exec"
}

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
// for parameterized writable directories.
//
// Callers MUST pass a pre-resolved, deduplicated, capped slice from
// resolveWritableDirs. This function trusts its input and performs no
// dedup, skip, or cap logic of its own.
func buildSeatbeltProfile(cfg Config, writableDirs []string) string {
	var b strings.Builder

	// Header and base policy.
	// THREAT MODEL: This sandbox provides write-protection only, not
	// full containment. Under (allow default), processes can read any
	// file on the system (including secrets) and execute any binary.
	// Network isolation is applied when cfg.Network != NetworkOpen.
	b.WriteString(`(version 1)
(allow default)

;; Network isolation: deny all network operations when not explicitly open.
`)
	if cfg.Network != NetworkOpen {
		b.WriteString(`(deny network*)
(deny network-outbound)
(deny network-inbound)
`)
	}

	b.WriteString(`


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

	// Dynamic WRITABLE_N entries. Index matches buildSandboxArgs because
	// both iterate the same canonical slice in the same order.
	for i := range writableDirs {
		fmt.Fprintf(&b, "  (subpath (param \"WRITABLE_%d\"))\n", i)
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
`)

	return b.String()
}

// buildSandboxArgs constructs the full sandbox-exec argument list:
//
//	/usr/bin/sandbox-exec -p <profile> -D KEY=VALUE ... -- <binary> <args...>
//
// Callers MUST pass a pre-resolved, deduplicated, capped slice from
// resolveWritableDirs. This function trusts its input and performs no
// dedup, skip, or cap logic of its own.
func buildSandboxArgs(profile string, writableDirs []string, originalBinary string, originalArgs []string) []string {
	args := []string{sandboxExecLocator(), "-p", profile}

	for i, wdir := range writableDirs {
		args = append(args, "-D", fmt.Sprintf("WRITABLE_%d=%s", i, wdir))
	}

	// Separator + original command.
	args = append(args, "--", originalBinary)
	args = append(args, originalArgs...)
	return args
}
