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
			pid, readErr := fsutil.ReadLockPID(lockPath)
			if readErr != nil {
				cmd.Println("daemon is not running (no lockfile)")
				return nil
			}

			if !fsutil.IsProcessAlive(pid) {
				// Stale lockfile — clean it up.
				_ = os.Remove(lockPath)
				cmd.Println("daemon is not running (stale lockfile removed)")
				return nil
			}

			if killErr := killDaemon(pid); killErr != nil {
				return fmt.Errorf("stop daemon (PID %d): %w", pid, killErr)
			}

			// Wait for lockfile removal (daemon cleans up via signal handler).
			if waitErr := waitForLockfileRemoval(lockPath, lockfileWaitTimeoutLong); waitErr != nil {
				// On Windows, taskkill terminates the process without triggering
				// the signal handler, so the lockfile may not be cleaned up by
				// the daemon itself. Remove it if the process is gone.
				if !fsutil.IsProcessAlive(pid) {
					_ = os.Remove(lockPath)
					cmd.Printf("daemon stopped (PID %d)\n", pid)
					return nil
				}
				cmd.Printf("daemon signaled (PID %d) but lockfile still present; it may take a moment to shut down\n", pid)
				return nil
			}

			cmd.Printf("daemon stopped (PID %d)\n", pid)
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
