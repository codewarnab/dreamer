package fsutil

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"dreamer/internal/logging"
)

func newTestLogger(t *testing.T) *logging.Logger {
	t.Helper()
	logger, err := logging.New(t.TempDir(), "debug", 0)
	if err != nil {
		t.Fatalf("create logger: %v", err)
	}
	t.Cleanup(func() { _ = logger.Close() })
	return logger
}

func TestAcquireLockCreatesLockFile(t *testing.T) {
	dir := t.TempDir()
	lockPath := filepath.Join(dir, "test.lock")
	logger := newTestLogger(t)

	release, err := AcquireLock(lockPath, logger)
	if err != nil {
		t.Fatalf("AcquireLock: %v", err)
	}

	lockBytes, readErr := os.ReadFile(lockPath)
	if readErr != nil {
		t.Fatalf("read lock file: %v", readErr)
	}
	if len(lockBytes) == 0 {
		t.Fatal("lock file is empty")
	}

	release()

	if _, statErr := os.Stat(lockPath); !os.IsNotExist(statErr) {
		t.Fatal("lock file still exists after release")
	}
}

func TestAcquireLockRejectsLiveProcess(t *testing.T) {
	dir := t.TempDir()
	lockPath := filepath.Join(dir, "test.lock")
	logger := newTestLogger(t)

	release, err := AcquireLock(lockPath, logger)
	if err != nil {
		t.Fatalf("first AcquireLock: %v", err)
	}
	defer release()

	_, err = AcquireLock(lockPath, logger)
	if err == nil {
		t.Fatal("second AcquireLock should have failed")
	}
}

func TestAcquireLockCleansStaleLock(t *testing.T) {
	dir := t.TempDir()
	lockPath := filepath.Join(dir, "test.lock")
	logger := newTestLogger(t)

	// Spawn a short-lived child, wait for it to exit, then use its PID
	// as a guaranteed-stale lock holder.
	cmd := exec.Command("go", "version")
	if err := cmd.Run(); err != nil {
		t.Fatalf("run child: %v", err)
	}
	stalePID := cmd.Process.Pid

	if err := os.WriteFile(lockPath, []byte(fmt.Sprintf("%d\n1234567890\n", stalePID)), 0o644); err != nil {
		t.Fatalf("write stale lock: %v", err)
	}

	release, err := AcquireLock(lockPath, logger)
	if err != nil {
		t.Fatalf("AcquireLock should clean stale lock: %v", err)
	}
	defer release()
}

func TestAcquireLockRejectsCorruptFile(t *testing.T) {
	dir := t.TempDir()
	lockPath := filepath.Join(dir, "test.lock")
	logger := newTestLogger(t)

	if err := os.WriteFile(lockPath, []byte("not-a-pid\n"), 0o644); err != nil {
		t.Fatalf("write corrupt lock: %v", err)
	}

	release, err := AcquireLock(lockPath, logger)
	if err != nil {
		t.Fatalf("AcquireLock should recover from corrupt lock: %v", err)
	}
	defer release()
}

func TestAcquireLockCreatesParentDirectory(t *testing.T) {
	dir := t.TempDir()
	lockPath := filepath.Join(dir, "subdir", "deep", "test.lock")
	logger := newTestLogger(t)

	release, err := AcquireLock(lockPath, logger)
	if err != nil {
		t.Fatalf("AcquireLock: %v", err)
	}
	defer release()

	if _, statErr := os.Stat(lockPath); statErr != nil {
		t.Fatalf("lock file does not exist: %v", statErr)
	}
}

// TestAcquireLockAcceptsLegacyTwoLineFormat keeps backward compatibility for
// lock files written by dreamer versions before the exec-identity field was
// added. The recorded PID is high enough to be unowned in any sane test
// environment, so liveness alone drives the stale-cleanup path.
func TestAcquireLockAcceptsLegacyTwoLineFormat(t *testing.T) {
	dir := t.TempDir()
	lockPath := filepath.Join(dir, "test.lock")
	logger := newTestLogger(t)

	if writeErr := os.WriteFile(lockPath, []byte("4194303\n0\n"), 0o644); writeErr != nil {
		t.Fatalf("write legacy lock: %v", writeErr)
	}

	release, err := AcquireLock(lockPath, logger)
	if err != nil {
		t.Fatalf("AcquireLock should accept legacy two-line format: %v", err)
	}
	defer release()
}

// B4: Verify ExecPathsMatch handles case differences on Windows.
func TestExecPathsMatch_CaseInsensitive(t *testing.T) {
	a := "C:\\Users\\test\\dreamer.exe"
	b := "c:\\users\\test\\dreamer.exe"
	if !ExecPathsMatch(a, b) {
		t.Errorf("ExecPathsMatch should be case-insensitive on Windows: %q vs %q", a, b)
	}
}

func TestExecPathsMatch_ExactMatch(t *testing.T) {
	p := "/usr/local/bin/dreamer"
	if !ExecPathsMatch(p, p) {
		t.Errorf("ExecPathsMatch should match identical paths: %q", p)
	}
}

func TestExecPathsMatch_Different(t *testing.T) {
	a := "/usr/local/bin/dreamer"
	b := "/usr/local/bin/other"
	if ExecPathsMatch(a, b) {
		t.Errorf("ExecPathsMatch should not match different paths: %q vs %q", a, b)
	}
}
