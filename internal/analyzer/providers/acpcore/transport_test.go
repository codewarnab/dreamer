package acpcore

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

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

	_, err := tr.call(ctx, "test", nil)
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

	tr.goroutines.Add(1)
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

	tr.goroutines.Add(1)
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

	tr.goroutines.Add(1)
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

	tr.goroutines.Add(1)
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

// TestCleanupRunsOnEOFBeforClose exercises the "EOF-first" ordering:
// readLoop observes stdout EOF and flips closed=true; then provider.Close
// is called. With the sync.Once fix, all cleanup hooks must still run.
func TestCleanupRunsOnEOFBeforeClose(t *testing.T) {
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

// --- helper ---

type nopWriteCloser struct{ io.Writer }

func (nopWriteCloser) Close() error { return nil }
