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
	"sync"
	"time"

	"dreamer/internal/errs"
)

const (
	defaultLogFileName    = "dreamer.log"
	backupLogFileName     = "dreamer.log.1"
	loggingDirName        = "logging"
	defaultMaxSizeMB      = 5
	bytesPerMB            = 1024 * 1024
	rotationRetryInterval = 30 * time.Second
)

// Logger writes progress and issue details to a project-local Dreamer log file.
//
// The logger keeps file handling small and explicit: every command run opens one
// append-only log file under the configured output root, and callers close it
// when the command finishes. Log levels are intentionally simple because Dreamer
// currently needs durable progress/error breadcrumbs more than a full logging
// framework.
type Logger struct {
	mu                 sync.Mutex
	file               *os.File
	handler            *slog.Logger
	level              slog.Level
	path               string
	maxSizeMB          int
	rotationRetryAfter time.Time
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
	file, err := openLogAppend(logPath)
	if err != nil {
		return nil, fmt.Errorf("open log file %q: %w", logPath, err)
	}

	minLevel := parseLevel(level)
	handler := slog.NewTextHandler(io.MultiWriter(file, os.Stderr), &slog.HandlerOptions{
		Level:       minLevel,
		ReplaceAttr: replaceAttrLowerLevel,
	})
	return &Logger{file: file, handler: slog.New(handler), level: minLevel, path: logPath, maxSizeMB: maxSizeMB}, nil
}

// Silent returns a logger that discards all output. Useful for CLI commands
// that need a logger for the Store/RunStore but don't want log files.
func Silent() *Logger {
	handler := slog.NewTextHandler(io.Discard, &slog.HandlerOptions{
		Level: slog.LevelError + 1, // above Error = nothing logged
	})
	return &Logger{handler: slog.New(handler), level: slog.LevelError + 1}
}

// Path returns the absolute log file path used by this Logger.
func (l *Logger) Path() string {
	if l == nil {
		return ""
	}
	return l.path
}

// Close flushes and closes the underlying log file. Subsequent
// Info/Warn/Error/Debug calls become no-ops so racing callers cannot drive
// writes through a closed file descriptor after the daemon has shut down.
func (l *Logger) Close() error {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.handler = nil
	l.rotationRetryAfter = time.Time{}
	if l.file == nil {
		return nil
	}
	err := l.file.Close()
	l.file = nil
	return err
}

// Error records a command issue or failure.
func (l *Logger) Error(message string, attrs ...Attr) {
	l.write(slog.LevelError, message, attrs...)
}

// Warn records a recoverable issue that did not stop the command.
func (l *Logger) Warn(message string, attrs ...Attr) {
	l.write(slog.LevelWarn, message, attrs...)
}

// Info records normal command progress.
func (l *Logger) Info(message string, attrs ...Attr) {
	l.write(slog.LevelInfo, message, attrs...)
}

// Debug records detailed troubleshooting information.
func (l *Logger) Debug(message string, attrs ...Attr) {
	l.write(slog.LevelDebug, message, attrs...)
}

func (l *Logger) write(level slog.Level, message string, attrs ...Attr) {
	if l == nil || level < l.level {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.handler == nil {
		return
	}
	if l.rotateIfNeededLocked() {
		l.rebuildHandlerLocked()
	}
	// LogAttrs avoids []any allocation by accepting ...Attr directly
	l.handler.LogAttrs(context.Background(), level, message, attrs...)
}

// rotateIfNeededLocked checks whether the log file exceeds the configured size
// limit and rotates it by renaming the current file to .1 and opening a fresh
// one. If rename fails (e.g. on Windows due to an external viewer locking the
// file), it falls back to a timestamped backup name, and if that also fails,
// it applies a retry cooldown to prevent log thrashing and stderr spam.
// Returns true if rotation happened or the file handle was refreshed. Caller must hold l.mu.
func (l *Logger) rotateIfNeededLocked() bool {
	if l.maxSizeMB <= 0 || l.file == nil {
		return false
	}
	if !l.rotationRetryAfter.IsZero() && time.Now().Before(l.rotationRetryAfter) {
		return false
	}
	fileInfo, err := l.file.Stat()
	if err != nil || fileInfo.Size() < int64(l.maxSizeMB)*bytesPerMB {
		return false
	}

	backupPath := filepath.Join(filepath.Dir(l.path), backupLogFileName)
	if err := l.file.Close(); err != nil {
		fmt.Fprintf(os.Stderr, "log rotation: close current log failed: %v\n", err)
	}
	if rmErr := os.Remove(backupPath); rmErr != nil && !os.IsNotExist(rmErr) {
		// Non-fatal; if backupPath is locked, fallback timestamped rename will be attempted.
	}
	renameErr := os.Rename(l.path, backupPath)
	if renameErr != nil {
		// If the primary backup path is locked by another process, try a unique timestamped backup.
		timestampedBackup := fmt.Sprintf("%s.%s", l.path, time.Now().UTC().Format("20060102-150405"))
		if fallbackErr := os.Rename(l.path, timestampedBackup); fallbackErr == nil {
			renameErr = nil
		}
	}
	if renameErr != nil {
		fmt.Fprintf(os.Stderr, "log rotation: rename %s -> %s failed: %v\n", l.path, backupPath, renameErr)
		l.rotationRetryAfter = time.Now().Add(rotationRetryInterval)
	} else {
		l.rotationRetryAfter = time.Time{}
	}

	file, openErr := openLogAppend(l.path)
	if openErr != nil {
		l.file = nil
		return true
	}
	l.file = file
	return true
}

// rebuildHandlerLocked creates a new slog handler that writes to the current
// file (or stderr-only if rotation failed to reopen). Caller must hold
// l.mu.
func (l *Logger) rebuildHandlerLocked() {
	var writer io.Writer = os.Stderr
	if l.file != nil {
		writer = io.MultiWriter(l.file, os.Stderr)
	}
	handler := slog.NewTextHandler(writer, &slog.HandlerOptions{
		Level:       l.level,
		ReplaceAttr: replaceAttrLowerLevel,
	})
	l.handler = slog.New(handler)
}

// replaceAttrLowerLevel normalizes the level attribute to lowercase.
// Shared by New and rebuildHandlerLocked.
func replaceAttrLowerLevel(_ []string, attr slog.Attr) slog.Attr {
	if attr.Key == slog.LevelKey {
		return slog.String(slog.LevelKey, strings.ToLower(attr.Value.String()))
	}
	return attr
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
