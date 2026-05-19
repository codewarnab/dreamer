package fsutil

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"dreamer/internal/logging"
)

const (
	lockDirPerms  = 0o755
	lockFilePerms = 0o644
)

// AcquireLock creates a PID-based lock file at path. If the lock is already
// held by a live process, it returns an error. Stale locks (from a crashed
// process) are automatically cleaned up. The returned release function removes
// the lock file and should be called via defer.
func AcquireLock(path string, logger *logging.Logger) (release func(), err error) {
	parent := filepath.Dir(path)
	if mkErr := os.MkdirAll(parent, lockDirPerms); mkErr != nil {
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
	for attempt := 0; attempt < maxLockRetries; attempt++ {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_EXCL, lockFilePerms)
		if err == nil {
			payload := fmt.Sprintf("%d\n%d\n", os.Getpid(), time.Now().Unix())
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

		// Lock file exists — check if holder is still alive.
		existingPID, readErr := readLockPID(path)
		if readErr != nil {
			logger.Warn("removing corrupt lock file", logging.Any("path", path), logging.Any("err", readErr))
			_ = os.Remove(path)
			continue
		}

		if isProcessAlive(existingPID) {
			return fmt.Errorf("daemon already running (PID %d); lock file %s", existingPID, path)
		}

		logger.Info("removing stale lock file", logging.Any("pid", existingPID), logging.Any("path", path))
		_ = os.Remove(path)
	}
	return fmt.Errorf("failed to acquire lock %q after %d attempts", path, maxLockRetries)
}

func readLockPID(path string) (int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, fmt.Errorf("read lock file %q: %w", path, err)
	}
	lines := bytes.SplitN(data, []byte("\n"), 2)
	if len(lines) == 0 || len(lines[0]) == 0 {
		return 0, fmt.Errorf("lock file %q has no PID", path)
	}
	pid, err := strconv.Atoi(string(lines[0]))
	if err != nil {
		return 0, fmt.Errorf("parse PID from lock file %q: %w", path, err)
	}
	if pid <= 0 {
		return 0, fmt.Errorf("invalid PID %d in lock file %q", pid, path)
	}
	return pid, nil
}
