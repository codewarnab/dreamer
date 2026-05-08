package logging

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
)

const (
	defaultLogFileName = "dreamer.log"
	loggingDirName     = "logging"
)

// Logger writes progress and issue details to a project-local Dreamer log file.
//
// The logger keeps file handling small and explicit: every command run opens one
// append-only log file under the configured output root, and callers close it
// when the command finishes. Log levels are intentionally simple because Dreamer
// currently needs durable progress/error breadcrumbs more than a full logging
// framework.
type Logger struct {
	file   *os.File
	logger *log.Logger
	level  logLevel
	path   string
}

type logLevel int

const (
	levelError logLevel = iota
	levelWarn
	levelInfo
	levelDebug
)

// New creates a logger that writes to "<outputRoot>/logging/dreamer.log".
//
// The outputRoot argument should already be expanded and validated by config
// loading. The level argument accepts "error", "warn", "info", or "debug";
// unknown values fall back to "info" so logging remains available even when a
// config file contains a typo.
func New(outputRoot string, level string) (*Logger, error) {
	logDir := filepath.Join(outputRoot, loggingDirName)
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		return nil, fmt.Errorf("create logging directory %q: %w", logDir, err)
	}

	logPath := filepath.Join(logDir, defaultLogFileName)
	file, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open log file %q: %w", logPath, err)
	}

	return &Logger{
		file:   file,
		logger: log.New(file, "", log.LstdFlags|log.LUTC),
		level:  parseLevel(level),
		path:   logPath,
	}, nil
}

// Path returns the absolute log file path used by this logger.
func (logger *Logger) Path() string {
	if logger == nil {
		return ""
	}
	return logger.path
}

// Close flushes and closes the underlying log file.
func (logger *Logger) Close() error {
	if logger == nil || logger.file == nil {
		return nil
	}
	return logger.file.Close()
}

// Error records a command issue or failure.
func (logger *Logger) Error(format string, args ...any) {
	logger.write(levelError, "ERROR", format, args...)
}

// Warn records a recoverable issue that did not stop the command.
func (logger *Logger) Warn(format string, args ...any) {
	logger.write(levelWarn, "WARN", format, args...)
}

// Info records normal command progress.
func (logger *Logger) Info(format string, args ...any) {
	logger.write(levelInfo, "INFO", format, args...)
}

// Debug records detailed troubleshooting information.
func (logger *Logger) Debug(format string, args ...any) {
	logger.write(levelDebug, "DEBUG", format, args...)
}

func (logger *Logger) write(level logLevel, label string, format string, args ...any) {
	if logger == nil || logger.logger == nil || level > logger.level {
		return
	}
	logger.logger.Printf("%s %s", label, fmt.Sprintf(format, args...))
}

func parseLevel(value string) logLevel {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "error":
		return levelError
	case "warn", "warning":
		return levelWarn
	case "debug":
		return levelDebug
	default:
		return levelInfo
	}
}
