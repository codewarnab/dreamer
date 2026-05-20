//go:build linux || windows

package fsutil

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// TestAcquireLockReclaimsAfterPIDReuse simulates a SIGKILL-then-PID-reuse
// scenario: the lock file records our PID (alive) but a different executable
// path than the running process's actual exe. Acquire must treat that as
// stale, not refuse to start. Gated to linux/windows because those are the
// only OSes where processExecutable can answer (see process_other_unix.go).
func TestAcquireLockReclaimsAfterPIDReuse(t *testing.T) {
	dir := t.TempDir()
	lockPath := filepath.Join(dir, "test.lock")
	logger := newTestLogger(t)

	payload := fmt.Sprintf("%d\n0\n/usr/bin/some-other-program-that-does-not-match\n", os.Getpid())
	if writeErr := os.WriteFile(lockPath, []byte(payload), 0o644); writeErr != nil {
		t.Fatalf("write lock: %v", writeErr)
	}

	release, err := AcquireLock(lockPath, logger)
	if err != nil {
		t.Fatalf("AcquireLock should reclaim after PID reuse: %v", err)
	}
	defer release()
}
