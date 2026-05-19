package fsutil

import (
	"os"
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

	data, readErr := os.ReadFile(lockPath)
	if readErr != nil {
		t.Fatalf("read lock file: %v", readErr)
	}
	if len(data) == 0 {
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

	// Write a lock file with a PID that doesn't exist.
	if err := os.WriteFile(lockPath, []byte("99999999\n1234567890\n"), 0o644); err != nil {
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
