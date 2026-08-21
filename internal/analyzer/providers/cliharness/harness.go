// Package cliharness provides the shared lifecycle for CLI-based analyzer
// providers. Four providers (claudecli, openclaudecli, geminicli, codexcli)
// duplicate the same 5-step process: LookPath, build args, inject flags,
// spawn with sandbox, drain stream-json. This package extracts that common
// logic so each provider only supplies its Spec (binary, defaults, error
// constructors) and a readStreamJSON callback.
package cliharness

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"dreamer/internal/analyzer"
	"dreamer/internal/analyzer/providers/flagutil"
	"dreamer/internal/analyzer/transport"
	"dreamer/internal/chat"
	"dreamer/internal/config"
	"dreamer/internal/errs"
	"dreamer/internal/logging"
	"dreamer/internal/sandbox"
	"dreamer/internal/sysinfo"
)

// Spec captures the provider-specific configuration that drives the shared
// lifecycle. Each CLI provider defines one as a package-level var.
type Spec struct {
	// ID is the provider constant (e.g. "claude-cli").
	ID string
	// ErrPrefix is the human-readable prefix for error messages.
	ErrPrefix string
	// DefaultCommand returns the generated command for the current sandbox state.
	DefaultCommand func(useNativeSandbox bool) []string
	// StartErr wraps the LookPath failure. Receives the binary name.
	StartErr func(binary string, err error) error
	// CmdStartErr wraps cmd.Start failures.
	CmdStartErr func(err error) error
	// WorkingDirFlag is the CLI flag for the working directory (e.g. "--add-dir",
	// "--cd"). Empty string means no flag is appended.
	WorkingDirFlag string
	// ConfigDir returns the config directory path for sandbox writable paths.
	ConfigDir func(env map[string]string) (string, error)
	// ParseErrFirst controls error precedence in Run: when true, parseErr
	// is checked before waitErr.
	ParseErrFirst bool
	// ReadStreamJSON parses NDJSON events from stdout and returns the final
	// assistant text.
	ReadStreamJSON func(r io.Reader) (string, error)
	// PreStdinWrite optionally transforms the prompt body before writing to
	// stdin. Nil means no transform.
	PreStdinWrite func(body string) string
	// InjectPhase2 optionally mutates the command slice for Phase 2 mode.
	InjectPhase2 func(command []string, sc analyzer.SessionConfig, useNative bool) ([]string, error)
	// ResolveModel resolves the model from session config + provider options.
	ResolveModel func(sc analyzer.SessionConfig, model, defaultModel string) string
	// SkipPhase2Validation disables Phase2.Validate() in NewSession.
	SkipPhase2Validation bool
	// SupportsMaxTurns indicates the provider CLI accepts the --max-turns flag
	// in headless (-p/--print) mode. True for claude-cli and openclaude-cli;
	// false for gemini-cli (uses settings.json) and codex-cli (Rust binary
	// rejects unknown flags hard).
	SupportsMaxTurns bool
}

// Options is the per-provider configuration carried from YAML config.
type Options struct {
	Command             []string
	Env                 map[string]string
	Model               string
	DefaultModel        string
	SandboxProjectWrite bool
	SandboxWritableDirs []string
	SandboxNetwork      string
	SandboxSeccomp      string
	SandboxResources    sandbox.ResourceLimits
	// MaxTurns caps the number of agentic loop iterations per session.
	// 0 = use config.DefaultMaxTurns; -1 = no cap.
	MaxTurns int
	// Background indicates the session is for a background job. When true,
	// the default command uses a permissive permission mode (e.g.
	// --permission-mode auto instead of plan) so the provider can execute
	// commands, not just plan them.
	Background bool
}

// Provider holds per-instance state shared across sessions for one provider.
type Provider struct {
	Spec               *Spec
	Options            Options
	Command            []string
	UsesDefaultCommand bool
	Background         bool // true when created for a background job
}

// NewProvider creates a Provider from options and spec, resolving the default
// command if no custom command is set.
func NewProvider(options Options, spec *Spec) *Provider {
	command := append([]string(nil), options.Command...)
	usesDefault := len(command) == 0
	if len(command) == 0 {
		command = spec.DefaultCommand(sandbox.Available())
	}
	return &Provider{
		Spec:               spec,
		Options:            options,
		Command:            command,
		UsesDefaultCommand: usesDefault,
		Background:         options.Background,
	}
}

// Session is the shared session struct created by NewSession.
type Session struct {
	command       []string
	env           map[string]string
	workingDir    string
	systemMsg     string
	runID         string
	sandboxCfg    sandbox.Config
	spec          *Spec
	postStartHook func(jobHandle uintptr)
}

// Command returns the resolved command slice for testing.
func (s *Session) Command() []string { return s.command }

// SandboxConfig returns the sandbox configuration for testing.
func (s *Session) SandboxConfig() sandbox.Config { return s.sandboxCfg }

// LookPath checks that the binary is on PATH. Under WSL it also warns when
// the binary resolves to a Windows installation through the interop mounts
// (/mnt/<drive>/...): the Windows CLI will then operate on Windows paths, not
// the WSL workspace, producing silently wrong analyses.
func LookPath(command []string) error {
	resolved, err := exec.LookPath(command[0])
	if err != nil {
		return err
	}
	if sysinfo.IsWSL() && sysinfo.IsWindowsInteropPath(resolved) {
		slog.Warn("provider CLI resolves to a Windows installation via WSL interop; "+
			"it may not see the WSL workspace — install the provider inside WSL",
			logging.String("binary", command[0]),
			logging.String("resolved", resolved),
		)
	}
	return nil
}

// CommandForMode returns the command for the current sandbox state.
func CommandForMode(p *Provider, useNativeSandbox bool) []string {
	if p.UsesDefaultCommand ||
		flagutil.EqualArgs(p.Command, p.Spec.DefaultCommand(true)) ||
		flagutil.EqualArgs(p.Command, p.Spec.DefaultCommand(false)) {
		return p.Spec.DefaultCommand(useNativeSandbox)
	}
	return append([]string(nil), p.Command...)
}

// NewSession creates a Session from the given session config.
func NewSession(p *Provider, sessionConfig analyzer.SessionConfig) (*Session, error) {
	spec := p.Spec
	wd := strings.TrimSpace(sessionConfig.WorkingDirectory)
	if wd == "" {
		return nil, fmt.Errorf("%s: SessionConfig.WorkingDirectory is required", spec.ErrPrefix)
	}
	if !spec.SkipPhase2Validation && sessionConfig.Phase2 != nil {
		if err := sessionConfig.Phase2.Validate(); err != nil {
			return nil, fmt.Errorf("%s: phase 2 config: %w", spec.ErrPrefix, err)
		}
	}
	sbMode, err := sandbox.ParseMode(sessionConfig.Sandbox)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", spec.ErrPrefix, err)
	}
	useNative := sandbox.ShouldUseNative(sbMode)
	command := CommandForMode(p, useNative)
	// Background jobs need execution capability. When not using the native
	// sandbox, the default command uses a read-only permission mode (plan/
	// read-only) that blocks all execution. Switch to a permissive mode so
	// the provider can actually run commands.
	if p.Background && !useNative {
		command = adjustForBackground(command, spec.ID)
	}
	if spec.WorkingDirFlag != "" {
		command = append(command, spec.WorkingDirFlag, wd)
	}
	model := spec.ResolveModel(sessionConfig, p.Options.Model, p.Options.DefaultModel)
	if model != "" {
		command = append(command, "--model", model)
	}
	// Inject --max-turns to cap agentic loop iterations and prevent runaway
	// analysis from burning tokens until the job timeout fires (default 8h).
	// Only injected for providers whose CLI supports the flag (claude-cli and
	// openclaude-cli). Gemini CLI uses settings.json maxSessionTurns; Codex CLI
	// (Rust) rejects unknown flags with a hard error.
	if spec.SupportsMaxTurns {
		maxTurns := p.Options.MaxTurns
		if maxTurns == 0 {
			maxTurns = config.DefaultMaxTurns
		}
		// maxTurns == -1 means the operator explicitly disabled the cap; skip.
		if maxTurns > 0 {
			command = append(command, "--max-turns", strconv.Itoa(maxTurns))
		}
	}
	if spec.InjectPhase2 != nil {
		command, err = spec.InjectPhase2(command, sessionConfig, useNative)
		if err != nil {
			return nil, fmt.Errorf("%s: phase 2 config: %w", spec.ErrPrefix, err)
		}
	}
	configDir, err := spec.ConfigDir(p.Options.Env)
	if err != nil {
		return nil, fmt.Errorf("%s: resolve home dir for sandbox writable paths: %w", spec.ErrPrefix, err)
	}
	writable := append([]string{os.TempDir(), configDir}, append([]string(nil), p.Options.SandboxWritableDirs...)...)
	// Node.js processes (openclaude-cli, claude-cli, gemini-cli, codex-cli)
	// write to npm-cache and local temp dirs that aren't in the standard
	// writable list. Without these, the Windows sandbox crashes the process
	// with STATUS_HEAP_CORRUPTION.
	writable = append(writable, nodeJSExtraDirs(command)...)
	return &Session{
		command:    command,
		env:        p.Options.Env,
		workingDir: wd,
		systemMsg:  strings.TrimSpace(sessionConfig.SystemMessage),
		runID:      sessionConfig.RunID,
		sandboxCfg: sandbox.Config{
			ProjectDir:   wd,
			WritableDirs: writable,
			Mode:         sbMode,
			ProjectWrite: p.Options.SandboxProjectWrite,
			Network:      p.Options.SandboxNetwork,
			Seccomp:      p.Options.SandboxSeccomp,
			Resources: sandbox.ResourceLimits{
				MemoryMB:  p.Options.SandboxResources.MemoryMB,
				Processes: p.Options.SandboxResources.Processes,
				FDs:       p.Options.SandboxResources.FDs,
			},
		},
		spec:          spec,
		postStartHook: sessionConfig.PostStartHook,
	}, nil
}

// Run executes the CLI process and returns the final assistant text.
func (s *Session) Run(ctx context.Context, prompt string, timeout time.Duration) (string, error) {
	if ctx == nil {
		return "", analyzer.ErrNilContext
	}
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	cmd := exec.CommandContext(ctx, s.command[0], s.command[1:]...)
	cmd.Dir = s.workingDir
	cmd.Env = transport.MergeWithProcessEnv(s.env)
	setNoWindow(cmd)

	prepareCleanup, err := sandbox.Prepare(cmd, s.sandboxCfg)
	if err != nil {
		return "", fmt.Errorf("%s: sandbox prepare: %w", s.spec.ErrPrefix, err)
	}
	defer prepareCleanup()

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return "", fmt.Errorf("%s: open stdin: %w", s.spec.ErrPrefix, err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return "", fmt.Errorf("%s: open stdout: %w", s.spec.ErrPrefix, err)
	}
	stderrBuf := new(transport.SafeBuffer)
	cmd.Stderr = stderrBuf

	if err := cmd.Start(); err != nil {
		return "", s.spec.CmdStartErr(err)
	}

	jobHandle, postCleanup, err := sandbox.PostStartWithHandleOrKill(cmd, s.sandboxCfg, stdin, stdout, s.spec.ID)
	if err != nil {
		return "", err
	}
	defer postCleanup()

	if s.postStartHook != nil {
		s.postStartHook(jobHandle)
	}

	go func() {
		defer stdin.Close()
		body := chat.PrependMarker(prompt, s.runID)
		if s.systemMsg != "" {
			body = s.systemMsg + "\n\n" + body
		}
		if s.spec.PreStdinWrite != nil {
			body = s.spec.PreStdinWrite(body)
		}
		if _, writeErr := io.WriteString(stdin, body); writeErr != nil {
			stderrBuf.WriteString(fmt.Sprintf("[stdin write failed: %v]", writeErr))
		}
	}()

	final, parseErr := s.spec.ReadStreamJSON(stdout)
	waitErr := cmd.Wait()
	stderrStr := strings.TrimSpace(stderrBuf.String())

	if s.spec.ParseErrFirst {
		return handleErrorsParseFirst(s.spec, parseErr, waitErr, stderrStr, final)
	}
	return handleErrorsWaitFirst(s.spec, parseErr, waitErr, stderrStr, final)
}

// handleErrorsWaitFirst checks waitErr first (claude, gemini pattern).
// Returns the parsed output alongside the error when available, so the
// caller can persist partial output to the run log even on failure.
func handleErrorsWaitFirst(spec *Spec, parseErr, waitErr error, stderr, final string) (string, error) {
	if waitErr != nil {
		err := fmt.Errorf("%s: process exited: %w (stderr: %s)", spec.ErrPrefix, waitErr, stderr)
		if transport.IsRateLimitMessage(stderr) {
			return final, errs.RateLimit(spec.ID, "session.run", 0, err)
		}
		return final, err
	}
	if parseErr != nil {
		err := fmt.Errorf("%s: parse stream-json: %w (stderr: %s)", spec.ErrPrefix, parseErr, stderr)
		if transport.IsRateLimitMessage(parseErr.Error()) || transport.IsRateLimitMessage(stderr) {
			return final, errs.RateLimit(spec.ID, "session.run", 0, err)
		}
		return final, err
	}
	if final == "" {
		return "", fmt.Errorf("%s: no assistant content emitted (stderr: %s)", spec.ErrPrefix, stderr)
	}
	return final, nil
}

// handleErrorsParseFirst checks parseErr first (openclaude, codex pattern).
// Returns the parsed output alongside the error when available, so the
// caller can persist partial output to the run log even on failure.
func handleErrorsParseFirst(spec *Spec, parseErr, waitErr error, stderr, final string) (string, error) {
	if parseErr != nil {
		err := fmt.Errorf("%s: %w (stderr: %s)", spec.ErrPrefix, parseErr, stderr)
		if transport.IsRateLimitMessage(parseErr.Error()) || transport.IsRateLimitMessage(stderr) {
			return final, errs.RateLimit(spec.ID, "session.run", 0, err)
		}
		return final, err
	}
	if waitErr != nil {
		err := fmt.Errorf("%s: process exited: %w (stderr: %s)", spec.ErrPrefix, waitErr, stderr)
		if transport.IsRateLimitMessage(stderr) || transport.IsRateLimitMessage(waitErr.Error()) {
			return final, errs.RateLimit(spec.ID, "session.run", 0, err)
		}
		return final, err
	}
	if final == "" {
		return "", fmt.Errorf("%s: no assistant content emitted (stderr: %s)", spec.ErrPrefix, stderr)
	}
	return final, nil
}

func (s *Session) Close() error { return nil }

// ResolveConfigDir mirrors the config directory that the subprocess will use.
func ResolveConfigDir(env map[string]string, envName, fallbackName string) (string, error) {
	if envValue := strings.TrimSpace(env[envName]); envValue != "" {
		return envValue, nil
	}
	if envValue := strings.TrimSpace(os.Getenv(envName)); envValue != "" {
		return envValue, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, fallbackName), nil
}

// ResolveModel2Tier resolves model from session config → defaultModel.
func ResolveModel2Tier(sc analyzer.SessionConfig, _, defaultModel string) string {
	model := strings.TrimSpace(sc.Model)
	if model == "" {
		model = strings.TrimSpace(defaultModel)
	}
	return model
}

// ResolveModel3Tier resolves model from session config → model → defaultModel.
func ResolveModel3Tier(sc analyzer.SessionConfig, model, defaultModel string) string {
	m := strings.TrimSpace(sc.Model)
	if m == "" {
		m = strings.TrimSpace(model)
	}
	if m == "" {
		m = strings.TrimSpace(defaultModel)
	}
	return m
}

// InjectMCPFlags injects MCP flags for Phase2ModeMCP.
func InjectMCPFlags(command []string, sc analyzer.SessionConfig, useNative bool) ([]string, error) {
	if sc.Phase2.Mode() != analyzer.Phase2ModeMCP {
		return command, nil
	}
	return flagutil.InjectMCPFlags(command,
		sc.Phase2.MCP.ToolNames,
		sc.Phase2.MCP.ConfigFilePath,
		useNative,
		nil,
	)
}

// InjectCLITools injects --tools for Phase2ModeCLI.
func InjectCLITools(command []string, sc analyzer.SessionConfig, _ bool) ([]string, error) {
	if sc.Phase2.Mode() != analyzer.Phase2ModeCLI {
		return command, nil
	}
	return append(command, "--tools", strings.Join(flagutil.Phase2MCPTools, ",")), nil
}

// StartErrPlain returns a plain fmt.Errorf for Start failures.
func StartErrPlain(prefix string) func(string, error) error {
	return func(binary string, err error) error {
		return fmt.Errorf("%s binary %q not found in PATH; install and authenticate", prefix, binary)
	}
}

// StartErrNotInstalled returns an errs.NotInstalled for Start failures.
func StartErrNotInstalled(hint string) func(string, error) error {
	return func(binary string, err error) error {
		return errs.NotInstalled(binary, "start", hint, err)
	}
}

// CmdStartErrPlain returns a plain fmt.Errorf for cmd.Start failures.
func CmdStartErrPlain(prefix string) func(error) error {
	return func(err error) error {
		return fmt.Errorf("%s: start: %w", prefix, err)
	}
}

// CmdStartErrUnavailable returns an errs.ProviderUnavailable for cmd.Start failures.
func CmdStartErrUnavailable(id string) func(error) error {
	return func(err error) error {
		return errs.ProviderUnavailable(id, "session.run", err)
	}
}

// ConfigDirFromEnv returns a ConfigDir func that uses env override + fallback.
func ConfigDirFromEnv(envName, fallbackName string) func(map[string]string) (string, error) {
	return func(env map[string]string) (string, error) {
		return ResolveConfigDir(env, envName, fallbackName)
	}
}

// ConfigDirHardcoded returns a ConfigDir func that uses a hardcoded subdirectory.
func ConfigDirHardcoded(subdir string) func(map[string]string) (string, error) {
	return func(_ map[string]string) (string, error) {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, subdir), nil
	}
}

// adjustForBackground switches read-only permission flags to permissive
// ones so background jobs can execute commands. Each provider CLI uses a
// different flag for this:
//   - Claude/OpenClaude: --permission-mode plan → auto (skipped if bypassPermissions)
//   - Gemini: --approval-mode plan → yolo (skipped if --yolo present)
//   - Codex: --sandbox read-only → full-auto (skipped if already full-auto)
func adjustForBackground(command []string, providerID string) []string {
	switch providerID {
	case "claude-cli", "openclaude-cli":
		if flagutil.HasFlagValue(command, "--permission-mode", "bypassPermissions") {
			return command
		}
		return flagutil.ReplaceFlag(command, "--permission-mode", "plan", "auto")
	case "gemini-cli":
		if flagutil.HasFlag(command, "--yolo") {
			return command
		}
		return flagutil.ReplaceFlag(command, "--approval-mode", "plan", "yolo")
	case "codex-cli":
		if flagutil.HasFlagValue(command, "--sandbox", "full-auto") {
			return command
		}
		return flagutil.ReplaceFlag(command, "--sandbox", "read-only", "full-auto")
	default:
		return command
	}
}

// nodeJSExtraDirs returns Node.js-specific writable directories when the
// command is a Node.js-based CLI. These dirs are required for the Windows
// sandbox to not crash Node.js processes with STATUS_HEAP_CORRUPTION.
//
// NOTE: Not all npm-distributed CLIs are Node.js processes. Codex CLI
// was rewritten in Rust (the npm package is a thin JS shim wrapping a
// native binary). Kiro CLI is also a native binary. Only add Node.js
// extra dirs for CLIs that actually run on the Node.js runtime.
func nodeJSExtraDirs(command []string) []string {
	if len(command) == 0 {
		return nil
	}
	binary := filepath.Base(command[0])
	// Strip .exe on Windows for consistent matching.
	if runtime.GOOS == "windows" && len(binary) > 4 && binary[len(binary)-4:] == ".exe" {
		binary = binary[:len(binary)-4]
	}
	switch binary {
	case "openclaude", "claude", "gemini", "copilot", "codebuff", "node":
		return sandbox.NodeJSExtraDirs()
	}
	return nil
}
