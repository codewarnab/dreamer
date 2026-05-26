package acpcore

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os/exec"
	"runtime"
	"strings"
	"sync/atomic"
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

// TestCleanupRunsOnEOFBeforClose exercises the "EOF-first" ordering:
// readLoop observes stdout EOF and flips closed=true; then provider.Close
// is called. With the sync.Once fix, all cleanup hooks must still run.
func TestCleanupRunsOnEOFBeforClose(t *testing.T) {
	var sandboxCalls, prepareCalls atomic.Int32

	cmd := exec.Command("go", "version")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}

	tt := &transport{
		cmd:            cmd,
		stdin:          stdin,
		stdout:         bufio.NewReader(stdout),
		stderrPipe:     stderr,
		sandboxCleanup: func() { sandboxCalls.Add(1) },
		prepareCleanup: func() { prepareCalls.Add(1) },
	}
	tt.goroutines.Add(2)
	go tt.drainStderr(stderr)
	go tt.readLoop(nil)

	// Wait for readLoop to observe EOF and flip closed.
	deadline := time.After(2 * time.Second)
	for !tt.isClosed() {
		select {
		case <-deadline:
			t.Fatal("readLoop did not flip closed within 2s")
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}

	// Now call close() — EOF was first. cleanupOnce must still run.
	if err := tt.close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	if got := sandboxCalls.Load(); got != 1 {
		t.Errorf("sandboxCleanup calls = %d, want 1", got)
	}
	if got := prepareCalls.Load(); got != 1 {
		t.Errorf("prepareCleanup calls = %d, want 1", got)
	}
}

// TestCleanupRunsOnCloseBeforeEOF exercises the "Close-first" ordering:
// provider.Close() is called before readLoop sees EOF. cleanupOnce must
// run exactly once, and the subsequent markClosed from readLoop must be
// a no-op for resources.
func TestCleanupRunsOnCloseBeforeEOF(t *testing.T) {
	var sandboxCalls, prepareCalls atomic.Int32

	// Build a transport with pipes we control (not connected to a real
	// process) so we can sequence Close-before-EOF manually.
	_, stdinW := io.Pipe()
	stdoutR, stdoutW := io.Pipe()
	stderrR, stderrW := io.Pipe()

	// Use a fake cmd. We can't call cmd.Wait() on it, so we'll
	// skip the cmd.Wait portion of close() by directly testing
	// the cleanupOnce mechanics.
	tt := &transport{
		stdin:          stdinW,
		sandboxCleanup: func() { sandboxCalls.Add(1) },
		prepareCleanup: func() { prepareCalls.Add(1) },
	}
	// Use a real cmd that exits immediately so cmd.Wait() returns fast.
	cmd := exec.Command("go", "version")
	if err := cmd.Start(); err != nil {
		t.Skip("go not available")
	}
	tt.cmd = cmd
	tt.stdout = bufio.NewReader(stdoutR)
	tt.stderrPipe = stderrR

	// Close the fake pipes' read ends so drainStderr and readLoop exit.
	go func() {
		// Let close() run first.
		time.Sleep(50 * time.Millisecond)
		stdoutW.Close()
		stderrW.Close()
	}()

	// Close before readLoop sees EOF.
	tt.goroutines.Add(2)
	go tt.drainStderr(stderrR)
	go tt.readLoop(nil)
	if err := tt.close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	if got := sandboxCalls.Load(); got != 1 {
		t.Errorf("sandboxCleanup calls = %d, want 1", got)
	}
	if got := prepareCalls.Load(); got != 1 {
		t.Errorf("prepareCleanup calls = %d, want 1", got)
	}
}

// TestCloseIsIdempotent verifies calling close() multiple times doesn't
// re-run cleanup hooks or panic.
func TestCloseIsIdempotent(t *testing.T) {
	var sandboxCalls, prepareCalls atomic.Int32

	cmd := exec.Command("go", "version")
	if err := cmd.Start(); err != nil {
		t.Skip("go not available")
	}

	stdinR, stdinW := io.Pipe()
	stdoutR, _ := io.Pipe()
	stderrR, _ := io.Pipe()

	tt := &transport{
		cmd:            cmd,
		stdin:          stdinW,
		stdout:         bufio.NewReader(stdoutR),
		stderrPipe:     stderrR,
		sandboxCleanup: func() { sandboxCalls.Add(1) },
		prepareCleanup: func() { prepareCalls.Add(1) },
	}
	// Close pipes so goroutines exit quickly.
	stdinR.Close()
	stdoutR.Close()
	stderrR.Close()

	tt.goroutines.Add(2)
	go tt.drainStderr(io.NopCloser(strings.NewReader("")))
	go tt.readLoop(nil)

	// Call close three times.
	for i := 0; i < 3; i++ {
		if err := tt.close(); err != nil {
			t.Fatalf("close call %d: %v", i+1, err)
		}
	}

	if got := sandboxCalls.Load(); got != 1 {
		t.Errorf("sandboxCleanup calls = %d after 3 closes, want 1", got)
	}
	if got := prepareCalls.Load(); got != 1 {
		t.Errorf("prepareCleanup calls = %d after 3 closes, want 1", got)
	}
}
