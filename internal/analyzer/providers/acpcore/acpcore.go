// Package acpcore implements the minimum stdio JSON-RPC 2.0 client needed to
// talk to an Agent Client Protocol (ACP) agent. It exposes a Provider factory
// that adapter packages compose with platform-specific commands
// (claudeacp, copilotacp, geminiacp, kiroacp).
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
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"dreamer/internal/analyzer"
	transportutil "dreamer/internal/analyzer/transport"
	"dreamer/internal/chat"
	"dreamer/internal/errs"
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

func (p *provider) Start(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.started {
		return nil
	}
	t, err := dialStdio(ctx, p.command, p.env, p.sandboxMode)
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

	normalizedRoot, _ := analyzer.NormalizeRootPath(sessionConfig.WorkingDirectory)
	handler := func(req map[string]any) map[string]any {
		permReq := translatePermissionRequest(req)
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
		return "", errors.Join(analyzer.ErrUnavailable, ErrTransportClosed)
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

func dialStdio(ctx context.Context, command []string, env map[string]string, sandboxMode string) (*transport, error) {
	cmd := exec.CommandContext(ctx, command[0], command[1:]...)
	cmd.Env = transportutil.MergeWithProcessEnv(env)

	// Apply OS-level sandbox if configured.
	sbMode, err := sandbox.ParseMode(sandboxMode)
	if err != nil {
		return nil, fmt.Errorf("acpcore: %w", err)
	}
	if sbMode != sandbox.ModeOff {
		// ACP providers don't have a project dir at spawn time — they
		// receive it per-session via session/new. We still apply the
		// restricted token and Job Object for privilege stripping and
		// orphan cleanup, but skip directory ACLs (the policy layer
		// handles per-session path restrictions).
		sbCfg := sandbox.Config{
			ProjectDir:   "",
			WritableDirs: []string{os.TempDir()},
			Mode:         sbMode,
		}
		if err := sandbox.Prepare(cmd, sbCfg); err != nil {
			return nil, fmt.Errorf("acpcore: sandbox prepare: %w", err)
		}
	}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}

	// Apply post-start sandbox (Job Object on Windows).
	if sbMode != sandbox.ModeOff {
		sbCfg := sandbox.Config{Mode: sbMode}
		if err := sandbox.PostStart(cmd, sbCfg); err != nil {
			_ = cmd.Process.Kill()
			return nil, fmt.Errorf("acpcore: sandbox post-start: %w", err)
		}
	}

	t := &transport{
		cmd:    cmd,
		stdin:  stdin,
		stdout: bufio.NewReader(stdout),
	}
	go t.drainStderr(stderr)
	go t.readLoop(nil)
	return t, nil
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
			return nil, errors.Join(analyzer.ErrUnavailable, ErrTransportClosed, ctx.Err())
		}
		return nil, ctx.Err()
	case resp := <-respCh:
		if resp.Error != nil {
			if t.isClosed() {
				return nil, errors.Join(analyzer.ErrUnavailable, ErrTransportClosed, resp.Error)
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

func (t *transport) handlePermissionRequest(envelope rpcEnvelope) {
	t.mu.Lock()
	handler := t.permHandler
	t.mu.Unlock()

	approved, reason := decidePermission(handler, envelope.Params)

	// Re-parse params for option selection. A malformed envelope produced a
	// denial above; selectPermissionOptionID still needs to pick a rejection
	// option from whatever option list (if any) the agent supplied.
	var params map[string]any
	if envelope.Params != nil {
		_ = json.Unmarshal(envelope.Params, &params)
	}
	optionID := selectPermissionOptionID(params, approved)
	var outcome map[string]any
	if optionID != "" {
		outcome = map[string]any{"outcome": "selected", "optionId": optionID}
	} else {
		// No matching option offered — fall back to cancelled so the agent
		// can finish the turn instead of waiting on us.
		outcome = map[string]any{"outcome": "cancelled"}
	}
	if reason != "" {
		outcome["_meta"] = map[string]any{"reason": reason}
	}

	response := map[string]any{
		"jsonrpc": "2.0",
		"id":      envelope.ID,
		"result":  map[string]any{"outcome": outcome},
	}
	_ = t.send(response)
}

// decidePermission produces (approved, reason) for an ACP permission request
// given raw params bytes. Empty or malformed JSON yields a denial (B15)
// rather than the previous silent-approve default. The handler is consulted
// only after a successful unmarshal of non-empty params.
func decidePermission(handler permissionHandler, rawParams json.RawMessage) (bool, string) {
	if len(rawParams) == 0 {
		return false, "empty permission params"
	}
	var params map[string]any
	if err := json.Unmarshal(rawParams, &params); err != nil {
		return false, fmt.Sprintf("malformed permission params: %v", err)
	}
	if handler == nil {
		return true, ""
	}
	decision := handler(params)
	if d, _ := decision["decision"].(string); d == "deny" {
		reason, _ := decision["reason"].(string)
		return false, reason
	}
	return true, ""
}

// selectPermissionOptionID picks an optionId from the params.options[] list
// whose `kind` matches the requested polarity. Falls back to the first option
// matching the polarity, then the first option of any kind.
func selectPermissionOptionID(params map[string]any, approved bool) string {
	rawOpts, _ := params["options"].([]any)
	wantKinds := []string{"allow_once", "allow_always"}
	if !approved {
		wantKinds = []string{"reject_once", "reject_always"}
	}
	var fallbackOptionID string
	for _, raw := range rawOpts {
		opt, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		id, _ := opt["optionId"].(string)
		if id == "" {
			continue
		}
		if fallbackOptionID == "" {
			fallbackOptionID = id
		}
		kind, _ := opt["kind"].(string)
		for _, want := range wantKinds {
			if kind == want {
				return id
			}
		}
	}
	return fallbackOptionID
}

func (t *transport) drainStderr(r io.Reader) {
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
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return nil
	}
	t.closed = true
	t.mu.Unlock()

	_ = t.stdin.Close()
	done := make(chan error, 1)
	go func() { done <- t.cmd.Wait() }()
	select {
	case <-time.After(5 * time.Second):
		_ = t.cmd.Process.Kill()
		<-done
	case err := <-done:
		if err != nil {
			return err
		}
	}
	return nil
}

type rpcEnvelope struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      string          `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *jsonrpcError   `json:"error,omitempty"`
}

func translatePermissionRequest(req map[string]any) analyzer.PermissionRequest {
	out := analyzer.PermissionRequest{}
	if kind, ok := req["kind"].(string); ok {
		switch kind {
		case "read":
			out.Kind = analyzer.PermissionKindRead
		case "url":
			out.Kind = analyzer.PermissionKindURL
		case "shell":
			out.Kind = analyzer.PermissionKindShell
		case "mcp":
			out.Kind = analyzer.PermissionKindMCPTool
		case "custom_tool":
			out.Kind = analyzer.PermissionKindCustomTool
		default:
			out.Kind = analyzer.PermissionKind(kind)
		}
	}
	if path, ok := req["path"].(string); ok {
		out.Path = &path
	}
	if possible, ok := req["possible_paths"].([]any); ok {
		for _, candidatePath := range possible {
			if s, ok := candidatePath.(string); ok {
				out.PossiblePaths = append(out.PossiblePaths, s)
			}
		}
	}
	if readOnly, ok := req["read_only"].(bool); ok {
		out.ReadOnly = &readOnly
	}
	if hasRedir, ok := req["has_write_file_redirection"].(bool); ok {
		out.HasWriteFileRedirection = &hasRedir
	}
	if fullText, ok := req["full_command_text"].(string); ok {
		out.FullCommandText = &fullText
	}
	if commands, ok := req["commands"].([]any); ok {
		for _, command := range commands {
			cmd, ok := command.(map[string]any)
			if !ok {
				continue
			}
			shell := analyzer.ShellCommand{}
			if identifier, ok := cmd["identifier"].(string); ok {
				shell.Identifier = identifier
			}
			if readOnly, ok := cmd["read_only"].(bool); ok {
				shell.ReadOnly = readOnly
			}
			out.Commands = append(out.Commands, shell)
		}
	}
	return out
}

func extractSessionID(rawResponse json.RawMessage) string {
	if len(rawResponse) == 0 {
		return ""
	}
	var parsed map[string]any
	if err := json.Unmarshal(rawResponse, &parsed); err != nil {
		return ""
	}
	if sid, ok := parsed["sessionId"].(string); ok {
		return sid
	}
	if sid, ok := parsed["session_id"].(string); ok {
		return sid
	}
	return ""
}

// pickAvailableModelID returns the modelId from session/new's
// result.models.availableModels[] that matches `preferred`. If `preferred`
// isn't present, tries each fallback in order. Returns ok=false when no
// match is found so the caller skips set_model.
func pickAvailableModelID(rawResponse json.RawMessage, preferred string, fallbacks []string) (string, bool) {
	if preferred == "" {
		return "", false
	}
	var parsed struct {
		Models struct {
			AvailableModels []struct {
				ModelID string `json:"modelId"`
			} `json:"availableModels"`
		} `json:"models"`
	}
	if err := json.Unmarshal(rawResponse, &parsed); err != nil {
		return "", false
	}
	// Build candidate list: preferred first, then fallbacks.
	candidates := make([]string, 0, 1+len(fallbacks))
	candidates = append(candidates, preferred)
	candidates = append(candidates, fallbacks...)
	for _, want := range candidates {
		for _, m := range parsed.Models.AvailableModels {
			if m.ModelID == want {
				return m.ModelID, true
			}
		}
	}
	return "", false
}

// pickReadOnlyModeID picks a modeId that minimizes side effects. Recognizes
// "plan" (claude-code-acp), "read-only" (codex-acp). Returns ok=false when
// the agent doesn't advertise modes or none of the recognized ids appear.
func pickReadOnlyModeID(rawResponse json.RawMessage) (string, bool) {
	var parsed struct {
		Modes struct {
			AvailableModes []struct {
				ID string `json:"id"`
			} `json:"availableModes"`
		} `json:"modes"`
	}
	if err := json.Unmarshal(rawResponse, &parsed); err != nil {
		return "", false
	}
	preferred := []string{"plan", "read-only", "readonly"}
	for _, want := range preferred {
		for _, m := range parsed.Modes.AvailableModes {
			if m.ID == want {
				return m.ID, true
			}
		}
	}
	return "", false
}

func extractStopReason(rawResponse json.RawMessage) string {
	if len(rawResponse) == 0 {
		return ""
	}
	var parsed struct {
		StopReason string `json:"stopReason"`
	}
	if err := json.Unmarshal(rawResponse, &parsed); err != nil {
		return ""
	}
	return parsed.StopReason
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
