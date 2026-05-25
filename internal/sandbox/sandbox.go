// Package sandbox provides OS-level process sandboxing for child-process
// providers. On Windows, it uses WRITE_RESTRICTED tokens with capability SIDs
// and Job Objects. On other platforms, it is a no-op (providers run
// unsandboxed — the caller is responsible for policy-only flags).
//
// The sandbox replaces provider-native policy flags (--permission-mode plan,
// --yolo, --sandbox read-only) with kernel-enforced file access control.
// Providers use unrestricted flags (--dangerously-skip-permissions, --yolo,
// etc.) so the model gets full tool access. On Windows, the kernel blocks
// writes to the project directory (except for ACP providers where the
// project dir is not known at spawn time — see acpcore.go). On non-Windows
// platforms, sandbox functions are no-ops; callers must rely on provider
// policy flags for access control.
package sandbox

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Mode controls sandbox behavior.
type Mode string

const (
	// ModeAuto uses the OS sandbox if available. Errors out if unavailable.
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
// applies Allow-Write ACLs on writable dirs. On other platforms it is a
// no-op. Must be called before cmd.Start().
//
// Returns a cleanup function that must be called after cmd.Wait() to release
// kernel handles (restricted token). Returns a no-op cleanup on ModeOff.
func Prepare(cmd *exec.Cmd, cfg Config) (cleanup func(), err error) {
	if cfg.Mode == ModeOff {
		return func() {}, nil
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
	if cfg.Mode == ModeOff || cmd.Process == nil {
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
		_ = cmd.Process.Kill()
		return nil, fmt.Errorf("%s: sandbox post-start: %w", providerID, err)
	}
	return cleanup, nil
}
