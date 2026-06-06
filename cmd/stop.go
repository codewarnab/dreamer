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
						if killErr := killDaemon(pid); killErr == nil {
							// Wait for lockfile removal (daemon cleans up via signal handler).
							_ = waitForLockfileRemoval(lockPath, lockfileWaitTimeoutLong)
							if !fsutil.IsProcessAlive(pid) {
								_ = os.Remove(lockPath)
								cmd.Printf("daemon stopped (PID %d)\n", pid)
								daemonStopped = true
							}
						}
					} else {
						_ = os.Remove(lockPath)
						cmd.Println("cleaned up stale daemon lockfile")
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
				if p, err := FindPIDByPort(port); err == nil && p > 0 {
					cmd.Printf("stopping active server process (PID %d)...\n", p)
					if killErr := killDaemon(p); killErr == nil {
						cmd.Printf("server stopped (PID %d)\n", p)
						return nil
					} else {
						return fmt.Errorf("stop process (PID %d): %w", p, killErr)
					}
				} else {
					cmd.Printf("found active server on port %d, but could not determine its PID. Please kill it manually.\n", port)
					return nil
				}
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
