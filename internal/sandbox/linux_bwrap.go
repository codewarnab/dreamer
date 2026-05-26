//go:build linux

package sandbox

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
)

// bwrapBin is the bubblewrap binary name looked up in PATH.
const bwrapBin = "bwrap"

// tmpfsSize limits /tmp inside the sandbox to prevent runaway processes
// from exhausting host memory.
const tmpfsSize = "512M"

// maxWritableDirs caps the number of --bind flags to prevent argument
// list explosion from a misconfigured or adversarial config.
const maxWritableDirs = 64

// usernsClonePath is the Debian/Ubuntu sysctl that gates unprivileged
// user namespaces. Absent on kernel >= 5.x where user NS are default-on.
var usernsClonePath = "/proc/sys/kernel/unprivileged_user_ns_clone"

// maxUserNamespacesPath caps the number of user namespaces. "0" disables.
var maxUserNamespacesPath = "/proc/sys/user/max_user_namespaces"

// procVersionPath exists as a var so tests can inject a temp file.
var procVersionPath = "/proc/version"

var (
	bwrapPathOnce   sync.Once
	bwrapPathCached string

	userNSOnce   sync.Once
	userNSCached bool

	wsl1Once   sync.Once
	wsl1Cached bool
)

// bwrapPath returns the absolute path to the bwrap binary, or "" if not
// found. Cached via sync.Once for the process lifetime. Tests that need
// to exercise detection logic should call checkUserNamespacesEnabled /
// checkWSL1 (which read var-injectable paths directly) instead.
func bwrapPath() string {
	bwrapPathOnce.Do(func() {
		p, err := exec.LookPath(bwrapBin)
		if err != nil {
			bwrapPathCached = ""
			return
		}
		bwrapPathCached = p
	})
	return bwrapPathCached
}

// userNamespacesEnabled checks whether unprivileged user namespaces are
// available. Returns true if both sysctl files are absent or non-zero
// (the kernel >= 5.x default).
func userNamespacesEnabled() bool {
	userNSOnce.Do(func() {
		userNSCached = checkUserNamespacesEnabled()
	})
	return userNSCached
}

func checkUserNamespacesEnabled() bool {
	// Debian/Ubuntu: /proc/sys/kernel/unprivileged_user_ns_clone
	if data, err := os.ReadFile(usernsClonePath); err == nil {
		if strings.TrimSpace(string(data)) == "0" {
			return false
		}
	}
	// General: /proc/sys/user/max_user_namespaces
	if data, err := os.ReadFile(maxUserNamespacesPath); err == nil {
		if strings.TrimSpace(string(data)) == "0" {
			return false
		}
	}
	return true
}

// isWSL1 detects Windows Subsystem for Linux v1 by reading /proc/version.
// WSL1 reports "Microsoft" without "microsoft-standard" (the WSL2 marker).
// WSL1 cannot create user namespaces even when sysctl reports enabled.
func isWSL1() bool {
	wsl1Once.Do(func() {
		wsl1Cached = checkWSL1()
	})
	return wsl1Cached
}

func checkWSL1() bool {
	data, err := os.ReadFile(procVersionPath)
	if err != nil {
		return false
	}
	content := string(data)
	// WSL2 includes "microsoft-standard" in the version string.
	if strings.Contains(strings.ToLower(content), "microsoft-standard") {
		return false
	}
	// WSL1 has "Microsoft" (capital M) without the WSL2 marker.
	return strings.Contains(content, "Microsoft")
}

// buildBwrapArgs constructs the bwrap argument list for sandboxing a
// child process. The strategy mirrors Codex's proven approach:
//
//	--ro-bind / / (entire host FS read-only) + selective --bind for writable dirs.
func buildBwrapArgs(cfg Config, projectDir string, originalBinary string, originalArgs []string) []string {
	args := []string{
		// Prevent TIOCSTI terminal injection.
		"--new-session",
		// Auto-cleanup descendants when parent dies.
		"--die-with-parent",
		// User namespace — required for unprivileged operation.
		"--unshare-user",
		// PID namespace — kills grandchildren on exit.
		"--unshare-pid",
		// Entire host filesystem read-only (default-deny writes).
		"--ro-bind", "/", "/",
		// Minimal device tree (null, zero, random, urandom, tty).
		"--dev", "/dev",
		// /proc for PID namespace.
		"--proc", "/proc",
		// Writable temp, size-limited.
		"--tmpfs", "/tmp", "--size", tmpfsSize,
		// Redirect /var/tmp into sandbox tmpfs.
		"--symlink", "/tmp", "/var/tmp",
	}

	// Deduplicate and bind-mount writable directories.
	seen := make(map[string]bool)
	for _, wdir := range cfg.WritableDirs {
		if wdir == "" {
			continue
		}
		absDir, err := filepath.Abs(wdir)
		if err != nil {
			continue
		}
		// Deduplicate by resolved path.
		resolved, err := filepath.EvalSymlinks(absDir)
		if err != nil {
			resolved = absDir
		}
		if seen[resolved] {
			continue
		}
		seen[resolved] = true
		args = append(args, "--bind", absDir, absDir)
		if len(seen) >= maxWritableDirs {
			break
		}
	}

	// Pin CWD inside sandbox.
	args = append(args, "--chdir", projectDir)

	// Separator + original command.
	args = append(args, "--", originalBinary)
	args = append(args, originalArgs...)
	return args
}
