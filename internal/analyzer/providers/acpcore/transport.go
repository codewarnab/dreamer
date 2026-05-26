package acpcore

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"dreamer/internal/analyzer"
	transportutil "dreamer/internal/analyzer/transport"
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
