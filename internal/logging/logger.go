package logging

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"dreamer/internal/errs"
)

const (
	defaultLogFileName    = "dreamer.log"
	backupLogFileName     = "dreamer.log.1"
	loggingDirName        = "logging"
	defaultMaxSizeMB      = 5
	bytesPerMB            = 1024 * 1024
)

// Logger writes progress and issue details to a project-local Dreamer log file.
//
// The logger keeps file handling small and explicit: every command run opens one
// append-only log file under the configured output root, and callers close it
// when the command finishes. Log levels are intentionally simple because Dreamer
// currently needs durable progress/error breadcrumbs more than a full logging
// framework.
type Logger struct {
	file      *os.File
	logger    *slog.Logger
	level     slog.Level
	path      string
	maxSizeMB int
}

// Attr is one structured logging field.
type Attr = slog.Attr

// Any builds a structured field for Logger methods.
func Any(key string, value any) Attr {
	return slog.Any(key, value)
}

// String builds a string-typed structured field for Logger methods.
func String(key string, value string) Attr {
	return slog.String(key, value)
}

// ErrAttr returns a slice of structured attributes for err. Tagged *errs.Error
// values emit errKind, provider, op, and each Details entry as err.<key>.
// Untagged errors degrade to a single err attribute.
func ErrAttr(err error) []Attr {
	attrs := []Attr{Any("err", err)}
	var e *errs.Error
	if errors.As(err, &e) {
		attrs = append(attrs,
			String("errKind", string(e.Kind)),
			String("provider", e.Provider),
			String("op", e.Op),
		)
		for k, v := range e.Details {
			attrs = append(attrs, Any("err."+k, v))
		}
	}
	return attrs
}

// New creates a logger that writes to "<outputRoot>/logging/dreamer.log".
//
// The outputRoot argument should already be expanded and validated by config
// loading. The level argument accepts "error", "warn", "info", or "debug";
// unknown values fall back to "info" so logging remains available even when a
// config file contains a typo.
func New(outputRoot string, level string, maxSizeMB int) (*Logger, error) {
	if maxSizeMB <= 0 {
		maxSizeMB = defaultMaxSizeMB
	}
	logDir := filepath.Join(outputRoot, loggingDirName)
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		return nil, fmt.Errorf("create logging directory %q: %w", logDir, err)
	}

	logPath := filepath.Join(logDir, defaultLogFileName)
	file, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open log file %q: %w", logPath, err)
	}

	minLevel := parseLevel(level)
	handler := slog.NewTextHandler(io.MultiWriter(file, os.Stderr), &slog.HandlerOptions{
		Level: minLevel,
		ReplaceAttr: func(groups []string, attr slog.Attr) slog.Attr {
			if attr.Key == slog.LevelKey {
				return slog.String(slog.LevelKey, strings.ToLower(attr.Value.String()))
			}
			return attr
		},
	})
	return &Logger{file: file, logger: slog.New(handler), level: minLevel, path: logPath, maxSizeMB: maxSizeMB}, nil
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
func (logger *Logger) Error(message string, attrs ...Attr) {
	logger.write(slog.LevelError, message, attrs...)
}

// Warn records a recoverable issue that did not stop the command.
func (logger *Logger) Warn(message string, attrs ...Attr) {
	logger.write(slog.LevelWarn, message, attrs...)
}

// Info records normal command progress.
func (logger *Logger) Info(message string, attrs ...Attr) {
	logger.write(slog.LevelInfo, message, attrs...)
}

// Debug records detailed troubleshooting information.
func (logger *Logger) Debug(message string, attrs ...Attr) {
	logger.write(slog.LevelDebug, message, attrs...)
}

func (logger *Logger) write(level slog.Level, message string, attrs ...Attr) {
	if logger == nil || logger.logger == nil || level < logger.level {
		return
	}
	if logger.rotateIfNeeded() {
		// The file was rotated; rebuild the slog handler so it writes to the
		// new file descriptor. The old MultiWriter still references the closed fd.
		logger.rebuildHandler()
	}
	args := make([]any, 0, len(attrs))
	for _, attr := range attrs {
		args = append(args, attr)
	}
	logger.logger.Log(context.Background(), level, message, args...)
}

// rotateIfNeeded checks whether the log file exceeds the configured size limit
// and rotates it by renaming the current file to .1 and opening a fresh one.
// Returns true if rotation happened.
func (logger *Logger) rotateIfNeeded() bool {
	if logger.maxSizeMB <= 0 || logger.file == nil {
		return false
	}
	info, err := logger.file.Stat()
	if err != nil || info.Size() < int64(logger.maxSizeMB)*bytesPerMB {
		return false
	}

	backupPath := filepath.Join(filepath.Dir(logger.path), backupLogFileName)
	_ = logger.file.Close()
	_ = os.Remove(backupPath)
	_ = os.Rename(logger.path, backupPath)

	file, openErr := os.OpenFile(logger.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if openErr != nil {
		logger.file = nil
		return false
	}
	logger.file = file
	return true
}

// rebuildHandler creates a new slog handler that writes to the current file.
func (logger *Logger) rebuildHandler() {
	handler := slog.NewTextHandler(io.MultiWriter(logger.file, os.Stderr), &slog.HandlerOptions{
		Level: logger.level,
		ReplaceAttr: func(groups []string, attr slog.Attr) slog.Attr {
			if attr.Key == slog.LevelKey {
				return slog.String(slog.LevelKey, strings.ToLower(attr.Value.String()))
			}
			return attr
		},
	})
	logger.logger = slog.New(handler)
}

func parseLevel(value string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "error":
		return slog.LevelError
	case "warn", "warning":
		return slog.LevelWarn
	case "debug":
		return slog.LevelDebug
	default:
		return slog.LevelInfo
	}
}
