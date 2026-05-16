package logging

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoggerWritesProgressAndIssuesToLoggingFolder(t *testing.T) {
	outputRoot := t.TempDir()

	logger, err := New(outputRoot, "debug")
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

	data, err := os.ReadFile(expectedPath)
	if err != nil {
		t.Fatalf("ReadFile returned error: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, `level=info msg="analysis started" project=example`) {
		t.Fatalf("log missing info progress entry: %s", content)
	}
	if !strings.Contains(content, `level=error msg="analysis failed" err="permission denied"`) {
		t.Fatalf("log missing error issue entry: %s", content)
	}
}

func TestLoggerLevelFiltersDebugEntries(t *testing.T) {
	outputRoot := t.TempDir()

	logger, err := New(outputRoot, "info")
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	logger.Debug("hidden details")
	logger.Info("visible progress")
	if err := logger.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}

	data, err := os.ReadFile(logger.Path())
	if err != nil {
		t.Fatalf("ReadFile returned error: %v", err)
	}
	content := string(data)
	if strings.Contains(content, "hidden details") {
		t.Fatalf("debug entry should be filtered at info level: %s", content)
	}
	if !strings.Contains(content, "visible progress") {
		t.Fatalf("info entry should be written: %s", content)
	}
}
