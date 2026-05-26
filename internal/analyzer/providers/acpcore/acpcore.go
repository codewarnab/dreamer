// Package acpcore implements the minimum stdio JSON-RPC 2.0 client needed to
// talk to an Agent Client Protocol (ACP) agent. It exposes a Provider factory
// that adapter packages compose with platform-specific commands
// (claudeacp, copilotacp, geminiacp, kiroacp).
package acpcore

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"dreamer/internal/analyzer"
	transportutil "dreamer/internal/analyzer/transport"
	"dreamer/internal/chat"
	"dreamer/internal/errs"
	"dreamer/internal/fsutil"
	"dreamer/internal/sandbox"
)

// ErrTransportClosed: ACP child process exited before/during a session.Run.
// Joins with analyzer.ErrUnavailable so callers can detect via errors.Is.
var ErrTransportClosed = errors.New("acp: transport closed")

// errCodeTransportClosed is the JSON-RPC error code used when the transport
// is closed and pending requests must be rejected.
const errCodeTransportClosed = -32000

// Options carries the per-provider configuration carried by ACP adapter packages.
type Options struct {
	// ID is the provider id this acpcore Provider answers to (eg.
	// "claude-acp", "copilot-acp", "codex-acp"). Required.
	ID string

	// Command is the argv to spawn the ACP agent over stdio. Required.
	Command []string

	// Env adds environment variables on top of the parent process env.
	Env map[string]string

	// DefaultModel is the per-provider default model. Applied when
	// SessionConfig.Model is empty. Empty string means no default.
	DefaultModel string

	// ModelFallbacks lists alternative models to try if the preferred
	// model (from SessionConfig.Model or DefaultModel) is not in the
	// agent's availableModels list. Only used by ACP providers.
	ModelFallbacks []string

	// Sandbox holds the resolved sandbox mode ("auto", "true", "false").
	// The ACP provider spawns a long-lived child process; the sandbox is
	// applied at process start time.
	Sandbox string
}

// New returns an analyzer.Provider that drives an ACP agent over stdio.
func New(options Options) (analyzer.Provider, error) {
	if strings.TrimSpace(options.ID) == "" {
		return nil, errors.New("acpcore: ID is required")
	}
	if len(options.Command) == 0 {
		return nil, errors.New("acpcore: Command is required")
	}
	return &provider{
		id:             options.ID,
		command:        append([]string(nil), options.Command...),
		env:            copyStringMap(options.Env),
		defaultModel:   strings.TrimSpace(options.DefaultModel),
		modelFallbacks: append([]string(nil), options.ModelFallbacks...),
		sandboxMode:    options.Sandbox,
	}, nil
}

type provider struct {
	id             string
	command        []string
	env            map[string]string
	defaultModel   string
	modelFallbacks []string
	sandboxMode    string

	mu         sync.Mutex
	transport  *transport
	started    bool
	closed     bool
	initResult json.RawMessage
}

func (p *provider) ID() string { return p.id }

// InspectProvider extracts the stored Command and Env from an acpcore-backed
// analyzer.Provider. Returns ok=false if p is not an acpcore provider.
// Intended for test verification that config values propagated correctly.
func InspectProvider(p analyzer.Provider) (command []string, env map[string]string, ok bool) {
	prov, ok := p.(*provider)
	if !ok {
		return nil, nil, false
	}
	return prov.command, prov.env, true
}

func (p *provider) Start(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.started {
		return nil
	}
	t, err := dialStdio(ctx, p.id, p.command, p.env, p.sandboxMode)
	if err != nil {
		return fmt.Errorf("acpcore: spawn %v: %w", p.command, err)
	}
	p.transport = t

	// ACP spec §initialize: protocolVersion is a required integer; clientCapabilities
	// advertises filesystem reach. We're a read-only analyzer client.
	initResponse, err := t.call(ctx, "initialize", map[string]any{
		"protocolVersion": 1,
		"clientCapabilities": map[string]any{
			"fs": map[string]any{
				"readTextFile":  true,
				"writeTextFile": false,
			},
			"terminal": false,
		},
	}, nil)
	if err != nil {
		_ = t.close()
		return fmt.Errorf("acpcore: initialize: %w", err)
	}
	p.initResult = initResponse
	p.started = true
	return nil
}

func (p *provider) NewSession(ctx context.Context, sessionConfig analyzer.SessionConfig) (analyzer.Session, error) {
	p.mu.Lock()
	t := p.transport
	started := p.started
	p.mu.Unlock()
	if !started || t == nil {
		return nil, errors.New("acpcore: provider not started")
	}

	systemMessage := sessionConfig.SystemMessage
	if strings.TrimSpace(systemMessage) == "" {
		systemMessage = analyzer.BuildReadOnlySystemMessage(sessionConfig.WorkingDirectory, sessionConfig.RunID)
	}
	preferredModel := strings.TrimSpace(sessionConfig.Model)
	if preferredModel == "" {
		preferredModel = p.defaultModel
	}

	normalizedRoot, normErr := fsutil.NormalizeRootPath(sessionConfig.WorkingDirectory)
	handler := func(req map[string]any) map[string]any {
		permReq := translatePermissionRequest(req)
		if normErr != nil {
			return map[string]any{"decision": "deny", "reason": "working directory invalid: " + normErr.Error()}
		}
		decision := analyzer.DecidePermission(permReq, normalizedRoot)
		if decision.Approved {
			return map[string]any{"decision": "allow"}
		}
		return map[string]any{"decision": "deny", "reason": decision.Reason}
	}

	// Defer ACP session/new to Run: each rule needs its own ACP sessionId so
	// transcript history doesn't accumulate across rules and trip the agent's
	// "Prompt is too long" guard.
	return &session{
		transport:      t,
		handler:        handler,
		providerID:     p.id,
		workingDir:     sessionConfig.WorkingDirectory,
		permTarget:     normalizedRoot,
		systemMessage:  systemMessage,
		model:          preferredModel,
		modelFallbacks: p.modelFallbacks,
		runID:          sessionConfig.RunID,
	}, nil
}

func (p *provider) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return nil
	}
	p.closed = true
	if p.transport == nil {
		return nil
	}
	// ACP has no shutdown RPC; closing stdin is the agreed shutdown signal.
	return p.transport.close()
}

type session struct {
	transport      *transport
	handler        permissionHandler
	providerID     string
	workingDir     string
	permTarget     string
	systemMessage  string
	model          string
	modelFallbacks []string
	runID          string
}

func (s *session) Run(ctx context.Context, prompt string, timeout time.Duration) (string, error) {
	if ctx == nil {
		return "", analyzer.ErrNilContext
	}
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	// Fail fast when readLoop already saw the child exit.
	if s.transport.isClosed() {
		return "", s.transport.closedErrorWithStderr(nil)
	}

	// Fresh ACP sessionId per Run keeps each rule's prompt context isolated.
	newResult, err := s.transport.call(ctx, "session/new", map[string]any{
		"cwd":        s.workingDir,
		"mcpServers": []any{},
	}, s.handler)
	if err != nil {
		return "", fmt.Errorf("acpcore: session/new: %w", err)
	}
	sid := extractSessionID(newResult)
	if sid == "" {
		return "", errors.New("acpcore: session/new: missing sessionId in response")
	}
	defer func() {
		// Best-effort tear-down; session/cancel is a notification.
		_ = s.transport.notify(context.Background(), "session/cancel", map[string]any{"sessionId": sid})
	}()

	// Optional agent tuning. Only attempt when the agent advertised the option
	// in session/new — calling set_model with an unsupported id triggers
	// provider-specific 400s (e.g. codex rejects "sonnet"). Best-effort:
	// failure is expected when the agent doesn't support the knob.
	if id, ok := pickAvailableModelID(newResult, s.model, s.modelFallbacks); ok {
		_, _ = s.transport.call(ctx, "session/set_model", map[string]any{
			"sessionId": sid,
			"modelId":   id,
		}, nil)
	}
	if id, ok := pickReadOnlyModeID(newResult); ok {
		if _, err := s.transport.call(ctx, "session/set_mode", map[string]any{
			"sessionId": sid,
			"modeId":    id,
		}, nil); err != nil {
			fmt.Fprintf(os.Stderr, "warning: acp set_mode(%s) failed, read-only boundary not enforced: %v\n", id, err)
		}
	}

	// acpMaxInputBytes caps the per-prompt body fed into ACP agents. claude-code-acp
	// rejects prompts above its model's input window with "Prompt is too long";
	// 200 KB ≈ 50k tokens keeps Opus runs well under their per-turn budget too.
	const acpMaxInputBytes = 200_000

	body := chat.PrependMarker(prompt, s.runID)
	if s.systemMessage != "" {
		body = s.systemMessage + "\n\n" + body
	}
	body = transportutil.CapInputBytes(body, acpMaxInputBytes, "\n\n[transcript truncated to fit agent input cap]\n")

	stream := s.transport.openStream(sid)
	defer s.transport.closeStream(sid)

	params := map[string]any{
		"sessionId": sid,
		"prompt": []any{
			map[string]any{"type": "text", "text": body},
		},
	}
	promptResponse, err := s.transport.call(ctx, "session/prompt", params, s.handler)
	if err != nil {
		wrapped := fmt.Errorf("acpcore: session/prompt: %w", err)
		if transportutil.IsRateLimitMessage(err.Error()) {
			return "", errs.RateLimit(s.providerID, "session/prompt", 0, wrapped)
		}
		return "", wrapped
	}
	stopReason := extractStopReason(promptResponse)
	text := stream.text()
	if text == "" && stopReason != "" && stopReason != "end_turn" {
		return "", fmt.Errorf("acpcore: session/prompt stopReason=%q with no agent_message_chunk content", stopReason)
	}
	return text, nil
}

func (s *session) Close() error { return nil }

type permissionHandler func(req map[string]any) map[string]any

func dialStdio(ctx context.Context, providerID string, command []string, env map[string]string, sandboxMode string) (*transport, error) {
	cmd := exec.CommandContext(ctx, command[0], command[1:]...)
	cmd.Env = transportutil.MergeWithProcessEnv(env)

	// Apply OS-level sandbox if configured.
	sbMode, err := sandbox.ParseMode(sandboxMode)
	if err != nil {
		return nil, fmt.Errorf("acpcore: %w", err)
	}
	// ACP providers don't know the project dir at spawn time (received
	// per-session via session/new). The kernel write-block does NOT
	// cover ACP project files — per-session path restrictions rely
	// entirely on the policy layer (permission handler). Under agents
	// running with --yolo / --dangerously-skip-permissions, the policy
	// layer is bypassed, leaving project files unprotected from writes.
	// Privilege stripping (WRITE_RESTRICTED token) and orphan cleanup
	// (Job Object KILL_ON_JOB_CLOSE) are still applied.
	writableDirs, err := acpWritableDirs(providerID, env)
	if err != nil {
		return nil, err
	}
	sbCfg := sandbox.Config{
		ProjectDir:   "",
		WritableDirs: writableDirs,
		Mode:         sbMode,
	}
	var prepareCleanup func()
	if sbMode != sandbox.ModeOff {
		var err error
		prepareCleanup, err = sandbox.Prepare(cmd, sbCfg)
		if err != nil {
			return nil, fmt.Errorf("acpcore: sandbox prepare: %w", err)
		}
	}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		if prepareCleanup != nil {
			prepareCleanup()
		}
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		if prepareCleanup != nil {
			prepareCleanup()
		}
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		if prepareCleanup != nil {
			prepareCleanup()
		}
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		_ = stderr.Close()
		if prepareCleanup != nil {
			prepareCleanup()
		}
		return nil, err
	}

	// Apply post-start sandbox (Job Object on Windows).
	// Store cleanup on transport so it's called when transport.close() runs.
	var postCleanup func()
	if sbMode != sandbox.ModeOff {
		var err error
		postCleanup, err = sandbox.PostStart(cmd, sbCfg)
		if err != nil {
			_ = stdin.Close()
			_ = stdout.Close()
			_ = stderr.Close()
			_ = cmd.Process.Kill()
			go func() { _ = cmd.Wait() }()
			if prepareCleanup != nil {
				prepareCleanup()
			}
			return nil, fmt.Errorf("acpcore: sandbox post-start: %w", err)
		}
	}

	t := &transport{
		cmd:            cmd,
		stdin:          stdin,
		stdout:         bufio.NewReader(stdout),
		sandboxCleanup: postCleanup,
		prepareCleanup: prepareCleanup,
		stderrPipe:     stderr,
	}
	t.goroutines.Add(2)
	go t.drainStderr(stderr)
	go t.readLoop(nil)
	return t, nil
}

// acpWritableDirs returns provider-specific state directories that ACP agents
// need for auth, sessions, and caches while running under a restricted token.
func acpWritableDirs(providerID string, env map[string]string) ([]string, error) {
	dirs := []string{os.TempDir()}
	switch analyzer.ProviderID(providerID) {
	case analyzer.ProviderClaudeACP:
		dir, err := resolveACPConfigDir(env, "CLAUDE_CONFIG_DIR", ".claude")
		if err != nil {
			return nil, fmt.Errorf("acpcore: resolve claude config dir for sandbox writable paths: %w", err)
		}
		dirs = append(dirs, dir)
	case analyzer.ProviderGeminiACP:
		dir, err := resolveACPConfigDir(env, "GEMINI_HOME", ".gemini")
		if err != nil {
			return nil, fmt.Errorf("acpcore: resolve gemini home for sandbox writable paths: %w", err)
		}
		dirs = append(dirs, dir)
	case analyzer.ProviderCodexACP:
		dir, err := resolveACPConfigDir(env, "CODEX_HOME", ".codex")
		if err != nil {
			return nil, fmt.Errorf("acpcore: resolve codex home for sandbox writable paths: %w", err)
		}
		dirs = append(dirs, dir)
	}
	return dirs, nil
}

// resolveACPConfigDir mirrors each provider's discovery environment variable
// before falling back to the conventional directory under the user's home.
func resolveACPConfigDir(env map[string]string, envName, fallbackName string) (string, error) {
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

func copyStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
