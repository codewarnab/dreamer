package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"dreamer/internal/config"
	"dreamer/internal/fsutil"
	"github.com/spf13/cobra"
)

func newStartCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "start",
		Short: "Start the daemon in the background.",
		Long: "Start the dreamer analysis daemon as a background process. " +
			"The daemon runs independently of this terminal session. " +
			"Use 'dreamer stop' to shut it down.",
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

			exePath, err := os.Executable()
			if err != nil {
				return fmt.Errorf("resolve executable path: %w", err)
			}

			outputRoot := cfg.Daemon.OutputRoot
			logPath := filepath.Join(outputRoot, "dreamer.log")
			lockPath := filepath.Join(outputRoot, "dreamer.daemon.lock")

			// Check if already running.
			if pid, readErr := fsutil.ReadLockPID(lockPath); readErr == nil && fsutil.IsProcessAlive(pid) {
				printAlreadyRunningBox(cmd, pid, logPath, cfg)
				return nil
			}

			// Prepare log file for child stdout/stderr.
			if mkErr := os.MkdirAll(outputRoot, fsutil.DirPerms); mkErr != nil {
				return fmt.Errorf("create output dir %q: %w", outputRoot, mkErr)
			}
			logFile, openErr := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, fsutil.FilePerms)
			if openErr != nil {
				return fmt.Errorf("open log file %q: %w", logPath, openErr)
			}
			defer logFile.Close()

			// Build child command.
			daemonArgs := []string{"daemon", "--config", resolvedConfigPath}
			child := exec.Command(exePath, daemonArgs...)
			child.Stdout = logFile
			child.Stderr = logFile
			child.SysProcAttr = detachedProcessAttr()

			if startErr := child.Start(); startErr != nil {
				return fmt.Errorf("start daemon process: %w", startErr)
			}

			// Poll lockfile to confirm the daemon started successfully.
			daemonPID := waitForLockfile(lockPath, lockfileWaitTimeoutShort)
			if daemonPID == 0 {
				return fmt.Errorf("daemon process started (PID %d) but did not write lockfile; check %s", child.Process.Pid, logPath)
			}

			printStartedBox(cmd, daemonPID, logPath, cfg)
			return nil
		},
	}

	return command
}

// waitForLockfile polls the lockfile for up to timeout, returning the PID once
// it appears and contains a live process. Returns 0 on timeout.
func waitForLockfile(lockPath string, timeout time.Duration) int {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if pid, err := fsutil.ReadLockPID(lockPath); err == nil && pid > 0 {
			return pid
		}
		time.Sleep(lockfilePollIntervalFast)
	}
	return 0
}

func printAlreadyRunningBox(cmd *cobra.Command, pid int, logPath string, cfg *config.Config) {
	pidStr := fmt.Sprintf("PID: %d", pid)
	lines := []string{
		"daemon is already running",
		pidStr,
		"",
		"stop:     dreamer stop",
		"status:   dreamer status",
		"logs:     " + logPath,
	}
	if cfg.Web.Enabled != nil && *cfg.Web.Enabled {
		lines = append(lines, fmt.Sprintf("web UI:   http://127.0.0.1:%d", cfg.Web.Port))
	}
	printBox(cmd, lines)
}

func printStartedBox(cmd *cobra.Command, pid int, logPath string, cfg *config.Config) {
	pidStr := fmt.Sprintf("PID: %d", pid)
	lines := []string{
		"daemon started in background",
		pidStr,
		"",
		"stop:     dreamer stop",
		"status:   dreamer status",
		"logs:     " + logPath,
	}
	if cfg.Web.Enabled != nil && *cfg.Web.Enabled {
		lines = append(lines, fmt.Sprintf("web UI:   http://127.0.0.1:%d", cfg.Web.Port))
	}
	printBox(cmd, lines)
}

// printBox renders a Unicode box around the given lines. Matches the style
// used by the web command's error output.
func printBox(cmd *cobra.Command, lines []string) {
	maxLen := 0
	for _, l := range lines {
		if len(l) > maxLen {
			maxLen = len(l)
		}
	}
	w := maxLen + 4 // padding inside the box

	cmd.Printf("╔%s╗\n", strings.Repeat("═", w))
	for _, l := range lines {
		cmd.Printf("║  %-*s  ║\n", maxLen, l)
	}
	cmd.Printf("╚%s╝\n", strings.Repeat("═", w))
}
