package acpcore

import (
	"context"
	"errors"
	"runtime"
	"testing"
	"time"

	"dreamer/internal/analyzer"
)

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
	tr, err := dialStdio(context.Background(), []string{"/usr/bin/true"}, nil)
	if err != nil {
		t.Fatalf("dialStdio: %v", err)
	}
	defer tr.close()

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) && !tr.isClosed() {
		time.Sleep(2 * time.Millisecond)
	}
	if !tr.isClosed() {
		t.Fatalf("transport should observe EOF and flip closed flag within 1s")
	}

	sess := &session{transport: tr, workingDir: repo, model: "sonnet"}
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
	tr := &transport{}
	tr.markClosed()
	if !tr.isClosed() {
		t.Fatalf("first markClosed should flip flag")
	}
	tr.markClosed() // must not panic on already-closed transport
	if !tr.isClosed() {
		t.Fatalf("second markClosed should remain closed")
	}
}
