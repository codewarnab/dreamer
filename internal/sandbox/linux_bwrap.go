//go:build linux

package sandbox

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

// bwrapBin is the bubblewrap binary name looked up in PATH.
const bwrapBin = "bwrap"

// tmpfsSizeBytes limits /tmp inside the sandbox to prevent runaway
// processes from exhausting host memory. bwrap's --size flag requires
// a raw byte count parsed by strtoull — suffixes like "M" or "G" are
// rejected. 512 MiB = 536870912.
const tmpfsSizeBytes = 512 * 1024 * 1024

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

// tmpPaths lists paths that are already covered by --tmpfs /tmp or
// --symlink /tmp /var/tmp inside the sandbox. A --bind on any of
// these would override the tmpfs/symlink and expose the host filesystem.
var tmpPaths = []string{"/tmp", "/var/tmp"}

var (
	bwrapMu      sync.Mutex
	bwrapCached  string
	bwrapChecked bool
)

// bwrapPath returns the absolute path to the bwrap binary, or "" if not
// found. The result is cached after the first call.
// Call ResetBwrapPathCache() in tests that need to re-detect bwrap
// after modifying PATH or installing/uninstalling bwrap.
func bwrapPath() string {
	bwrapMu.Lock()
	defer bwrapMu.Unlock()
	if !bwrapChecked {
		p, err := exec.LookPath(bwrapBin)
		if err == nil {
			bwrapCached = p
		}
		bwrapChecked = true
	}
	return bwrapCached
}

// ResetBwrapPathCache clears the cached bwrap path so the next call to
// bwrapPath() re-runs exec.LookPath. Only needed in tests that modify
// PATH or install/uninstall bwrap between test cases.
func ResetBwrapPathCache() {
	bwrapMu.Lock()
	defer bwrapMu.Unlock()
	bwrapCached = ""
	bwrapChecked = false
	bwrapRlimitCached = rlimitUnknown
}

// rlimitSupport tracks whether the installed bwrap supports --rlimit.
type rlimitSupport int

const (
	rlimitUnknown rlimitSupport = iota
	rlimitSupported
	rlimitUnsupported
)

var bwrapRlimitCached = rlimitUnknown

// bwrapSupportsRlimit reports whether the installed bwrap supports the
// --rlimit flag (added in bubblewrap 0.12.0). The result is cached.
func bwrapSupportsRlimit() rlimitSupport {
	bwrapMu.Lock()
	defer bwrapMu.Unlock()
	if bwrapRlimitCached != rlimitUnknown {
		return bwrapRlimitCached
	}
	path := bwrapPathLocked()
	if path == "" {
		bwrapRlimitCached = rlimitUnsupported
		return bwrapRlimitCached
	}
	out, err := exec.Command(path, "--version").Output()
	if err != nil {
		bwrapRlimitCached = rlimitUnsupported
		return bwrapRlimitCached
	}
	// Output format: "bubblewrap X.Y.Z" or "bubblewrap X.Y.Z+git..."
	ver := strings.TrimSpace(string(out))
	ver = strings.TrimPrefix(ver, "bubblewrap ")
	bwrapRlimitCached = rlimitUnsupported
	if compareSemver(ver, "0.12.0") >= 0 {
		bwrapRlimitCached = rlimitSupported
	}
	return bwrapRlimitCached
}

// bwrapPathLocked returns bwrapCached without acquiring bwrapMu.
// Caller must hold bwrapMu.
func bwrapPathLocked() string {
	if !bwrapChecked {
		p, err := exec.LookPath(bwrapBin)
		if err == nil {
			bwrapCached = p
		}
		bwrapChecked = true
	}
	return bwrapCached
}

// compareSemver compares two dotted version strings (e.g. "0.12.0" vs "0.11.9").
// Returns -1, 0, or 1. Non-numeric suffixes (e.g. "+git") are stripped before
// comparing the numeric portion.
func compareSemver(a, b string) int {
	// Strip non-numeric suffixes like "+git" or "-rc1".
	stripSuffix := func(s string) string {
		for i, c := range s {
			if c != '.' && (c < '0' || c > '9') {
				return s[:i]
			}
		}
		return s
	}
	a = stripSuffix(a)
	b = stripSuffix(b)

	aParts := strings.Split(a, ".")
	bParts := strings.Split(b, ".")
	max := len(aParts)
	if len(bParts) > max {
		max = len(bParts)
	}
	for i := 0; i < max; i++ {
		var aNum, bNum int
		if i < len(aParts) {
			aNum, _ = strconv.Atoi(aParts[i])
		}
		if i < len(bParts) {
			bNum, _ = strconv.Atoi(bParts[i])
		}
		if aNum < bNum {
			return -1
		}
		if aNum > bNum {
			return 1
		}
	}
	return 0
}

// userNamespacesEnabled checks whether unprivileged user namespaces are
// available. Returns true if both sysctl files are absent or non-zero
// (the kernel >= 5.x default).
func userNamespacesEnabled() bool {
	return checkUserNamespacesEnabled()
}

func checkUserNamespacesEnabled() bool {
	// Debian/Ubuntu: /proc/sys/kernel/unprivileged_user_ns_clone
	if data, err := readFileVar(usernsClonePath); err == nil {
		if strings.TrimSpace(data) == "0" {
			return false
		}
	}
	// General: /proc/sys/user/max_user_namespaces
	if data, err := readFileVar(maxUserNamespacesPath); err == nil {
		if strings.TrimSpace(data) == "0" {
			return false
		}
	}
	return true
}

// isWSL1 detects Windows Subsystem for Linux v1 by reading /proc/version.
// WSL1 reports "Microsoft" without "microsoft-standard" (the WSL2 marker).
// WSL1 cannot create user namespaces even when sysctl reports enabled.
func isWSL1() bool {
	return checkWSL1()
}

func checkWSL1() bool {
	data, err := readFileVar(procVersionPath)
	if err != nil {
		return false
	}
	// WSL2 includes "microsoft-standard" in the version string.
	if strings.Contains(strings.ToLower(data), "microsoft-standard") {
		return false
	}
	// WSL1 has "Microsoft" (capital M) without the WSL2 marker.
	return strings.Contains(data, "Microsoft")
}

// readFileVar is a thin wrapper over os.ReadFile to allow test injection
// via the exported package-level vars (usernsClonePath, etc.).
func readFileVar(path string) (string, error) {
	data, err := readFile(path)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// readFile is the actual file reader; separated so tests can stub it
// without touching os.ReadFile directly. Tests that mutate this var
// must NOT use t.Parallel() — it shares process-global state.
var readFile = func(path string) ([]byte, error) {
	return os.ReadFile(path)
}

// buildBwrapArgs constructs the bwrap argument list for sandboxing a
// child process. resolvedDirs must already be absolute, symlink-resolved
// paths (resolved in prepare()). The builder skips any path already
// covered by --tmpfs /tmp or --symlink /var/tmp.
//
// bwrap applies setup operations in order:
//   - --size sets next_size_arg consumed by the following --tmpfs
//   - --tmpfs /tmp creates an in-memory filesystem at /tmp
//   - --symlink /tmp /var/tmp redirects /var/tmp into the tmpfs
//   - --bind /foo /foo would replace whatever was at /foo
//
// So --size must precede --tmpfs, and we must NOT emit a --bind for
// paths under /tmp or /var/tmp (which would replace the tmpfs with
// the host path).
func buildBwrapArgs(cfg Config, projectDir string, resolvedDirs []string, originalBinary string, originalArgs []string, seccompFD uintptr, supportsRlimit bool) []string {
	// Normalize empty Network to NetworkOpen so zero-value configs behave
	// the same as Prepare() (isolation is opt-in — see Prepare in sandbox.go).
	network := cfg.Network
	if network == "" {
		network = NetworkOpen
	}
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
		//
		// KNOWN LIMITATION — OS scheduler manipulation (persistence) is NOT blocked.
		//
		// The read-only root mount prevents file writes but does NOT prevent
		// the sandboxed child from executing scheduling binaries that exist
		// on the host filesystem. A prompt-injection attack could cause the
		// child to run:
		//
		//   crontab -e        # install a cron job
		//   at now + 1 hour   # schedule a one-shot command
		//   systemctl --user enable malicious.timer  # (if systemd user session active)
		//
		// These succeed because bwrap does not filter execve — the child
		// can execute any binary visible through the read-only root mount.
		// The seccomp profile (minimal/full) only blocks ptrace and privilege
		// escalation syscalls, not process creation.
		//
		// Mitigation plan — shadow bind (not yet implemented):
		//
		//   --ro-bind /dev/null /usr/bin/crontab
		//   --ro-bind /dev/null /usr/bin/at
		//   --ro-bind /dev/null /usr/bin/atq
		//   --ro-bind /dev/null /usr/bin/atrm
		//   --ro-bind /dev/null /usr/bin/batch
		//
		// This replaces scheduling binaries with /dev/null inside the sandbox,
		// so any attempt to execute them reads an empty file and exits
		// immediately. The child's view of the filesystem is modified before
		// execve, so there's no race window. This is the same technique
		// used by Flatpak and Snap for binary blocking.
		//
		// systemctl is intentionally NOT in the shadow list — some provider
		// CLIs may legitimately need it. If blocked, use a targeted
		// allowlist approach instead.
		//
		// Seccomp cannot do path-based exec filtering because BPF cannot
		// safely dereference userspace pointers (the filename argument to
		// execve). Landlock LSM (kernel >= 5.13) could provide path-based
		// EXECUTE deny as an alternative to shadow binding.
		"--ro-bind", "/", "/",
		// Minimal device tree (null, zero, random, urandom, tty).
		"--dev", "/dev",
		// /proc for PID namespace.
		"--proc", "/proc",
		// Writable temp, size-limited. --size must precede --tmpfs
		// (bwrap consumes next_size_arg from --size when it hits --tmpfs).
		"--size", strconv.Itoa(tmpfsSizeBytes), "--tmpfs", "/tmp",
	}
	args = appendVarTmpArgs(args)

	// Network isolation: remove network stack when not explicitly open.
	if network != NetworkOpen {
		args = append(args, "--unshare-net")
	}

	// Resource limits (rlimits). The --rlimit flag was added in bubblewrap
	// 0.12.0; older versions reject it with "Unknown option". Only emit
	// when the installed bwrap supports it.
	if supportsRlimit {
		if cfg.Resources.MemoryMB > 0 {
			args = append(args, "--rlimit", "RLIMIT_AS", fmt.Sprintf("%d", int64(cfg.Resources.MemoryMB)*1024*1024))
		}
		if cfg.Resources.Processes > 0 {
			args = append(args, "--rlimit", "RLIMIT_NPROC", fmt.Sprintf("%d", cfg.Resources.Processes))
		}
		if cfg.Resources.FDs > 0 {
			args = append(args, "--rlimit", "RLIMIT_NOFILE", fmt.Sprintf("%d", cfg.Resources.FDs))
		}
	}

	// Seccomp BPF filter. FD is passed as-is; bwrap reads it after fork.
	if seccompFD > 0 {
		args = append(args, "--seccomp", fmt.Sprintf("%d", seccompFD))
	}

	// Deduplicate and bind-mount writable directories. resolvedDirs
	// are already absolute + EvalSymlinks'd by prepare().
	seen := make(map[string]bool)
	for _, resolved := range resolvedDirs {
		if isTmpfsPath(resolved) {
			continue
		}
		if seen[resolved] {
			continue
		}
		seen[resolved] = true
		args = append(args, "--bind", resolved, resolved)
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

// varTmpPath is overridable in tests so the symlink vs real-directory
// detection can be exercised on any host.
var varTmpPath = "/var/tmp"

// appendVarTmpArgs redirects /var/tmp into the sandbox. On standard distros
// /var/tmp is a symlink to /tmp, so a --symlink recreates it inside the
// sandbox. Some containers ship /var/tmp as a REAL directory, where bwrap
// fails with "Can't make symlink at /var/tmp: destination exists and is not
// a symlink" — in that case mount a fresh tmpfs over it instead.
func appendVarTmpArgs(args []string) []string {
	if info, err := os.Lstat(varTmpPath); err == nil && info.Mode()&os.ModeSymlink == 0 {
		return append(args, "--tmpfs", "/var/tmp")
	}
	return append(args, "--symlink", "/tmp", "/var/tmp")
}

// isTmpfsPath reports whether p is a path already covered by the tmpfs
// at /tmp or the /var/tmp -> /tmp symlink. A --bind on such a path
// would override the tmpfs and expose the host filesystem.
func isTmpfsPath(p string) bool {
	for _, tp := range tmpPaths {
		if p == tp || strings.HasPrefix(p, tp+"/") {
			return true
		}
	}
	return false
}

// validateWritableDir checks that a writable directory is safe to mount
// inside the sandbox. It rejects paths that are "/" (entire host FS),
// ancestors of or equal to projectDir, or outside the allowed roots.
// When projectWrite is true, the dir == projectDir check is skipped
// (the user explicitly opted into writable project mode).
//
// The ".." check is defense-in-depth: callers should already resolve
// via filepath.Abs (which normalizes ".."), but we reject it explicitly
// in case a caller passes an unresolved path.
func validateWritableDir(dir, projectDir string, allowedRoots []string, projectWrite bool) error {
	if dir == "/" {
		return fmt.Errorf("sandbox: refusing to mount entire host filesystem as writable")
	}
	// Defense-in-depth: reject unresolved ".." components.
	if strings.Contains(dir, "..") {
		return fmt.Errorf("sandbox: writable dir %q contains '..'", dir)
	}
	// The dir must not be projectDir itself or an ancestor of projectDir.
	// Either would make the project tree writable, defeating the
	// deny-write ACL that the sandbox is designed to enforce.
	// Skip when projectWrite is true — user explicitly opted in.
	if !projectWrite && (dir == projectDir || isAncestor(dir, projectDir)) {
		return fmt.Errorf("sandbox: writable dir %q overlaps with project dir %s (project must remain read-only)", dir, projectDir)
	}
	// The dir must be under one of the allowed roots.
	for _, root := range allowedRoots {
		if isSubpath(dir, root) {
			return nil
		}
	}
	return fmt.Errorf("sandbox: writable dir %q is not under any allowed root (project dir, home, or temp)", dir)
}

// isSubpath reports whether child is equal to or under parent.
// Both arguments are cleaned via filepath.Clean to normalize trailing
// slashes and redundant separators before comparison.
func isSubpath(child, parent string) bool {
	child = filepath.Clean(child)
	parent = filepath.Clean(parent)
	if child == parent {
		return true
	}
	return strings.HasPrefix(child, parent+"/")
}

// isAncestor reports whether ancestor is a strict ancestor of path.
// Both arguments are cleaned via filepath.Clean to normalize trailing
// slashes and redundant separators before comparison.
func isAncestor(ancestor, path string) bool {
	ancestor = filepath.Clean(ancestor)
	path = filepath.Clean(path)
	if ancestor == path {
		return false
	}
	return strings.HasPrefix(path, ancestor+"/")
}

// resolveAndValidateWritableDirs resolves each writable dir to absolute
// + EvalSymlinks, validates containment, and creates it if needed.
// Returns the resolved paths for buildBwrapArgs.
func resolveAndValidateWritableDirs(writableDirs []string, projectDir string, projectWrite bool) ([]string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		homeDir = ""
	}
	if homeDir != "" {
		if resolved, err := filepath.EvalSymlinks(homeDir); err == nil {
			homeDir = resolved
		}
	}

	tmpDir, err := filepath.Abs(os.TempDir())
	if err != nil {
		tmpDir = "/tmp"
	}
	if resolved, err := filepath.EvalSymlinks(tmpDir); err == nil {
		tmpDir = resolved
	}

	allowedRoots := []string{tmpDir, projectDir}
	if homeDir != "" {
		allowedRoots = append(allowedRoots, homeDir)
	}

	var resolved []string
	for _, wdir := range writableDirs {
		if wdir == "" {
			continue
		}
		absDir, err := filepath.Abs(wdir)
		if err != nil {
			return nil, fmt.Errorf("sandbox: resolve writable dir %q: %w", wdir, err)
		}
		resolvedDir, err := filepath.EvalSymlinks(absDir)
		if err != nil {
			resolvedDir = absDir
		}
		if err := validateWritableDir(resolvedDir, projectDir, allowedRoots, projectWrite); err != nil {
			return nil, err
		}
		if err := os.MkdirAll(resolvedDir, 0o755); err != nil {
			return nil, fmt.Errorf("sandbox: create writable dir %s: %w", resolvedDir, err)
		}
		resolved = append(resolved, resolvedDir)
	}
	return resolved, nil
}
