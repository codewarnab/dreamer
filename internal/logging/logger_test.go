package logging

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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
