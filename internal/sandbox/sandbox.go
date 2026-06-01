// Package sandbox provides OS-level process sandboxing for child-process
// providers. On Windows, it uses WRITE_RESTRICTED tokens with capability SIDs
// and Job Objects. On Linux, it uses bubblewrap (bwrap) with user+PID
// namespaces and read-only root mount. On macOS, it uses Apple's Seatbelt
// framework via sandbox-exec with (allow default) base policy and selective
// write denial. On other platforms, ModeAuto is a no-op and providers are
// responsible for using policy-only flags.
//
// The sandbox replaces provider-native policy flags (--permission-mode plan,
// --yolo, --sandbox read-only) with kernel-enforced file access control.
// Providers may use unrestricted flags (--dangerously-skip-permissions,
// --yolo, etc.) only when ShouldUseNative returns true. When the native
// sandbox is unavailable or disabled, providers must keep their policy-only
// read-only flags for access control.
package sandbox

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"dreamer/internal/fsutil"
)

// Mode controls sandbox behavior.
type Mode string

const (
	// ModeAuto uses the OS sandbox if available. If unavailable, callers
	// should keep provider-native policy flags instead. This is the
	// "native sandbox" mode — the provider manages its own write protection.
	ModeAuto Mode = "auto"
	// ModeOn requires the sandbox. Errors out if the OS doesn't support it.
	// This is the "controlled" mode — dreamer enforces read-only project,
	// writable output/tmp/config dirs, network isolation, and resource caps.
	ModeOn Mode = "true"
	// ModeOff disables sandboxing. The user explicitly accepts the risk.
	// All other sandbox knobs (network, seccomp, resources) are ignored.
	ModeOff Mode = "false"
)

// Config holds the sandbox configuration for a single child process.
type Config struct {
	// ProjectDir is the directory the child must not write to (Deny-Write ACL).
	ProjectDir string

	// WritableDirs are paths the child may write to (Allow-Write ACL).
	// Typically includes the findings output dir, a per-provider temp dir,
	// and the provider's config home directory.
	WritableDirs []string

	// Mode controls whether the sandbox is applied.
	Mode Mode

	// ProjectWrite adds ProjectDir to the writable list when true.
	// Default false (analysis mode — project dir is read-only).
	ProjectWrite bool

	// Network controls network isolation. "isolated" (default) removes
	// network access; "open" allows full network. Parsed via ParseNetwork.
	Network string

	// Seccomp selects the syscall filter profile on Linux. "off", "minimal"
	// (default, blocks ptrace), or "full" (blocks escalation syscalls).
	// Ignored on non-Linux platforms.
	Seccomp string

	// Resources configures OS resource caps (Linux rlimits).
	Resources ResourceLimits

	// SIDExpiryDays overrides the default SID file expiry (7 days).
	// Only used on Windows. 0 means use DefaultSIDExpiryDays.
	SIDExpiryDays int
}

// ResourceLimits configures OS resource caps for the sandboxed process.
type ResourceLimits struct {
	// MemoryMB is the virtual memory cap in MB (RLIMIT_AS). Min 64. Default 2048.
	MemoryMB int
	// Processes is the max process count (RLIMIT_NPROC). Min 1. Default 64.
	Processes int
	// FDs is the max file descriptors (RLIMIT_NOFILE). Min 16. Default 256.
	FDs int
}

const (
	DefaultMemoryMB = 2048
	// DefaultProcesses is the default RLIMIT_NPROC value. This limit is
	// per-UID (not per-process), so it counts the daemon, web server, all
	// sibling provider children, and the user's other processes. 256
	// provides headroom for typical multi-provider setups.
	DefaultProcesses = 256
	DefaultFDs       = 256
	MinMemoryMB      = 64
	MinProcesses     = 1
	MinFDs           = 16
)

// NetworkMode constants for Config.Network.
const (
	NetworkIsolated = "isolated"
	NetworkOpen     = "open"
)

// SeccompProfile constants for Config.Seccomp.
const (
	SeccompOff     = "off"
	SeccompMinimal = "minimal"
	SeccompFull    = "full"
)

// sandboxKillGrace is how long to wait for a process to exit after Kill
// before escalating to a second Kill.
const sandboxKillGrace = 10 * time.Second

// ParseNetwork converts a raw string into a network mode. Empty string
// maps to NetworkOpen (isolation is opt-in). Invalid values return an error.
func ParseNetwork(raw string) (string, error) {
	switch strings.TrimSpace(strings.ToLower(raw)) {
	case "":
		return NetworkOpen, nil
	case "isolated":
		return NetworkIsolated, nil
	case "open":
		return NetworkOpen, nil
	default:
		return "", fmt.Errorf("invalid sandbox network %q: expected isolated or open", raw)
	}
}

// ParseSeccomp converts a raw string into a seccomp profile name. Empty
// string maps to SeccompMinimal. Invalid values return an error.
func ParseSeccomp(raw string) (string, error) {
	switch strings.TrimSpace(strings.ToLower(raw)) {
	case "":
		return SeccompMinimal, nil
	case "off":
		return SeccompOff, nil
	case "minimal":
		return SeccompMinimal, nil
	case "full":
		return SeccompFull, nil
	default:
		return "", fmt.Errorf("invalid sandbox seccomp %q: expected off, minimal, or full", raw)
	}
}

// Validate checks resource limit minimums and returns an error if any
// value is below its floor. Zero values use defaults.
func (r *ResourceLimits) Validate() error {
	if r.MemoryMB == 0 {
		r.MemoryMB = DefaultMemoryMB
	} else if r.MemoryMB < MinMemoryMB {
		return fmt.Errorf("sandbox: memory_mb %d is below minimum %d", r.MemoryMB, MinMemoryMB)
	}
	if r.Processes == 0 {
		r.Processes = DefaultProcesses
	} else if r.Processes < MinProcesses {
		return fmt.Errorf("sandbox: processes %d is below minimum %d", r.Processes, MinProcesses)
	}
	if r.FDs == 0 {
		r.FDs = DefaultFDs
	} else if r.FDs < MinFDs {
		return fmt.Errorf("sandbox: fds %d is below minimum %d", r.FDs, MinFDs)
	}
	return nil
}

// ShouldUseNative reports whether child providers may rely on the OS sandbox
// for write protection and therefore use unrestricted provider CLI flags.
//
// ModeAuto returns true only on platforms with an implemented sandbox backend.
// ModeOff always returns false because the user explicitly disabled the native
// sandbox and providers must fall back to their own policy flags.
func ShouldUseNative(mode Mode) bool {
	return mode != ModeOff && Available()
}

// ParseMode converts a raw string into a Mode. Empty string maps to ModeAuto.
func ParseMode(raw string) (Mode, error) {
	switch strings.TrimSpace(strings.ToLower(raw)) {
	case "", "auto":
		return ModeAuto, nil
	case "true", "on", "require":
		return ModeOn, nil
	case "false", "off", "disable":
		return ModeOff, nil
	default:
		return "", fmt.Errorf("invalid sandbox mode %q: expected auto, true, or false", raw)
	}
}

// Prepare applies OS-level sandbox constraints to cmd before it is started.
// On Windows, this sets a WRITE_RESTRICTED token with a capability SID and
// applies Allow-Write ACLs on writable dirs. On Linux, this wraps cmd with
// bwrap (read-only root, selective bind mounts, user+PID namespaces). On
// other platforms it is a no-op. Must be called before cmd.Start().
//
// Returns a cleanup function that must be called after cmd.Wait() to release
// kernel handles (restricted token). Returns a no-op cleanup on ModeOff.
func Prepare(cmd *exec.Cmd, cfg Config) (cleanup func(), err error) {
	if cfg.Mode == ModeOff {
		return func() {}, nil
	}
	// Default network to open if empty. Network isolation is opt-in
	// because it breaks network-dependent agents (model backends, APIs).
	// Users must explicitly set network: isolated to enable it.
	if cfg.Network == "" {
		cfg.Network = NetworkOpen
	}
	// Default seccomp to minimal if empty.
	if cfg.Seccomp == "" {
		cfg.Seccomp = SeccompMinimal
	}
	// When ProjectWrite is enabled and ProjectDir is set, add it to the
	// writable list so the kernel allows writes to the project tree.
	if cfg.ProjectWrite && cfg.ProjectDir != "" {
		cfg.WritableDirs = append([]string{cfg.ProjectDir}, cfg.WritableDirs...)
	}
	// Validate resource limits (zero -> defaults, below min -> error).
	if err := cfg.Resources.Validate(); err != nil {
		return nil, err
	}
	if !Available() {
		if cfg.Mode == ModeOn {
			return nil, fmt.Errorf("sandbox: mode true requested but OS sandbox is not available on this platform")
		}
		return func() {}, nil
	}
	if cmd == nil {
		return nil, fmt.Errorf("sandbox: nil command")
	}
	return prepare(cmd, cfg)
}

// PostStart applies post-fork sandbox constraints. On Windows, this assigns
// the process to a Job Object with KILL_ON_JOB_CLOSE. Must be called after
// cmd.Start(). A nil cmd.Process is a no-op.
//
// Returns a cleanup function that must be called after cmd.Wait() to close
// the job handle. Closing the handle triggers KILL_ON_JOB_CLOSE, so the
// caller MUST NOT call cleanup before cmd.Wait() returns. Returns a no-op
// cleanup on ModeOff or nil Process.
func PostStart(cmd *exec.Cmd, cfg Config) (cleanup func(), err error) {
	if cfg.Mode == ModeOff {
		return func() {}, nil
	}
	if !Available() {
		if cfg.Mode == ModeOn {
			return nil, fmt.Errorf("sandbox: mode true requested but OS sandbox is not available on this platform")
		}
		return func() {}, nil
	}
	if cmd == nil || cmd.Process == nil {
		return func() {}, nil
	}
	return postStart(cmd, cfg)
}

// PostStartWithHandle is like PostStart but also returns the raw Job Object
// handle (as uintptr). On non-Windows platforms, returns 0 for the handle.
// The activity monitor uses the handle for IO completion port notifications.
func PostStartWithHandle(cmd *exec.Cmd, cfg Config) (uintptr, func(), error) {
	if cfg.Mode == ModeOff {
		return 0, func() {}, nil
	}
	if !Available() {
		if cfg.Mode == ModeOn {
			return 0, nil, fmt.Errorf("sandbox: mode true requested but OS sandbox is not available on this platform")
		}
		return 0, func() {}, nil
	}
	if cmd == nil || cmd.Process == nil {
		return 0, func() {}, nil
	}
	return postStartWithHandle(cmd, cfg)
}

// BuildConfig returns a Config for a provider with standard writable dirs
// (os.TempDir + ~/<providerHome>) and the given project dir + raw mode string.
// This collapses ~30 lines of boilerplate duplicated across CLI providers.
func BuildConfig(projectDir, providerHome, rawMode string) (Config, error) {
	mode, err := ParseMode(rawMode)
	if err != nil {
		return Config{}, err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return Config{}, fmt.Errorf("sandbox: resolve home dir: %w", err)
	}
	writableDirs := []string{os.TempDir(), filepath.Join(home, providerHome)}
	// Verify no writable dir contains or equals the project dir (and vice
	// versa). Symlink-resolve both sides so symlinked paths can't slip past
	// the textual prefix check.
	resolvedProject, err := fsutil.ResolveSymlinks(projectDir)
	if err != nil {
		return Config{}, fmt.Errorf("sandbox: resolve project dir %q: %w", projectDir, err)
	}
	for _, wdir := range writableDirs {
		absWdir, err := filepath.Abs(wdir)
		if err != nil {
			return Config{}, fmt.Errorf("sandbox: resolve writable dir %q: %w", wdir, err)
		}
		resolvedWdir, err := fsutil.ResolveSymlinks(absWdir)
		if err != nil {
			return Config{}, fmt.Errorf("sandbox: resolve writable dir %q: %w", wdir, err)
		}
		if fsutil.PathWithinRoot(resolvedWdir, resolvedProject) || fsutil.PathWithinRoot(resolvedProject, resolvedWdir) {
			return Config{}, fmt.Errorf("sandbox: writable dir %q overlaps with project dir %q", wdir, projectDir)
		}
	}
	return Config{
		ProjectDir:   projectDir,
		WritableDirs: writableDirs,
		Mode:         mode,
	}, nil
}

// killAndWait kills a process and waits for it to exit, with a double-kill
// escalation if the first kill doesn't take within sandboxKillGrace.
func killAndWait(cmd *exec.Cmd, providerID string) {
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-done:
		return
	case <-time.After(sandboxKillGrace):
		_ = cmd.Process.Kill()
		select {
		case <-done:
		case <-time.After(sandboxKillGrace):
			slog.Error("sandbox: process did not exit after two Kills within 2x grace period; goroutine leaked",
				"provider", providerID,
				"pid", cmd.Process.Pid)
		}
	}
}

// PostStartOrKill calls PostStart; on error, closes stdin/stdout, kills the
// process, and wraps the error. This collapses the identical error-handling
// pattern duplicated across all CLI providers.
func PostStartOrKill(cmd *exec.Cmd, cfg Config, stdin, stdout io.Closer, providerID string) (func(), error) {
	cleanup, err := PostStart(cmd, cfg)
	if err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		if cmd != nil && cmd.Process != nil {
			_ = cmd.Process.Kill()
			go killAndWait(cmd, providerID)
		}
		return nil, fmt.Errorf("%s: sandbox post-start: %w", providerID, err)
	}
	return cleanup, nil
}

// PostStartWithHandleOrKill is like PostStartOrKill but also returns
// the Job Object handle for activity monitoring. The handle is 0 on
// platforms without Job Objects.
func PostStartWithHandleOrKill(cmd *exec.Cmd, cfg Config, stdin, stdout io.Closer, providerID string) (uintptr, func(), error) {
	handle, cleanup, err := PostStartWithHandle(cmd, cfg)
	if err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		if cmd != nil && cmd.Process != nil {
			_ = cmd.Process.Kill()
			go killAndWait(cmd, providerID)
		}
		return 0, nil, fmt.Errorf("%s: sandbox post-start: %w", providerID, err)
	}
	return handle, cleanup, nil
}
