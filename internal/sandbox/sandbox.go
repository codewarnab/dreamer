// Package sandbox provides OS-level process sandboxing for child-process
// providers. On Windows, it uses WRITE_RESTRICTED tokens with capability SIDs
// and Job Objects. On Linux, it uses bubblewrap (bwrap) with user+PID
// namespaces and read-only root mount. On other platforms, ModeAuto is a
// no-op and providers are responsible for using policy-only flags.
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
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Mode controls sandbox behavior.
type Mode string

const (
	// ModeAuto uses the OS sandbox if available. If unavailable, callers
	// should keep provider-native policy flags instead.
	ModeAuto Mode = "auto"
	// ModeOn requires the sandbox. Errors out if the OS doesn't support it.
	ModeOn Mode = "true"
	// ModeOff disables sandboxing. The user explicitly accepts the risk.
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
	return Config{
		ProjectDir:   projectDir,
		WritableDirs: []string{os.TempDir(), filepath.Join(home, providerHome)},
		Mode:         mode,
	}, nil
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
			go func() {
				done := make(chan error, 1)
				go func() { done <- cmd.Wait() }()
				select {
				case <-done:
				case <-time.After(10 * time.Second):
					// Process didn't exit after Kill.
					// On Windows, KILL_ON_JOB_CLOSE from the Job Object
					// cleans up when the handle is released. On POSIX,
					// a zombie or D-state child leaks the goroutine and
					// a process slot for the lifetime of the daemon.
					log.Printf("sandbox: %s: process did not exit after Kill within 10s; possible zombie (platform-dependent cleanup)", providerID)
				}
			}()
		}
		return nil, fmt.Errorf("%s: sandbox post-start: %w", providerID, err)
	}
	return cleanup, nil
}
