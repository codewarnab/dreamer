# Concurrency Fixes Implementation Plan

## Context

The codebase analysis identified 8 concurrency issues across 3 severity tiers. This plan addresses them in priority order. The two MEDIUM issues are real data races; the LOW issues are edge-case goroutine leaks and missing context propagation.

---

## Phase 1: CLI Provider `strings.Builder` Race (MEDIUM)

### Problem
All 4 CLI providers (`claudecli`, `geminicli`, `codexcli`, `openclaudecli`) set `cmd.Stderr = stderrBuf` where `stderrBuf` is an unprotected `strings.Builder`. The OS pipe driver writes to it from the child process's stderr, and a goroutine also writes to it on stdin failure — both without synchronization. `strings.Builder` is not thread-safe.

### Fix: Shared `SafeBuffer` type

`acpcore` already does this correctly — its `drainStderr` method (line 773-789) locks `t.mu` before writing. Create a reusable `SafeBuffer` in the transport package.

### Files to modify
- **NEW** `internal/analyzer/transport/syncwriter.go` — `SafeBuffer` type (mutex-protected strings.Builder)
- **NEW** `internal/analyzer/transport/syncwriter_test.go` — concurrent read/write test
- `internal/analyzer/providers/claudecli/claudecli.go` — replace `strings.Builder` with `SafeBuffer`
- `internal/analyzer/providers/geminicli/geminicli.go` — same
- `internal/analyzer/providers/codexcli/codexcli.go` — same
- `internal/analyzer/providers/openclaudecli/openclaudecli.go` — same

### Implementation detail

```go
// transport/syncwriter.go
package transport

import (
    "strings"
    "sync"
)

// SafeBuffer is a mutex-protected strings.Builder safe for concurrent
// writes from multiple goroutines (e.g., OS pipe driver + error handler).
type SafeBuffer struct {
    mu  sync.Mutex
    buf strings.Builder
}

func (b *SafeBuffer) Write(p []byte) (int, error) {
    b.mu.Lock()
    defer b.mu.Unlock()
    return b.buf.Write(p)
}

func (b *SafeBuffer) WriteString(s string) (int, error) {
    b.mu.Lock()
    defer b.mu.Unlock()
    return b.buf.WriteString(s)
}

func (b *SafeBuffer) String() string {
    b.mu.Lock()
    defer b.mu.Unlock()
    return b.buf.String()
}
```

In each CLI provider, change:
```go
// Before:
stderrBuf := new(strings.Builder)
// After:
stderrBuf := new(transport.SafeBuffer)
```

No other changes needed — `SafeBuffer` implements `io.Writer`, so `cmd.Stderr = stderrBuf` still works. `.String()` and `.WriteString()` calls are drop-in replacements.

---

## Phase 2: `detectPort` Goroutine Leak + Stderr Pipe Leak (LOW+MEDIUM)

### Problem
`opencodehttp.go:255-283` — if the timeout fires, the goroutine keeps calling `r.Read(buf)` until the child process exits and the pipe closes. The caller (lines 143-144) always kills the process on timeout, so the goroutine does get cleaned up — but it's cleaner to close the pipe directly from the timeout path.

### Additional Problem: stderr pipe leak on success path (GAP — not in original analysis)
On the **success** path, `detectPort` returns the port but the stderr pipe is never closed by the caller. The goroutine exits after finding the port, but the pipe stays open. The caller only closes the pipe on error (`cmd.Process.Kill()`). On success, the pipe leaks until `cmd.Wait()` eventually closes it — but `cmd.Wait()` is never called in the happy path for the HTTP provider's server process.

### Fix: Close the pipe on both timeout AND success paths

Pass `io.ReadCloser` (the pipe from `cmd.StderrPipe()`) instead of `io.Reader`, close it on timeout, and also close it from the caller on success.

### Files to modify
- `internal/analyzer/providers/opencodehttp/opencodehttp.go` — change `detectPort` signature to accept `io.ReadCloser`, close on timeout; add `defer stderr.Close()` at call site

### Implementation detail

```go
func detectPort(r io.ReadCloser, timeout time.Duration) (int, error) {
    type result struct {
        port int
        err  error
    }
    ch := make(chan result, 1)
    go func() {
        buf := make([]byte, 4096)
        var accumulated string
        for {
            n, err := r.Read(buf)
            if n > 0 {
                accumulated += string(buf[:n])
                lines := strings.Split(accumulated, "\n")
                for _, line := range lines {
                    line = strings.TrimSpace(line)
                    if port := extractPort(line); port > 0 {
                        ch <- result{port: port}
                        return
                    }
                }
            }
            if err != nil {
                ch <- result{err: fmt.Errorf("server exited without reporting port: %w", err)}
                return
            }
        }
    }()

    select {
    case r := <-ch:
        return r.port, r.err
    case <-time.After(timeout):
        _ = r.Close() // unblocks the Read, goroutine exits
        return 0, fmt.Errorf("timeout waiting for server port")
    }
}
```

The caller already passes `stderr` from `cmd.StderrPipe()` which returns `io.ReadCloser`. **Caller-side change needed:** also close the pipe on success:
```go
// After detectPort returns successfully:
defer stderr.Close()
```

---

## Phase 3: `acpcore.transport.close` Timeout Documentation (LOW)

### Problem
`acpcore.go:791-809` — the 5-second shutdown timeout is hard-coded via `time.After`.

### Assessment after review
`close()` is called from `provider.Close()` (line 192) which implements a `Close() error` interface — **no context is available**. The 5-second timeout is intentional and appropriate for a graceful shutdown. `Kill()` + `KILL_ON_JOB_CLOSE` from the sandbox always ensures the process exits.

### Fix: Add clarifying comment only

### Files to modify
- `internal/analyzer/providers/acpcore/acpcore.go` — add comment on the 5s timeout explaining why it's hard-coded

---

## Phase 4: `sandbox.PostStartOrKill` Zombie Reaper Timeout (LOW)

### Problem
`sandbox.go:150` — `go func() { _ = cmd.Wait() }()` has no timeout. If `cmd.Wait()` hangs after `Kill()`, the goroutine leaks.

### Fix: Add a bounded wait with fallback

### Files to modify
- `internal/sandbox/sandbox.go` — add timeout to the reaper goroutine

### Implementation detail

```go
if cmd != nil && cmd.Process != nil {
    _ = cmd.Process.Kill()
    go func() {
        done := make(chan error, 1)
        go func() { done <- cmd.Wait() }()
        select {
        case <-done:
        case <-time.After(10 * time.Second):
            // Process didn't exit after Kill — abandon.
            // KILL_ON_JOB_CLOSE from the Job Object will clean up.
        }
    }()
}
```

Note: The nested goroutine is needed because the outer goroutine must not block the caller. The timeout is a safety net — `Kill()` + `KILL_ON_JOB_CLOSE` should always work.

---

## Phase 5: `acpcore.close()` Goroutine Cleanup (MEDIUM)

### Problem
`acpcore.go:473-474` — `drainStderr` and `readLoop` goroutines rely on the OS closing pipes when `cmd.Process.Kill()` fires (line 806). If `Kill()` fails silently on a zombie process (before Job Object cleanup), both goroutines hang indefinitely. This mirrors the sandbox zombie reaper issue (Phase 4) but exists in acpcore.

### Fix: Explicitly close stdout/stderr pipes in `close()`

### Files to modify
- `internal/analyzer/providers/acpcore/acpcore.go` — close pipes before or alongside Kill, add bounded wait for goroutine exit

### Implementation detail
```go
func (t *transport) close() {
    t.closeOnce.Do(func() {
        t.closed.Store(true)
        if t.cmd != nil && t.cmd.Process != nil {
            _ = t.cmd.Process.Kill()
        }
        // Explicitly close pipes so goroutines exit even if Kill() is ineffective
        if t.stdin != nil {
            _ = t.stdin.Close()
        }
        if t.stderrPipe != nil {
            _ = t.stderrPipe.Close()
        }
        // Wait for goroutines with timeout
        select {
        case <-t.done:
        case <-time.After(5 * time.Second):
            // Goroutines didn't exit — abandon. KILL_ON_JOB_CLOSE will clean up.
        }
    })
}
```

Note: This requires adding `done chan struct{}` closed by both `readLoop` and `drainStderr` on exit, and storing pipe references. Check current implementation first — existing `closeOnce` and `closed` atomic are already in place.

---

## Phase 6: FindingRecorder Cross-Process Note (DOCUMENTATION ONLY)

### Problem
`mcpserver/validation.go:201` — single-process safety is documented in a comment, but there's no guard against misuse if someone adds parallel MCP writers.

### Fix: Strengthen the existing warning comment.

### Files to modify
- `internal/mcpserver/validation.go` — improve the existing comment

---

## Phase 7: acpcore `stderrBuf` Dead Code (LOW)

### Problem
`acpcore.go:310,782` — `drainStderr` writes to `t.stderrBuf` under `t.mu.Lock()`, but the field is **never read** anywhere. CLI providers read their `stderrBuf.String()` for error messages, but acpcore does not. The mutex-protected writes are wasted work; acpcore has no way to surface stderr on process failure.

### Fix: Either surface stderr in error messages (read the field) or remove the dead writes

### Files to modify
- `internal/analyzer/providers/acpcore/acpcore.go` — decide: surface stderr or remove dead field

---

## Verification

```bash
go vet ./...
go test ./internal/analyzer/transport/ ./internal/analyzer/providers/claudecli/ ./internal/analyzer/providers/codexcli/ ./internal/analyzer/providers/geminicli/ ./internal/analyzer/providers/openclaudecli/ ./internal/analyzer/providers/acpcore/ ./internal/analyzer/providers/opencodehttp/ ./internal/sandbox/ ./internal/mcpserver/ ./...
```

---

## Commit Strategy

One commit per phase, following HOW_TO_COMMIT.md:
1. `Fix CLI provider stderr data race with SafeBuffer` — Phase 1 (biggest impact, 6 files)
2. `Fix detectPort goroutine leak and stderr pipe leak` — Phase 2
3. `Document acpcore transport close timeout rationale` — Phase 3
4. `Add timeout to sandbox zombie reaper` — Phase 4
5. `Fix acpcore close goroutine cleanup with explicit pipe closure` — Phase 5
6. `Strengthen FindingRecorder cross-process warning` — Phase 6
7. `Surface or remove acpcore dead stderrBuf` — Phase 7

Could bundle Phases 3+4+5+6+7 into: `Harden concurrency edge cases`.

## Files Summary

| Phase | Files | Type |
|-------|-------|------|
| 1 | `transport/syncwriter.go`, `transport/syncwriter_test.go` | NEW |
| 1 | `claudecli.go`, `geminicli.go`, `codexcli.go`, `openclaudecli.go` | EDIT |
| 2 | `opencodehttp.go` | EDIT (detectPort signature + caller stderr close) |
| 3 | `acpcore.go` | EDIT (comment only) |
| 4 | `sandbox.go` | EDIT |
| 5 | `acpcore.go` | EDIT (pipe closure + bounded wait) |
| 6 | `validation.go` | EDIT (comment only) |
| 7 | `acpcore.go` | EDIT (dead code removal or surface stderr) |

## Sequencing Note

Execute this entire plan **before** the test coverage improvement plan. The `detectPort` signature change (Phase 2) will break any tests written against the old `io.Reader` signature.
