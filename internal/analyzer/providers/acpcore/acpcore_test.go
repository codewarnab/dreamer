package acpcore

import (
	"context"
	"encoding/json"
	"errors"
	"runtime"
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
	transport, err := dialStdio(context.Background(), []string{"/usr/bin/true"}, nil, "false")
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
