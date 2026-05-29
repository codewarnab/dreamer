package cmd

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/spf13/cobra"
)

const (
	startupTaskName   = "Dreamer"
	systemdUnitName   = "dreamer.service"
	systemdDirName    = "systemd"
	systemdSubDirName = "user"
)

type commandRunner func(name string, args ...string) ([]byte, error)

var runStartupCommand commandRunner = runExternalCommand

func newStartupCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "startup",
		Short: "Manage OS startup registration for the daemon.",
		Long: "Manage an OS-level service or task that starts " +
			"the Dreamer daemon automatically. Uses Task Scheduler on Windows " +
			"and systemd user services on Linux.",
	}

	command.RunE = suggestSubcommandRunE()

	command.AddCommand(newStartupInstallCommand())
	command.AddCommand(newStartupUninstallCommand())
	command.AddCommand(newStartupStatusCommand())

	return command
}

func newStartupInstallCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "install",
		Short: "Start the daemon automatically at login/boot.",
		RunE: func(cmd *cobra.Command, args []string) error {
			resolvedConfigPath, err := resolveConfigPath(configPath)
			if err != nil {
				return err
			}

			executablePath, err := os.Executable()
			if err != nil {
				return fmt.Errorf("resolve dreamer executable path: %w", err)
			}

			if runtime.GOOS == "windows" {
				return installWindowsStartup(cmd, executablePath, resolvedConfigPath)
			}
			return installLinuxStartup(cmd, executablePath, resolvedConfigPath)
		},
	}

	return command
}

func newStartupUninstallCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "uninstall",
		Short: "Remove the OS startup registration.",
		RunE: func(cmd *cobra.Command, args []string) error {
			if runtime.GOOS == "windows" {
				return uninstallWindowsStartup(cmd)
			}
			return uninstallLinuxStartup(cmd)
		},
	}
}

func newStartupStatusCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show the startup registration status.",
		RunE: func(cmd *cobra.Command, args []string) error {
			if runtime.GOOS == "windows" {
				return statusWindowsStartup(cmd)
			}
			return statusLinuxStartup(cmd)
		},
	}
}

// --- Windows ---

func installWindowsStartup(cmd *cobra.Command, executablePath, configPath string) error {
	taskCommand := buildStartupTaskCommand(executablePath, configPath)
	// /RI is rejected by schtasks for ONLOGON triggers, so we only register the
	// logon trigger here. Crash recovery on Windows is the daemon's own concern.
	if output, err := runStartupCommand("schtasks.exe", "/Create", "/TN", startupTaskName, "/TR", taskCommand, "/SC", "ONLOGON", "/RL", "LIMITED", "/F"); err != nil {
		return fmt.Errorf("install startup task: %w%s", err, formatCommandOutput(output))
	}

	cmd.Printf("startup task installed: %s\n", startupTaskName)
	cmd.Printf("daemon command: %s\n", taskCommand)
	return nil
}

func uninstallWindowsStartup(cmd *cobra.Command) error {
	if output, err := runStartupCommand("schtasks.exe", "/Delete", "/TN", startupTaskName, "/F"); err != nil {
		return fmt.Errorf("uninstall startup task: %w%s", err, formatCommandOutput(output))
	}

	cmd.Printf("startup task uninstalled: %s\n", startupTaskName)
	return nil
}

func statusWindowsStartup(cmd *cobra.Command) error {
	output, err := runStartupCommand("schtasks.exe", "/Query", "/TN", startupTaskName, "/FO", "LIST", "/V")
	if err != nil {
		return fmt.Errorf("query startup task: %w%s", err, formatCommandOutput(output))
	}

	cmd.Print(string(output))
	return nil
}

func buildStartupTaskCommand(executablePath string, configPath string) string {
	parts := []string{
		quoteWindowsCommandArgument(executablePath),
		"daemon",
		"--config",
		quoteWindowsCommandArgument(configPath),
	}
	return strings.Join(parts, " ")
}

func quoteWindowsCommandArgument(value string) string {
	escaped := strings.ReplaceAll(value, `"`, `\"`)
	return `"` + escaped + `"`
}

// --- Linux ---

func installLinuxStartup(cmd *cobra.Command, executablePath, configPath string) error {
	unitContent := buildSystemdUnit(executablePath, configPath)

	configDir, err := os.UserConfigDir()
	if err != nil {
		return fmt.Errorf("resolve user config dir: %w", err)
	}
	unitDir := filepath.Join(configDir, systemdDirName, systemdSubDirName)
	if mkErr := os.MkdirAll(unitDir, 0o755); mkErr != nil {
		return fmt.Errorf("create systemd user dir %q: %w", unitDir, mkErr)
	}
	unitPath := filepath.Join(unitDir, systemdUnitName)
	if writeErr := os.WriteFile(unitPath, []byte(unitContent), 0o644); writeErr != nil {
		return fmt.Errorf("write systemd unit %q: %w", unitPath, writeErr)
	}

	if output, err := runStartupCommand("systemctl", "--user", "daemon-reload"); err != nil {
		return fmt.Errorf("systemctl daemon-reload: %w%s", err, formatCommandOutput(output))
	}
	// If enable fails the unit file is left on disk; `startup uninstall` cleans it up.
	if output, err := runStartupCommand("systemctl", "--user", "enable", "dreamer"); err != nil {
		return fmt.Errorf("systemctl enable: %w%s", err, formatCommandOutput(output))
	}

	cmd.Printf("systemd user service installed: %s\n", unitPath)
	cmd.Printf("start with: systemctl --user start dreamer\n")
	cmd.Printf("logs: journalctl --user -u dreamer\n")
	return nil
}

func uninstallLinuxStartup(cmd *cobra.Command) error {
	// Best-effort stop; ignore error if not running.
	_, _ = runStartupCommand("systemctl", "--user", "stop", "dreamer")

	if output, err := runStartupCommand("systemctl", "--user", "disable", "dreamer"); err != nil {
		return fmt.Errorf("systemctl disable: %w%s", err, formatCommandOutput(output))
	}

	configDir, err := os.UserConfigDir()
	if err != nil {
		return fmt.Errorf("resolve user config dir: %w", err)
	}
	unitPath := filepath.Join(configDir, systemdDirName, systemdSubDirName, systemdUnitName)
	if removeErr := os.Remove(unitPath); removeErr != nil && !os.IsNotExist(removeErr) {
		return fmt.Errorf("remove unit file %q: %w", unitPath, removeErr)
	}

	if output, err := runStartupCommand("systemctl", "--user", "daemon-reload"); err != nil {
		return fmt.Errorf("systemctl daemon-reload: %w%s", err, formatCommandOutput(output))
	}

	cmd.Printf("systemd user service uninstalled: dreamer\n")
	return nil
}

func statusLinuxStartup(cmd *cobra.Command) error {
	output, err := runStartupCommand("systemctl", "--user", "status", "dreamer")
	// systemctl status returns non-zero for inactive services; treat output as
	// informational regardless of exit code.
	if len(output) > 0 {
		cmd.Print(string(output))
	}
	if err != nil && len(output) == 0 {
		return fmt.Errorf("systemctl status: %w", err)
	}
	return nil
}

func buildSystemdUnit(executablePath, configPath string) string {
	// Each ExecStart argument is wrapped in systemd-style double quotes so the
	// unit keeps parsing correctly when the install path contains spaces.
	// StartLimit* caps the Restart=on-failure loop: at RestartSec=30 the
	// default 10s burst window never trips, so a permanently broken config
	// would otherwise relaunch the daemon every 30s indefinitely.
	return fmt.Sprintf(`[Unit]
Description=Dreamer daemon - periodic chat analysis
After=network.target
StartLimitIntervalSec=600
StartLimitBurst=5

[Service]
Type=simple
ExecStart=%s daemon --config %s
Restart=on-failure
RestartSec=30
StandardOutput=journal
StandardError=journal

[Install]
WantedBy=default.target
`, quoteSystemdArg(executablePath), quoteSystemdArg(configPath))
}

// quoteSystemdArg wraps value in systemd-style double quotes, escaping any
// backslashes or quotes so paths with spaces survive ExecStart parsing.
func quoteSystemdArg(value string) string {
	escaped := strings.ReplaceAll(value, `\`, `\\`)
	escaped = strings.ReplaceAll(escaped, `"`, `\"`)
	return `"` + escaped + `"`
}

// --- Shared ---

func runExternalCommand(name string, args ...string) ([]byte, error) {
	command := exec.Command(name, args...)
	var output bytes.Buffer
	command.Stdout = &output
	command.Stderr = &output
	err := command.Run()
	return output.Bytes(), err
}

func formatCommandOutput(output []byte) string {
	trimmedOutput := strings.TrimSpace(string(output))
	if trimmedOutput == "" {
		return ""
	}
	return ": " + trimmedOutput
}
