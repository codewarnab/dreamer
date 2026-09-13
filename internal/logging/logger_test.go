package logging

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLoggerWritesProgressAndIssuesToLoggingFolder(t *testing.T) {
	outputRoot := t.TempDir()

	logger, err := New(outputRoot, "debug", 0)
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	logger.Info("analysis started", Any("project", "example"))
	logger.Error("analysis failed", Any("err", os.ErrPermission))
	if err := logger.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}

	expectedPath := filepath.Join(outputRoot, loggingDirName, defaultLogFileName)
	if logger.Path() != expectedPath {
		t.Fatalf("Path = %q, want %q", logger.Path(), expectedPath)
	}

	logFileBytes, err := os.ReadFile(expectedPath)
	if err != nil {
		t.Fatalf("ReadFile returned error: %v", err)
	}
	content := string(logFileBytes)
	if !strings.Contains(content, `level=info msg="analysis started" project=example`) {
		t.Fatalf("log missing info progress entry: %s", content)
	}
	if !strings.Contains(content, `level=error msg="analysis failed" err="permission denied"`) {
		t.Fatalf("log missing error issue entry: %s", content)
	}
}

func TestLoggerLevelFiltersDebugEntries(t *testing.T) {
	outputRoot := t.TempDir()

	logger, err := New(outputRoot, "info", 0)
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	logger.Debug("hidden details")
	logger.Info("visible progress")
	if err := logger.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}

	logFileBytes, err := os.ReadFile(logger.Path())
	if err != nil {
		t.Fatalf("ReadFile returned error: %v", err)
	}
	content := string(logFileBytes)
	if strings.Contains(content, "hidden details") {
		t.Fatalf("debug entry should be filtered at info level: %s", content)
	}
	if !strings.Contains(content, "visible progress") {
		t.Fatalf("info entry should be written: %s", content)
	}
}

func TestLoggerRotatesWhenSizeExceeded(t *testing.T) {
	outputRoot := t.TempDir()
	// Use 1MB max size (the minimum config value).
	logger, err := New(outputRoot, "info", 1)
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}

	// Each slog line is ~100 bytes; write 15000 lines to exceed 1MB.
	for i := 0; i < 15000; i++ {
		logger.Info("padding line for rotation test", Any("index", i))
	}
	if err := logger.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}

	logDir := filepath.Join(outputRoot, loggingDirName)
	backupPath := filepath.Join(logDir, backupLogFileName)

	// The backup should exist (rotation happened).
	if _, statErr := os.Stat(backupPath); os.IsNotExist(statErr) {
		t.Fatal("backup log file should exist after rotation")
	}

	// The current log should exist and contain recent entries.
	currentData, readErr := os.ReadFile(filepath.Join(logDir, defaultLogFileName))
	if readErr != nil {
		t.Fatalf("read current log: %v", readErr)
	}
	if len(currentData) == 0 {
		t.Fatal("current log file should not be empty after rotation")
	}
}

// TestLoggerWritesAfterCloseAreNoOps guards against a regression where a late
// goroutine's log call could be silently routed through a closed file
// descriptor; the daemon's deferred Close runs LIFO with other shutdown
// cleanup, so this used to be possible in principle.
func TestLoggerWritesAfterCloseAreNoOps(t *testing.T) {
	outputRoot := t.TempDir()
	logger, err := New(outputRoot, "info", 0)
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	if err := logger.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}

	// Each of these must not panic, must not write, and must not race.
	logger.Info("post-close info")
	logger.Warn("post-close warn")
	logger.Error("post-close error")
	logger.Debug("post-close debug")

	// A second Close must remain idempotent.
	if err := logger.Close(); err != nil {
		t.Fatalf("second Close returned error: %v", err)
	}
}

func TestLoggerRotationDisabledByDefault(t *testing.T) {
	outputRoot := t.TempDir()
	// maxSizeMB=0 means default (5MB). Writing a few lines won't trigger rotation.
	logger, err := New(outputRoot, "info", 0)
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	logger.Info("test message")
	if err := logger.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}

	logDir := filepath.Join(outputRoot, loggingDirName)
	backupPath := filepath.Join(logDir, backupLogFileName)

	if _, statErr := os.Stat(backupPath); !os.IsNotExist(statErr) {
		t.Fatal("backup log should not exist when rotation threshold not reached")
	}
}

func TestOpenRead(t *testing.T) {
	tempDir := t.TempDir()
	logPath := filepath.Join(tempDir, "test.log")

	f, err := openLogAppend(logPath)
	if err != nil {
		t.Fatalf("openLogAppend failed: %v", err)
	}
	content := "hello world\n"
	if _, err := f.WriteString(content); err != nil {
		_ = f.Close()
		t.Fatalf("WriteString failed: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	rf, err := OpenRead(logPath)
	if err != nil {
		t.Fatalf("OpenRead failed: %v", err)
	}
	defer rf.Close()

	data, err := io.ReadAll(rf)
	if err != nil {
		t.Fatalf("ReadAll failed: %v", err)
	}
	if string(data) != content {
		t.Fatalf("got %q, want %q", string(data), content)
	}
}

func TestLoggerRotationFallbackToTimestampWhenBackupPathBlocked(t *testing.T) {
	outputRoot := t.TempDir()
	logDir := filepath.Join(outputRoot, loggingDirName)
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		t.Fatalf("MkdirAll failed: %v", err)
	}

	// Create a non-empty directory at backupLogFileName so os.Remove and os.Rename will fail on it.
	backupDir := filepath.Join(logDir, backupLogFileName)
	if err := os.MkdirAll(backupDir, 0o755); err != nil {
		t.Fatalf("MkdirAll backupDir failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(backupDir, "blocker.txt"), []byte("locked"), 0o644); err != nil {
		t.Fatalf("Write blocker failed: %v", err)
	}

	logger, err := New(outputRoot, "info", 1)
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	for i := 0; i < 15000; i++ {
		logger.Info("padding line for fallback rotation test", Any("index", i))
	}
	if err := logger.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	// Verify that a timestamped fallback backup was created in logDir.
	entries, err := os.ReadDir(logDir)
	if err != nil {
		t.Fatalf("ReadDir failed: %v", err)
	}
	var foundFallback bool
	prefix := defaultLogFileName + "."
	for _, entry := range entries {
		if entry.Name() != defaultLogFileName && entry.Name() != backupLogFileName && strings.HasPrefix(entry.Name(), prefix) {
			foundFallback = true
			break
		}
	}
	if !foundFallback {
		t.Fatal("expected timestamped fallback backup to be created when backupLogFileName is blocked")
	}
}

func TestLoggerRotationRetryBackoff(t *testing.T) {
	outputRoot := t.TempDir()
	logger, err := New(outputRoot, "info", 1)
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}
	defer logger.Close()

	// Simulate that rotationRetryAfter is set in the future.
	logger.mu.Lock()
	logger.rotationRetryAfter = time.Now().Add(10 * time.Minute)
	// Even with file size > 1MB, rotation should be bypassed due to cooldown.
	rotated := logger.rotateIfNeededLocked()
	logger.mu.Unlock()

	if rotated {
		t.Fatal("expected rotateIfNeededLocked to return false during cooldown window")
	}
}

