package acpcore

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"dreamer/internal/analyzer"
	transportutil "dreamer/internal/analyzer/transport"
	"dreamer/internal/sandbox"
)

type transport struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout *bufio.Reader
	encMu  sync.Mutex

	nextID  int64
	pending sync.Map // map[string]chan jsonrpcResponse

	mu          sync.Mutex
	closed      bool
	permHandler permissionHandler
	stderrBuf   strings.Builder

	streams sync.Map // map[string]*sessionStream — keyed by ACP sessionId

	sandboxCleanup func() // closes job handle after process exits
	prepareCleanup func() // closes restricted token after process exits

	stderrPipe   io.ReadCloser  // stored for explicit closure in close()
	goroutines   sync.WaitGroup // tracks drainStderr + readLoop
	cleanupOnce  sync.Once      // ensures resource-release path runs exactly once
	releaseErr   error          // error from the resource-release path
}

type sessionStream struct {
	mu  sync.Mutex
	buf strings.Builder
}

func (s *sessionStream) append(chunk string) {
	if chunk == "" {
		return
	}
	s.mu.Lock()
	s.buf.WriteString(chunk)
	s.mu.Unlock()
}

func (s *sessionStream) text() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.String()
}

func (t *transport) openStream(sessionID string) *sessionStream {
	stream := &sessionStream{}
	t.streams.Store(sessionID, stream)
	return stream
}

func (t *transport) closeStream(sessionID string) {
	t.streams.Delete(sessionID)
}

// handleSessionUpdate routes session/update notifications to the per-session
// text accumulator. Spec: params = {sessionId, update: {sessionUpdate, content?}}.
// We only collect agent_message_chunk text; tool_call / agent_thought_chunk
// are intentionally ignored for analyzer use.
func (t *transport) handleSessionUpdate(raw json.RawMessage) {
	if len(raw) == 0 {
		return
	}
	var params struct {
		SessionID string `json:"sessionId"`
		Update    struct {
			SessionUpdate string `json:"sessionUpdate"`
			Content       struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"update"`
	}
	if err := json.Unmarshal(raw, &params); err != nil {
		return
	}
	if params.SessionID == "" || params.Update.SessionUpdate != "agent_message_chunk" {
		return
	}
	if params.Update.Content.Type != "text" {
		return
	}
	streamRaw, ok := t.streams.Load(params.SessionID)
	if !ok {
		return
	}
	if stream, ok := streamRaw.(*sessionStream); ok {
		stream.append(params.Update.Content.Text)
	}
}

// notify sends a JSON-RPC notification (no id, no response expected).
func (t *transport) notify(_ context.Context, method string, params any) error {
	return t.send(map[string]any{
		"jsonrpc": "2.0",
		"method":  method,
		"params":  params,
	})
}

type jsonrpcResponse struct {
	Result json.RawMessage
	Error  *jsonrpcError
}

type jsonrpcError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *jsonrpcError) Error() string {
	if e == nil {
		return ""
	}
	if len(e.Data) > 0 {
		return fmt.Sprintf("rpc error %d: %s (data: %s)", e.Code, e.Message, string(e.Data))
	}
	return fmt.Sprintf("rpc error %d: %s", e.Code, e.Message)
}

func (t *transport) call(ctx context.Context, method string, params any, onPermission permissionHandler) (json.RawMessage, error) {
	requestID := strings.TrimSpace(fmt.Sprintf("%d", atomic.AddInt64(&t.nextID, 1)))
	respCh := make(chan jsonrpcResponse, 1)
	t.pending.Store(requestID, respCh)
	defer t.pending.Delete(requestID)

	payload := map[string]any{
		"jsonrpc": "2.0",
		"id":      requestID,
		"method":  method,
		"params":  params,
	}
	if err := t.send(payload); err != nil {
		return nil, err
	}

	// Re-arm permission handler scope while this call is in-flight.
	t.mu.Lock()
	if onPermission != nil {
		t.permHandler = onPermission
	}
	t.mu.Unlock()
	defer func() {
		t.mu.Lock()
		t.permHandler = nil
		t.mu.Unlock()
	}()

	select {
	case <-ctx.Done():
		if t.isClosed() {
			return nil, t.closedErrorWithStderr(ctx.Err())
		}
		return nil, ctx.Err()
	case resp := <-respCh:
		if resp.Error != nil {
			if t.isClosed() {
				return nil, t.closedErrorWithStderr(resp.Error)
			}
			return nil, resp.Error
		}
		return resp.Result, nil
	}
}

func (t *transport) send(payload any) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	t.encMu.Lock()
	defer t.encMu.Unlock()
	if t.isClosed() {
		return errors.Join(ErrTransportClosed, errors.New("acpcore: transport closed"))
	}
	if _, err := t.stdin.Write(append(data, '\n')); err != nil {
		return fmt.Errorf("acpcore: write to agent stdin: %w", err)
	}
	return nil
}

func (t *transport) readLoop(initial *bytes.Buffer) {
	defer t.goroutines.Done()
	if initial != nil {
		_ = initial // placeholder for future buffering
	}
	scanner := transportutil.NewScanner(t.stdout)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var envelope rpcEnvelope
		if err := json.Unmarshal([]byte(line), &envelope); err != nil {
			continue
		}
		if envelope.ID != "" && (envelope.Result != nil || envelope.Error != nil) {
			if chRaw, ok := t.pending.Load(envelope.ID); ok {
				ch := chRaw.(chan jsonrpcResponse)
				ch <- jsonrpcResponse{Result: envelope.Result, Error: envelope.Error}
				continue
			}
		}
		if envelope.Method == "session/request_permission" {
			t.handlePermissionRequest(envelope)
			continue
		}
		if envelope.Method == "session/update" {
			t.handleSessionUpdate(envelope.Params)
			continue
		}
	}
	// Child stdout EOF: flip flag and wake in-flight calls.
	t.markClosed()
}

// isClosed reports whether readLoop saw EOF or Close was called.
func (t *transport) isClosed() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.closed
}

// StderrString returns accumulated stderr output. Safe for concurrent use.
func (t *transport) StderrString() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.stderrBuf.String()
}

// closedErrorWithStderr builds the transport-closed error, attaching
// the last maxStderrBytes of stderr when available. Stderr is trimmed
// to prevent secret/URL leakage into dreamer.log.
func (t *transport) closedErrorWithStderr(extra error) error {
	stderr := t.StderrString()
	if stderr != "" {
		const maxStderrBytes = 512
		trimmed := strings.TrimSpace(stderr)
		if len(trimmed) > maxStderrBytes {
			trimmed = "..." + trimmed[len(trimmed)-maxStderrBytes:]
		}
		if extra != nil {
			return fmt.Errorf("%w: %w (stderr: %s)", analyzer.ErrUnavailable, errors.Join(ErrTransportClosed, extra), trimmed)
		}
		return fmt.Errorf("%w: %w (stderr: %s)", analyzer.ErrUnavailable, ErrTransportClosed, trimmed)
	}
	if extra != nil {
		return errors.Join(analyzer.ErrUnavailable, ErrTransportClosed, extra)
	}
	return errors.Join(analyzer.ErrUnavailable, ErrTransportClosed)
}

// markClosed flips the closed flag and wakes pending calls. Idempotent.
func (t *transport) markClosed() {
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return
	}
	t.closed = true
	t.mu.Unlock()

	closedErr := &jsonrpcError{Code: errCodeTransportClosed, Message: ErrTransportClosed.Error()}
	t.pending.Range(func(key, value any) bool {
		if ch, ok := value.(chan jsonrpcResponse); ok {
			select {
			case ch <- jsonrpcResponse{Error: closedErr}:
			default:
			}
		}
		return true
	})
}

func (t *transport) drainStderr(r io.Reader) {
	defer t.goroutines.Done()
	if r == nil {
		return
	}
	buf := make([]byte, 1024)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			t.mu.Lock()
			t.stderrBuf.Write(buf[:n])
			t.mu.Unlock()
		}
		if err != nil {
			return
		}
	}
}

func (t *transport) close() error {
	// Ensure the logical-closed flag is set so new calls are rejected.
	// readLoop may have already flipped this via markClosed; that's fine.
	t.mu.Lock()
	t.closed = true
	t.mu.Unlock()

	// Resource-release path runs exactly once regardless of whether
	// readLoop (markClosed) or provider.Close (this method) observed
	// the exit first.
	t.cleanupOnce.Do(func() {
		_ = t.stdin.Close()
		done := make(chan error, 1)
		go func() { done <- t.cmd.Wait() }()
		var waitErr error
		select {
		// 5s hard-coded: close() implements Close() error with no context
		// parameter, so there's no deadline to propagate. The timeout is
		// intentionally long to give the agent time to flush; Kill() +
		// KILL_ON_JOB_CLOSE from the sandbox always ensures the process exits.
		case <-time.After(5 * time.Second):
			_ = t.cmd.Process.Kill()
			waitErr = <-done
		case waitErr = <-done:
		}
		// Explicitly close pipes so goroutines exit even if process exit
		// didn't trigger EOF (e.g., zombie process before KILL_ON_JOB_CLOSE).
		if t.stderrPipe != nil {
			_ = t.stderrPipe.Close()
		}

		// Wait for drainStderr + readLoop to finish. They exit when their
		// input pipes close (from Kill, explicit Close, or process exit).
		waitCh := make(chan struct{})
		go func() { t.goroutines.Wait(); close(waitCh) }()
		select {
		case <-waitCh:
		case <-time.After(5 * time.Second):
			// Goroutines didn't exit — abandon. KILL_ON_JOB_CLOSE will clean up.
		}

		// Release sandbox kernel handles after the process has exited.
		// Order matters: post-start (Job Object) first, then prepare (token).
		// Closing the job handle is safe here because cmd.Wait() returned.
		if t.sandboxCleanup != nil {
			t.sandboxCleanup()
		}
		if t.prepareCleanup != nil {
			t.prepareCleanup()
		}
		t.releaseErr = waitErr
	})
	return t.releaseErr
}

type rpcEnvelope struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      string          `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *jsonrpcError   `json:"error,omitempty"`
}

// permissionHandler maps ACP permission request params to a decision map.
// Used by session.Run and wired into transport.call via the onPermission arg.
type permissionHandler func(req map[string]any) map[string]any

// dialStdio spawns the ACP agent subprocess over stdio and returns a
// transport wired to its stdin/stdout. Sandbox preparation and post-start
// hooks are applied when sandboxMode is not "off".
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
