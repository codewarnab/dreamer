package acpcore

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"runtime"
	"strings"
	"testing"
	"time"

	"dreamer/internal/analyzer"
)

// B15: malformed permission request JSON must deny rather than silently
// approving (the previous handler discarded the unmarshal error and fell
// through to approved=true).
func TestDecidePermissionDeniesOnMalformedJSON(t *testing.T) {
	handler := func(p map[string]any) map[string]any { return map[string]any{} }
	approved, reason := decidePermission(handler, json.RawMessage(`{not-json`))
	if approved {
		t.Fatalf("malformed params must be denied")
	}
	if reason == "" {
		t.Fatalf("denial must carry a reason")
	}
}

func TestDecidePermissionAppliesHandlerDeny(t *testing.T) {
	handler := func(p map[string]any) map[string]any {
		return map[string]any{"decision": "deny", "reason": "outside root"}
	}
	approved, reason := decidePermission(handler, json.RawMessage(`{"kind":"read"}`))
	if approved {
		t.Fatalf("handler deny must propagate")
	}
	if reason != "outside root" {
		t.Fatalf("reason = %q, want %q", reason, "outside root")
	}
}

func TestDecidePermissionDeniesOnEmptyParams(t *testing.T) {
	handler := func(p map[string]any) map[string]any { return map[string]any{} }
	approved, reason := decidePermission(handler, nil)
	if approved {
		t.Fatalf("empty params must be denied")
	}
	if reason == "" {
		t.Fatalf("denial must carry a reason")
	}
}

func TestDecidePermissionDefaultsApprovedOnEmptyDecision(t *testing.T) {
	handler := func(p map[string]any) map[string]any { return map[string]any{} }
	approved, reason := decidePermission(handler, json.RawMessage(`{"kind":"read"}`))
	if !approved {
		t.Fatalf("empty decision must default to approve")
	}
	if reason != "" {
		t.Fatalf("reason = %q, want empty", reason)
	}
}

func TestDecidePermissionNilHandlerApproves(t *testing.T) {
	approved, reason := decidePermission(nil, json.RawMessage(`{"kind":"read"}`))
	if !approved {
		t.Fatalf("nil handler should approve")
	}
	if reason != "" {
		t.Fatalf("reason = %q, want empty", reason)
	}
}

// TestSessionRunFailsFastAfterTransportClose drives the §20.20 acceptance:
// when the ACP child exits between rules, the next session.Run must
// return ErrTransportClosed within 100 ms (not the rule timeout).
//
// We bypass the full ACP initialize handshake — we want to drive the
// transport directly, observe the readLoop reacting to stdout EOF, and
// assert session.Run's liveness gate triggers.
func TestSessionRunFailsFastAfterTransportClose(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses POSIX /usr/bin/true")
	}
	repo := t.TempDir()

	// `true` exits 0 immediately so its stdout closes within
	// milliseconds. dialStdio spawns + starts the readLoop goroutine
	// which will observe EOF and call markClosed().
	transport, err := dialStdio(context.Background(), "test-acp", []string{"/usr/bin/true"}, nil, "false")
	if err != nil {
		t.Fatalf("dialStdio: %v", err)
	}
	defer transport.close()

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) && !transport.isClosed() {
		time.Sleep(2 * time.Millisecond)
	}
	if !transport.isClosed() {
		t.Fatalf("transport should observe EOF and flip closed flag within 1s")
	}

	sess := &session{transport: transport, workingDir: repo, model: "sonnet"}
	start := time.Now()
	_, err = sess.Run(context.Background(), "hello", 5*time.Second)
	elapsed := time.Since(start)
	if err == nil {
		t.Fatalf("Run against closed transport should error")
	}
	if !errors.Is(err, ErrTransportClosed) {
		t.Fatalf("err = %v, want errors.Is ErrTransportClosed", err)
	}
	if !errors.Is(err, analyzer.ErrUnavailable) {
		t.Fatalf("err = %v, want errors.Is analyzer.ErrUnavailable (joined sentinel)", err)
	}
	if elapsed > 100*time.Millisecond {
		t.Fatalf("Run on closed transport returned in %v, want <=100ms", elapsed)
	}
}

func TestTransportMarkClosedIsIdempotent(t *testing.T) {
	transport := &transport{}
	transport.markClosed()
	if !transport.isClosed() {
		t.Fatalf("first markClosed should flip flag")
	}
	transport.markClosed() // must not panic on already-closed transport
	if !transport.isClosed() {
		t.Fatalf("second markClosed should remain closed")
	}
}

func TestACPWritableDirsHonorProviderConfigEnv(t *testing.T) {
	claudeConfigDir := t.TempDir()
	dirs, err := acpWritableDirs(string(analyzer.ProviderClaudeACP), map[string]string{
		"CLAUDE_CONFIG_DIR": claudeConfigDir,
	})
	if err != nil {
		t.Fatalf("acpWritableDirs: %v", err)
	}
	for _, dir := range dirs {
		if dir == claudeConfigDir {
			return
		}
	}
	t.Fatalf("CLAUDE_CONFIG_DIR %q not found in writable dirs: %v", claudeConfigDir, dirs)
}

func TestNewNilEnvProducesNilMap(t *testing.T) {
	p, err := New(Options{ID: "test", Command: []string{"echo"}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	prov := p.(*provider)
	if prov.env != nil {
		t.Fatalf("env = %v, want nil", prov.env)
	}
}

func TestNewID(t *testing.T) {
	p, err := New(Options{ID: "my-provider", Command: []string{"echo"}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if p.ID() != "my-provider" {
		t.Fatalf("ID = %q, want %q", p.ID(), "my-provider")
	}
}

// --- selectPermissionOptionID ---

func TestSelectPermissionOptionIDApproved(t *testing.T) {
	params := map[string]any{
		"options": []any{
			map[string]any{"optionId": "opt1", "kind": "reject_once"},
			map[string]any{"optionId": "opt2", "kind": "allow_once"},
			map[string]any{"optionId": "opt3", "kind": "allow_always"},
		},
	}
	got := selectPermissionOptionID(params, true)
	if got != "opt2" {
		t.Fatalf("got %q, want opt2", got)
	}
}

func TestSelectPermissionOptionIDRejected(t *testing.T) {
	params := map[string]any{
		"options": []any{
			map[string]any{"optionId": "opt1", "kind": "allow_once"},
			map[string]any{"optionId": "opt2", "kind": "reject_once"},
			map[string]any{"optionId": "opt3", "kind": "reject_always"},
		},
	}
	got := selectPermissionOptionID(params, false)
	if got != "opt2" {
		t.Fatalf("got %q, want opt2", got)
	}
}

func TestSelectPermissionOptionIDFallbackToFirst(t *testing.T) {
	params := map[string]any{
		"options": []any{
			map[string]any{"optionId": "opt1", "kind": "unknown"},
			map[string]any{"optionId": "opt2", "kind": "unknown"},
		},
	}
	got := selectPermissionOptionID(params, true)
	if got != "opt1" {
		t.Fatalf("got %q, want opt1 (fallback to first)", got)
	}
}

func TestSelectPermissionOptionIDNoOptions(t *testing.T) {
	got := selectPermissionOptionID(map[string]any{}, true)
	if got != "" {
		t.Fatalf("got %q, want empty", got)
	}
}

func TestSelectPermissionOptionIDSkipsNonMap(t *testing.T) {
	params := map[string]any{
		"options": []any{"not-a-map", 42},
	}
	got := selectPermissionOptionID(params, true)
	if got != "" {
		t.Fatalf("got %q, want empty", got)
	}
}

func TestSelectPermissionOptionIDSkipsEmptyID(t *testing.T) {
	params := map[string]any{
		"options": []any{
			map[string]any{"optionId": "", "kind": "allow_once"},
		},
	}
	got := selectPermissionOptionID(params, true)
	if got != "" {
		t.Fatalf("got %q, want empty", got)
	}
}

func TestSelectPermissionOptionIDNonArrayOptions(t *testing.T) {
	params := map[string]any{"options": "not-an-array"}
	got := selectPermissionOptionID(params, true)
	if got != "" {
		t.Fatalf("non-array: got %q, want empty", got)
	}
}

// --- translatePermissionRequest ---

func TestTranslatePermissionRequestAllKinds(t *testing.T) {
	tests := []struct {
		kind string
		want analyzer.PermissionKind
	}{
		{"read", analyzer.PermissionKindRead},
		{"url", analyzer.PermissionKindURL},
		{"shell", analyzer.PermissionKindShell},
		{"mcp", analyzer.PermissionKindMCPTool},
		{"custom_tool", analyzer.PermissionKindCustomTool},
		{"unknown-kind", analyzer.PermissionKind("unknown-kind")},
	}
	for _, tt := range tests {
		req := translatePermissionRequest(map[string]any{"kind": tt.kind})
		if req.Kind != tt.want {
			t.Errorf("kind=%q: got %q, want %q", tt.kind, req.Kind, tt.want)
		}
	}
}

func TestTranslatePermissionRequestReadKind(t *testing.T) {
	req := map[string]any{"kind": "read", "path": "/foo/bar"}
	pr := translatePermissionRequest(req)
	if pr.Kind != analyzer.PermissionKindRead {
		t.Fatalf("Kind = %q, want read", pr.Kind)
	}
	if pr.Path == nil || *pr.Path != "/foo/bar" {
		t.Fatalf("Path = %v, want /foo/bar", pr.Path)
	}
}

func TestTranslatePermissionRequestShellKind(t *testing.T) {
	req := map[string]any{
		"kind":                      "shell",
		"full_command_text":         "ls -la",
		"read_only":                 true,
		"has_write_file_redirection": false,
	}
	pr := translatePermissionRequest(req)
	if pr.Kind != analyzer.PermissionKindShell {
		t.Fatalf("Kind = %q, want shell", pr.Kind)
	}
	if pr.FullCommandText == nil || *pr.FullCommandText != "ls -la" {
		t.Fatalf("FullCommandText = %v, want ls -la", pr.FullCommandText)
	}
	if pr.ReadOnly == nil || !*pr.ReadOnly {
		t.Fatalf("ReadOnly = %v, want true", pr.ReadOnly)
	}
}

func TestTranslatePermissionRequestPossiblePaths(t *testing.T) {
	req := map[string]any{
		"kind":           "read",
		"possible_paths": []any{"/a.go", "/b.go", 42},
	}
	pr := translatePermissionRequest(req)
	if len(pr.PossiblePaths) != 2 {
		t.Fatalf("PossiblePaths len = %d, want 2", len(pr.PossiblePaths))
	}
}

func TestTranslatePermissionRequestCommands(t *testing.T) {
	req := map[string]any{
		"commands": []any{
			map[string]any{"identifier": "git", "read_only": true},
			map[string]any{"identifier": "rm", "read_only": false},
			"not-a-map",
		},
	}
	pr := translatePermissionRequest(req)
	if len(pr.Commands) != 2 {
		t.Fatalf("Commands len = %d, want 2", len(pr.Commands))
	}
	if pr.Commands[0].Identifier != "git" || !pr.Commands[0].ReadOnly {
		t.Fatalf("Commands[0] = %+v", pr.Commands[0])
	}
}

func TestTranslatePermissionRequestEmptyMap(t *testing.T) {
	req := translatePermissionRequest(map[string]any{})
	if req.Kind != "" {
		t.Fatalf("empty map: kind = %q", req.Kind)
	}
}

// --- extractSessionID ---

func TestExtractSessionIDCamelCase(t *testing.T) {
	raw := json.RawMessage(`{"sessionId":"abc-123"}`)
	got := extractSessionID(raw)
	if got != "abc-123" {
		t.Fatalf("got %q, want abc-123", got)
	}
}

func TestExtractSessionIDSnakeCase(t *testing.T) {
	raw := json.RawMessage(`{"session_id":"def-456"}`)
	got := extractSessionID(raw)
	if got != "def-456" {
		t.Fatalf("got %q, want def-456", got)
	}
}

func TestExtractSessionIDEmpty(t *testing.T) {
	if got := extractSessionID(nil); got != "" {
		t.Fatalf("nil: got %q, want empty", got)
	}
	if got := extractSessionID(json.RawMessage(`{}`)); got != "" {
		t.Fatalf("empty obj: got %q, want empty", got)
	}
	if got := extractSessionID(json.RawMessage(`{bad json`)); got != "" {
		t.Fatalf("malformed: got %q, want empty", got)
	}
}

// --- pickAvailableModelID ---

func TestPickAvailableModelIDExactMatch(t *testing.T) {
	raw := json.RawMessage(`{"models":{"availableModels":[{"modelId":"sonnet"},{"modelId":"opus"}]}}`)
	got, ok := pickAvailableModelID(raw, "sonnet", nil)
	if !ok || got != "sonnet" {
		t.Fatalf("got (%q, %v), want (sonnet, true)", got, ok)
	}
}

func TestPickAvailableModelIDFallback(t *testing.T) {
	raw := json.RawMessage(`{"models":{"availableModels":[{"modelId":"opus"}]}}`)
	got, ok := pickAvailableModelID(raw, "sonnet", []string{"opus", "haiku"})
	if !ok || got != "opus" {
		t.Fatalf("got (%q, %v), want (opus, true)", got, ok)
	}
}

func TestPickAvailableModelIDNoMatch(t *testing.T) {
	raw := json.RawMessage(`{"models":{"availableModels":[{"modelId":"opus"}]}}`)
	_, ok := pickAvailableModelID(raw, "sonnet", []string{"haiku"})
	if ok {
		t.Fatal("expected ok=false when no match")
	}
}

func TestPickAvailableModelIDEmptyPreferred(t *testing.T) {
	raw := json.RawMessage(`{"models":{"availableModels":[{"modelId":"opus"}]}}`)
	_, ok := pickAvailableModelID(raw, "", nil)
	if ok {
		t.Fatal("expected ok=false for empty preferred")
	}
}

func TestPickAvailableModelIDMalformed(t *testing.T) {
	_, ok := pickAvailableModelID(json.RawMessage(`{bad`), "sonnet", nil)
	if ok {
		t.Fatal("expected ok=false for malformed JSON")
	}
}

// --- pickReadOnlyModeID ---

func TestPickReadOnlyModeIDPlan(t *testing.T) {
	raw := json.RawMessage(`{"modes":{"availableModes":[{"id":"plan"},{"id":"act"}]}}`)
	got, ok := pickReadOnlyModeID(raw)
	if !ok || got != "plan" {
		t.Fatalf("got (%q, %v), want (plan, true)", got, ok)
	}
}

func TestPickReadOnlyModeIDReadOnly(t *testing.T) {
	raw := json.RawMessage(`{"modes":{"availableModes":[{"id":"read-only"}]}}`)
	got, ok := pickReadOnlyModeID(raw)
	if !ok || got != "read-only" {
		t.Fatalf("got (%q, %v), want (read-only, true)", got, ok)
	}
}

func TestPickReadOnlyModeIDNoMatch(t *testing.T) {
	raw := json.RawMessage(`{"modes":{"availableModes":[{"id":"act"}]}}`)
	_, ok := pickReadOnlyModeID(raw)
	if ok {
		t.Fatal("expected ok=false when no recognized mode")
	}
}

func TestPickReadOnlyModeIDMalformed(t *testing.T) {
	_, ok := pickReadOnlyModeID(json.RawMessage(`{bad`))
	if ok {
		t.Fatal("expected ok=false for malformed JSON")
	}
}

func TestPickReadOnlyModeIDNoModes(t *testing.T) {
	_, ok := pickReadOnlyModeID(json.RawMessage(`{}`))
	if ok {
		t.Fatal("expected ok=false when no modes key")
	}
}

// --- extractStopReason ---

func TestExtractStopReason(t *testing.T) {
	raw := json.RawMessage(`{"stopReason":"end_turn"}`)
	if got := extractStopReason(raw); got != "end_turn" {
		t.Fatalf("got %q, want end_turn", got)
	}
}

func TestExtractStopReasonEmpty(t *testing.T) {
	if got := extractStopReason(nil); got != "" {
		t.Fatalf("nil: got %q, want empty", got)
	}
	if got := extractStopReason(json.RawMessage(`{}`)); got != "" {
		t.Fatalf("empty: got %q, want empty", got)
	}
	if got := extractStopReason(json.RawMessage(`{bad`)); got != "" {
		t.Fatalf("malformed: got %q, want empty", got)
	}
}

// --- copyStringMap ---

func TestCopyStringMapNil(t *testing.T) {
	if got := copyStringMap(nil); got != nil {
		t.Fatalf("got %v, want nil", got)
	}
	if got := copyStringMap(map[string]string{}); got != nil {
		t.Fatalf("empty: got %v, want nil", got)
	}
}

func TestCopyStringMapCopies(t *testing.T) {
	in := map[string]string{"a": "1", "b": "2"}
	out := copyStringMap(in)
	in["a"] = "mutated"
	if out["a"] != "1" {
		t.Fatalf("map was not copied")
	}
}

// --- jsonrpcError ---

func TestJSONRPCErrorFormat(t *testing.T) {
	e := &jsonrpcError{Code: -32000, Message: "closed"}
	got := e.Error()
	want := "rpc error -32000: closed"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestJSONRPCErrorWithData(t *testing.T) {
	e := &jsonrpcError{Code: 123, Message: "bad", Data: json.RawMessage(`"detail"`)}
	got := e.Error()
	want := `rpc error 123: bad (data: "detail")`
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestJSONRPCErrorNil(t *testing.T) {
	var e *jsonrpcError
	if got := e.Error(); got != "" {
		t.Fatalf("nil error: got %q, want empty", got)
	}
}

// --- sessionStream ---

func TestSessionStreamAppendAndText(t *testing.T) {
	s := &sessionStream{}
	s.append("hello")
	s.append(" world")
	if got := s.text(); got != "hello world" {
		t.Fatalf("got %q, want %q", got, "hello world")
	}
}

func TestSessionStreamAppendEmpty(t *testing.T) {
	s := &sessionStream{}
	s.append("")
	if got := s.text(); got != "" {
		t.Fatalf("got %q, want empty", got)
	}
}

// --- acpWritableDirs edge cases ---

func TestACPWritableDirsUnknownProvider(t *testing.T) {
	dirs, err := acpWritableDirs("unknown-provider", nil)
	if err != nil {
		t.Fatalf("acpWritableDirs: %v", err)
	}
	if len(dirs) != 1 {
		t.Fatalf("expected only temp dir for unknown provider, got %d", len(dirs))
	}
}

func TestACPWritableDirsGeminiEnv(t *testing.T) {
	dir := t.TempDir()
	dirs, err := acpWritableDirs(string(analyzer.ProviderGeminiACP), map[string]string{
		"GEMINI_HOME": dir,
	})
	if err != nil {
		t.Fatalf("acpWritableDirs: %v", err)
	}
	found := false
	for _, d := range dirs {
		if d == dir {
			found = true
		}
	}
	if !found {
		t.Fatalf("GEMINI_HOME %q not in dirs: %v", dir, dirs)
	}
}

func TestACPWritableDirsCodexEnv(t *testing.T) {
	dir := t.TempDir()
	dirs, err := acpWritableDirs(string(analyzer.ProviderCodexACP), map[string]string{
		"CODEX_HOME": dir,
	})
	if err != nil {
		t.Fatalf("acpWritableDirs: %v", err)
	}
	found := false
	for _, d := range dirs {
		if d == dir {
			found = true
		}
	}
	if !found {
		t.Fatalf("CODEX_HOME %q not in dirs: %v", dir, dirs)
	}
}

func TestACPWritableDirsCopilot(t *testing.T) {
	dirs, err := acpWritableDirs(string(analyzer.ProviderCopilotACP), nil)
	if err != nil {
		t.Fatalf("acpWritableDirs: %v", err)
	}
	if len(dirs) != 1 {
		t.Fatalf("copilot should have exactly 1 dir (temp), got %d", len(dirs))
	}
}

// --- resolveACPConfigDir ---

func TestResolveACPConfigDirFromEnvMap(t *testing.T) {
	dir := t.TempDir()
	got, err := resolveACPConfigDir(map[string]string{"MY_ENV": dir}, "MY_ENV", "fallback")
	if err != nil {
		t.Fatalf("resolveACPConfigDir: %v", err)
	}
	if got != dir {
		t.Fatalf("got %q, want %q", got, dir)
	}
}

func TestResolveACPConfigDirFallback(t *testing.T) {
	got, err := resolveACPConfigDir(nil, "NONEXISTENT_ENV_VAR_12345", ".fallback")
	if err != nil {
		t.Fatalf("resolveACPConfigDir: %v", err)
	}
	if !strings.HasSuffix(got, ".fallback") {
		t.Fatalf("got %q, expected suffix .fallback", got)
	}
}

// --- New validation ---

func TestNewRequiresID(t *testing.T) {
	_, err := New(Options{Command: []string{"cmd"}})
	if err == nil {
		t.Fatal("empty ID should error")
	}
}

func TestNewRequiresCommand(t *testing.T) {
	_, err := New(Options{ID: "test"})
	if err == nil {
		t.Fatal("empty Command should error")
	}
}

func TestNewCopiesCommand(t *testing.T) {
	cmd := []string{"original"}
	p, err := New(Options{ID: "test", Command: cmd})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	cmd[0] = "mutated"
	// Provider should still report its ID correctly.
	if p.ID() != "test" {
		t.Fatalf("ID should be 'test'")
	}
}

// --- NewSession before Start ---

func TestNewSessionBeforeStart(t *testing.T) {
	p, err := New(Options{ID: "test", Command: []string{"echo"}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, err = p.NewSession(context.Background(), analyzer.SessionConfig{})
	if err == nil {
		t.Fatal("expected error for NewSession before Start")
	}
}

// --- Close idempotent ---

func TestCloseIdempotent(t *testing.T) {
	p, err := New(Options{ID: "test", Command: []string{"echo"}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := p.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := p.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}

// --- session.Run nil context ---

func TestSessionRunNilContext(t *testing.T) {
	p, err := New(Options{ID: "test", Command: []string{"echo"}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	prov := p.(*provider)
	// Manually set up a minimal transport so NewSession succeeds
	prov.started = true
	prov.transport = &transport{}
	sess, err := prov.NewSession(context.Background(), analyzer.SessionConfig{})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	_, err = sess.Run(nil, "test", 0)
	if !errors.Is(err, analyzer.ErrNilContext) {
		t.Fatalf("err = %v, want ErrNilContext", err)
	}
}

// --- transport tests ---
// Note: io.Pipe is synchronous (writes block until reads happen).
// Use bytes.Buffer for stdin when we don't care about written data.
// Use io.Pipe with a reader goroutine when we need to verify what was written.

func TestTransportSend(t *testing.T) {
	var stdin bytes.Buffer
	tr := &transport{
		stdin: nopWriteCloser{&stdin},
	}

	payload := map[string]any{"jsonrpc": "2.0", "method": "test"}
	if err := tr.send(payload); err != nil {
		t.Fatalf("send: %v", err)
	}
	// Verify data was written.
	if stdin.Len() == 0 {
		t.Fatal("expected data written to stdin")
	}
}

func TestTransportSendClosed(t *testing.T) {
	tr := &transport{closed: true}
	err := tr.send(map[string]any{"jsonrpc": "2.0", "method": "test"})
	if !errors.Is(err, ErrTransportClosed) {
		t.Fatalf("expected ErrTransportClosed, got %v", err)
	}
}

func TestTransportCallContextCancellation(t *testing.T) {
	// Use bytes.Buffer for stdin so send() doesn't block on a synchronous pipe.
	var stdin bytes.Buffer
	var stdout bytes.Buffer
	tr := &transport{
		stdin:  nopWriteCloser{&stdin},
		stdout: bufio.NewReader(&stdout),
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	_, err := tr.call(ctx, "test", nil, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}

func TestTransportReadLoopDispatchesResponse(t *testing.T) {
	var buf bytes.Buffer
	tr := &transport{
		stdout: bufio.NewReader(&buf),
	}

	// Pre-register a pending request.
	respCh := make(chan jsonrpcResponse, 1)
	tr.pending.Store("42", respCh)

	// Write a valid JSON-RPC response to the reader.
	buf.WriteString(`{"jsonrpc":"2.0","id":"42","result":{"ok":true}}` + "\n")

	tr.readLoop(nil)

	select {
	case r := <-respCh:
		if r.Error != nil {
			t.Fatalf("unexpected error: %v", r.Error)
		}
		var parsed map[string]bool
		if err := json.Unmarshal(r.Result, &parsed); err != nil {
			t.Fatalf("unmarshal result: %v", err)
		}
		if !parsed["ok"] {
			t.Fatalf("expected ok=true")
		}
	default:
		t.Fatalf("response not dispatched")
	}
}

func TestTransportReadLoopHandlesSessionUpdate(t *testing.T) {
	var buf bytes.Buffer
	tr := &transport{
		stdout: bufio.NewReader(&buf),
	}

	stream := tr.openStream("sid-1")
	defer tr.closeStream("sid-1")

	buf.WriteString(`{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"sid-1","update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"hello"}}}}` + "\n")

	tr.readLoop(nil)

	if got := stream.text(); got != "hello" {
		t.Fatalf("stream text = %q, want %q", got, "hello")
	}
}

func TestTransportReadLoopHandlesError(t *testing.T) {
	var buf bytes.Buffer
	tr := &transport{
		stdout: bufio.NewReader(&buf),
	}

	respCh := make(chan jsonrpcResponse, 1)
	tr.pending.Store("99", respCh)

	buf.WriteString(`{"jsonrpc":"2.0","id":"99","error":{"code":-32600,"message":"invalid request"}}` + "\n")

	tr.readLoop(nil)

	r := <-respCh
	if r.Error == nil {
		t.Fatalf("expected error in response")
	}
	if r.Error.Code != -32600 {
		t.Fatalf("error code = %d, want -32600", r.Error.Code)
	}
}

func TestTransportReadLoopSkipsMalformedJSON(t *testing.T) {
	var buf bytes.Buffer
	tr := &transport{
		stdout: bufio.NewReader(&buf),
	}

	respCh := make(chan jsonrpcResponse, 1)
	tr.pending.Store("1", respCh)

	buf.WriteString("not-json\n")
	buf.WriteString(`{"jsonrpc":"2.0","id":"1","result":"ok"}` + "\n")

	tr.readLoop(nil)

	r := <-respCh
	if r.Error != nil {
		t.Fatalf("unexpected error: %v", r.Error)
	}
}

func TestTransportNotify(t *testing.T) {
	pr, pw := io.Pipe()
	defer pr.Close()
	defer pw.Close()

	tr := &transport{
		stdin: nopWriteCloser{pw},
	}

	done := make(chan []byte, 1)
	go func() {
		scanner := bufio.NewScanner(pr)
		if scanner.Scan() {
			done <- []byte(scanner.Text())
		}
	}()

	err := tr.notify(context.Background(), "session/cancel", map[string]any{"sessionId": "s1"})
	if err != nil {
		t.Fatalf("notify: %v", err)
	}

	select {
	case data := <-done:
		var envelope rpcEnvelope
		if err := json.Unmarshal(data, &envelope); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if envelope.Method != "session/cancel" {
			t.Fatalf("method = %q, want session/cancel", envelope.Method)
		}
		if envelope.ID != "" {
			t.Fatalf("notification should have no id, got %q", envelope.ID)
		}
	case <-time.After(time.Second):
		t.Fatalf("timeout waiting for notify")
	}
}

// --- helper ---

type nopWriteCloser struct{ io.Writer }

func (nopWriteCloser) Close() error { return nil }
