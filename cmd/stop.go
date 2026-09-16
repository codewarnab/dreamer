package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"dreamer/internal/config"
	"dreamer/internal/fsutil"
	"github.com/spf13/cobra"
)

func newStopCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "stop",
		Short: "Stop the running daemon.",
		Long:  "Shut down the dreamer daemon by sending a termination signal.",
		RunE: func(cmd *cobra.Command, args []string) error {
			resolvedConfigPath, err := resolveConfigPath(configPath)
			if err != nil {
				return err
			}
			overlayPath, _ := config.GlobalOverlayPath()
			cfg, err := config.LoadConfigWithOverlay(resolvedConfigPath, overlayPath)
			if err != nil {
				return fmt.Errorf("load config %q: %w", resolvedConfigPath, err)
			}

			lockPath := filepath.Join(cfg.Daemon.OutputRoot, "dreamer.daemon.lock")
			stopFilePath := daemonStopFilePath(cfg.Daemon.OutputRoot)
			pid, lockExec, readErr := fsutil.ReadLockMetadata(lockPath)
			daemonStopped := false

			if readErr == nil {
				if fsutil.IsProcessAlive(pid) {
					// Verify the PID still belongs to our executable. If the
					// daemon crashed and the PID was reused by another process,
					// killing it would be dangerous. Compare against the live
					// process image, not our own binary path.
					// When lockExec is empty (legacy lock file without exec
					// path), we cannot verify identity — treat conservatively
					// and do not kill the process.
					if lockExec != "" && (func() bool {
						liveExec, ok := fsutil.ProcessExecutable(pid)
						return ok && fsutil.ExecPathsMatch(lockExec, liveExec)
					})() {
						cmd.Printf("stopping daemon (PID %d)...\n", pid)
						// Prefer the graceful stop-file handshake: on Windows
						// a detached daemon cannot receive SIGTERM and
						// taskkill /F skips state flushing and cleanup.
						if requestGracefulStop(stopFilePath, lockPath, gracefulStopTimeout) {
							if !fsutil.IsProcessAlive(pid) {
								_ = os.Remove(lockPath)
								cmd.Printf("daemon stopped gracefully (PID %d)\n", pid)
								daemonStopped = true
							}
						}
						if !daemonStopped {
							cmd.Printf("graceful stop did not complete in %s; forcing termination\n", gracefulStopTimeout)
							if killErr := killDaemon(pid); killErr != nil {
								return fmt.Errorf("stop daemon process (PID %d): %w", pid, killErr)
							}
							// Wait for lockfile removal (daemon cleans up via signal handler),
							// but success ultimately depends on the process exiting.
							_ = waitForLockfileRemoval(lockPath, lockfileWaitTimeoutLong)
							if waitErr := waitForProcessExit(pid, lockfileWaitTimeoutLong); waitErr != nil {
								return fmt.Errorf("daemon process (PID %d) survived termination: %w", pid, waitErr)
							}
							_ = os.Remove(lockPath)
							cmd.Printf("daemon stopped (PID %d)\n", pid)
							daemonStopped = true
						}
					} else {
						// lockExec is empty (legacy lock) and the process is alive:
						// we cannot verify ownership, so leave the lock in place and
						// warn the operator rather than silently orphaning the daemon.
						if lockExec == "" {
							cmd.Printf("warning: daemon (PID %d) is running but lock file has no executable path; cannot verify ownership — leaving lock in place\n", pid)
						} else {
							_ = os.Remove(lockPath)
							cmd.Println("cleaned up stale daemon lockfile")
						}
					}
				} else {
					// Stale lockfile — clean it up.
					_ = os.Remove(lockPath)
					cmd.Println("cleaned up stale daemon lockfile")
				}
			}

			// 2. Resolve port and check if there's any active web server on it (daemon or standalone)
			port, err := resolveWebPort(cfg)
			if err != nil {
				port = config.DefaultWebPort
			}

			url := fmt.Sprintf("http://127.0.0.1:%d", port)
			if probeHealth(url+"/api/health", 200*time.Millisecond) == nil {
				cmd.Printf("active server detected on port %d\n", port)
				p, findErr := FindPIDByPort(port)
				if findErr != nil || p <= 0 {
					return fmt.Errorf("active server on port %d could not be identified; refusing to signal it: %w", port, findErr)
				}

				// A health response proves only that some HTTP server owns the port.
				// Before signaling a PID discovered from the socket table, require its
				// live process image to match this Dreamer executable. This protects
				// unrelated services and closes stale-PID/reuse races at the fallback.
				expectedExec, execErr := os.Executable()
				if execErr != nil {
					return fmt.Errorf("resolve Dreamer executable before stopping PID %d: %w", p, execErr)
				}
				if verifyErr := verifyDreamerProcess(p, expectedExec); verifyErr != nil {
					return fmt.Errorf("active server on port %d is not a verified Dreamer process: %w", port, verifyErr)
				}

				cmd.Printf("stopping verified Dreamer server process (PID %d)...\n", p)
				// Re-check immediately before a potentially process-group-wide signal.
				if verifyErr := verifyDreamerProcess(p, expectedExec); verifyErr != nil {
					return fmt.Errorf("Dreamer process identity changed before termination (PID %d): %w", p, verifyErr)
				}
				if killErr := killDaemon(p); killErr != nil {
					return fmt.Errorf("stop verified Dreamer process (PID %d): %w", p, killErr)
				}
				if waitErr := waitForProcessExit(p, lockfileWaitTimeoutLong); waitErr != nil {
					return fmt.Errorf("verified Dreamer process (PID %d) survived termination: %w", p, waitErr)
				}
				cmd.Printf("server stopped (PID %d)\n", p)
				return nil
			}

			if daemonStopped {
				return nil
			}

			if readErr != nil {
				cmd.Println("daemon is not running (no active server or lockfile found)")
			} else {
				cmd.Println("daemon is not running")
			}
			return nil
		},
	}

	return command
}

// waitForLockfileRemoval polls until the lockfile disappears or timeout.
func waitForLockfileRemoval(lockPath string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(lockPath); os.IsNotExist(err) {
			return nil
		}
		time.Sleep(lockfilePollIntervalSlow)
	}
	return fmt.Errorf("timeout waiting for lockfile removal")
}

// verifyDreamerProcess requires a readable live executable and an exact
// canonical-path match. A PID or HTTP response alone is never process identity.
func verifyDreamerProcess(pid int, expectedExec string) error {
	if pid <= 0 {
		return fmt.Errorf("invalid PID %d", pid)
	}
	liveExec, ok := fsutil.ProcessExecutable(pid)
	if !ok {
		return fmt.Errorf("cannot read executable identity for PID %d", pid)
	}
	if !fsutil.ExecPathsMatch(expectedExec, liveExec) {
		return fmt.Errorf("PID %d executable %q does not match Dreamer executable %q", pid, liveExec, expectedExec)
	}
	return nil
}

func waitForProcessExit(pid int, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if !fsutil.IsProcessAlive(pid) {
			return nil
		}
		time.Sleep(lockfilePollIntervalSlow)
	}
	return fmt.Errorf("timeout waiting for PID %d to exit", pid)
}
