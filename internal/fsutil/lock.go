package fsutil

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"dreamer/internal/logging"
)

// AcquireLock creates a PID-based lock file at path. If the lock is already
// held by a live process running the same executable, it returns an error.
// Stale locks — from a crashed process, or from a PID that has since been
// reused by an unrelated program — are automatically cleaned up. The returned
// release function removes the lock file and should be called via defer.
func AcquireLock(path string, logger *logging.Logger) (release func(), err error) {
	parent := filepath.Dir(path)
	if mkErr := os.MkdirAll(parent, DirPerms); mkErr != nil {
		return nil, fmt.Errorf("create lock dir %q: %w", parent, mkErr)
	}

	if err := tryAcquire(path, logger); err != nil {
		return nil, err
	}

	return func() {
		if removeErr := os.Remove(path); removeErr != nil && !os.IsNotExist(removeErr) {
			logger.Warn("failed to remove lock file", logging.Any("path", path), logging.Any("err", removeErr))
		}
		logger.Info("daemon lock released")
	}, nil
}

// maxLockRetries bounds the retry loop so a persistently recreated lock file
// cannot cause unbounded recursion.
const maxLockRetries = 3

func tryAcquire(path string, logger *logging.Logger) error {
	ownExec, _ := os.Executable()
	for attempt := 0; attempt < maxLockRetries; attempt++ {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_EXCL, FilePerms)
		if err == nil {
			payload := fmt.Sprintf("%d\n%d\n%s\n", os.Getpid(), time.Now().Unix(), ownExec)
			if _, writeErr := f.WriteString(payload); writeErr != nil {
				_ = f.Close()
				_ = os.Remove(path)
				return fmt.Errorf("write lock file %q: %w", path, writeErr)
			}
			if syncErr := f.Sync(); syncErr != nil {
				_ = f.Close()
				_ = os.Remove(path)
				return fmt.Errorf("sync lock file %q: %w", path, syncErr)
			}
			_ = f.Close()
			logger.Info("daemon lock acquired", logging.Any("pid", os.Getpid()), logging.Any("path", path))
			return nil
		}
		if !os.IsExist(err) {
			return fmt.Errorf("open lock file %q: %w", path, err)
		}

		// Lock file exists — check liveness, and (when possible) executable
		// identity. PID alone is not enough: after a SIGKILL the OS may reuse
		// the PID for an unrelated program before we run again, which would
		// otherwise jam systemd's Restart=on-failure loop forever. The exec
		// identity check is best-effort: on OSes where processExecutable
		// returns ok=false (darwin/freebsd today) we fall back to PID-only
		// liveness, so the PID-reuse-jam scenario is only fully closed on
		// Linux and Windows.
		existingPID, existingExec, readErr := readLockMetadata(path)
		if readErr != nil {
			logger.Warn("removing corrupt lock file", logging.Any("path", path), logging.Any("err", readErr))
			_ = os.Remove(path)
			continue
		}

		if !isProcessAlive(existingPID) {
			logger.Info("removing stale lock file",
				logging.Any("pid", existingPID),
				logging.Any("path", path),
				logging.Any("reason", "process not alive"),
			)
			_ = os.Remove(path)
			continue
		}

		if existingExec != "" {
			if liveExec, ok := processExecutable(existingPID); ok && !execPathsMatch(liveExec, existingExec) {
				logger.Info("removing stale lock file",
					logging.Any("pid", existingPID),
					logging.Any("path", path),
					logging.Any("recorded_exec", existingExec),
					logging.Any("live_exec", liveExec),
					logging.Any("reason", "PID reused by different executable"),
				)
				_ = os.Remove(path)
				continue
			}
		}

		return fmt.Errorf("daemon already running (PID %d); lock file %s", existingPID, path)
	}
	return fmt.Errorf("failed to acquire lock %q after %d attempts", path, maxLockRetries)
}

// readLockMetadata extracts the PID and (optionally) the recorded executable
// path from a lock file. Files written by older dreamer versions only carry
// "<pid>\n<unix-ts>\n"; in that case execPath is "" and the caller falls back
// to a PID-only liveness check.
func readLockMetadata(path string) (pid int, execPath string, err error) {
	metadataBytes, err := os.ReadFile(path)
	if err != nil {
		return 0, "", fmt.Errorf("read lock file %q: %w", path, err)
	}
	lines := bytes.Split(metadataBytes, []byte("\n"))
	if len(lines) == 0 || len(lines[0]) == 0 {
		return 0, "", fmt.Errorf("lock file %q has no PID", path)
	}
	pid, parseErr := strconv.Atoi(string(lines[0]))
	if parseErr != nil {
		return 0, "", fmt.Errorf("parse PID from lock file %q: %w", path, parseErr)
	}
	if pid <= 0 {
		return 0, "", fmt.Errorf("invalid PID %d in lock file %q", pid, path)
	}
	if len(lines) >= 3 {
		execPath = strings.TrimSpace(string(lines[2]))
	}
	return pid, execPath, nil
}

func execPathsMatch(a, b string) bool {
	// Resolve symlinks so an in-place upgrade still matches the recorded path.
	// Fall back to plain string compare when EvalSymlinks fails (e.g. the
	// recorded binary has since been deleted).
	if ra, err := filepath.EvalSymlinks(a); err == nil {
		a = ra
	}
	if rb, err := filepath.EvalSymlinks(b); err == nil {
		b = rb
	}
	return a == b
}
