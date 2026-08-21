package cmd

import (
	"context"
	"os"
	"path/filepath"
	"time"

	"dreamer/internal/fsutil"
	"dreamer/internal/logging"
)

// daemonStopFileName is the sentinel file that requests a graceful daemon
// shutdown. Windows cannot deliver SIGTERM to a detached process, and
// taskkill /F terminates without cleanup — so the stop command writes this
// file into the daemon output root and the daemon shuts down its context
// cleanly, exactly as if it had received Ctrl+C.
//
// The file lives next to dreamer.daemon.lock and requires the same local
// access level: only a process that could write the lockfile can request a
// stop, so no new trust boundary is introduced.
const daemonStopFileName = "dreamer.daemon.stop"

// stopFilePollInterval is how often the daemon checks for the stop file.
// A stat every 500ms is negligible against the daemon's hourly cycle.
const stopFilePollInterval = 500 * time.Millisecond

// gracefulStopTimeout bounds how long the stop command waits for the daemon
// to shut down after writing the stop file before falling back to taskkill.
// Sized to cover workerShutdownGrace (10s) + webShutdownTimeout (5s) plus
// queue drain overhead.
const gracefulStopTimeout = 20 * time.Second

// daemonStopFilePath returns the stop-file path inside the daemon output root.
func daemonStopFilePath(outputRoot string) string {
	return filepath.Join(outputRoot, daemonStopFileName)
}

// watchDaemonStopFile polls for the stop sentinel file until ctx is done.
// When the file appears it is removed (so a later daemon start does not
// immediately shut down again) and trigger is called exactly once, which
// cancels the daemon's signal-aware context and runs the normal graceful
// shutdown path.
func watchDaemonStopFile(ctx context.Context, logger *logging.Logger, path string, interval time.Duration, trigger func()) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if _, err := os.Stat(path); err != nil {
				continue
			}
			// Remove first so an interrupted shutdown cannot leave the
			// sentinel behind for the next daemon instance.
			if rmErr := os.Remove(path); rmErr != nil {
				logger.Warn("failed to remove daemon stop file", logging.Any("path", path), logging.Any("err", rmErr))
			}
			logger.Info("daemon stop requested via stop file", logging.Any("path", path))
			trigger()
			return
		}
	}
}

// requestGracefulStop writes the stop sentinel file and waits up to timeout
// for lockPath to disappear (the daemon removes it during shutdown). Returns
// true when the daemon exited gracefully; false when the caller should fall
// back to force-kill. The stop file is removed if the daemon never picked it
// up (e.g. it was already dead or wedged).
func requestGracefulStop(stopPath, lockPath string, timeout time.Duration) bool {
	if writeErr := fsutil.WriteFileAtomic(stopPath, []byte(time.Now().UTC().Format(time.RFC3339)), fsutil.FilePerms); writeErr != nil {
		return false
	}
	if waitErr := waitForLockfileRemoval(lockPath, timeout); waitErr != nil {
		// Daemon did not exit in time — clear the sentinel so it does not
		// stop a future daemon started in the meantime.
		_ = os.Remove(stopPath)
		return false
	}
	return true
}
