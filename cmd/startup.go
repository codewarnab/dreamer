package cmd

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"

	"github.com/spf13/cobra"
)

const startupTaskName = "Dreamer"

type commandRunner func(name string, args ...string) ([]byte, error)

var runStartupCommand commandRunner = runExternalCommand

func newStartupCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "startup",
		Short: "Manage Windows startup registration for the daemon.",
		Long: "Manage a per-user Windows Task Scheduler task that starts " +
			"the Dreamer daemon when the current user logs in.",
	}

	command.AddCommand(newStartupInstallCommand())
	command.AddCommand(newStartupUninstallCommand())
	command.AddCommand(newStartupStatusCommand())

	return command
}

func newStartupInstallCommand() *cobra.Command {
	var configPath string

	command := &cobra.Command{
		Use:   "install",
		Short: "Start the daemon automatically at Windows logon.",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := ensureWindowsStartupSupport(); err != nil {
				return err
			}

			resolvedConfigPath, err := resolveConfigPath(configPath)
			if err != nil {
				return err
			}

			executablePath, err := os.Executable()
			if err != nil {
				return fmt.Errorf("resolve dreamer executable path: %w", err)
			}

			taskCommand := buildStartupTaskCommand(executablePath, resolvedConfigPath)
			if output, err := runStartupCommand("schtasks.exe", "/Create", "/TN", startupTaskName, "/TR", taskCommand, "/SC", "ONLOGON", "/RL", "LIMITED", "/F"); err != nil {
				return fmt.Errorf("install startup task: %w%s", err, formatCommandOutput(output))
			}

			cmd.Printf("startup task installed: %s\n", startupTaskName)
			cmd.Printf("daemon command: %s\n", taskCommand)
			return nil
		},
	}

	command.Flags().StringVar(&configPath, "config", "", "Path to config file (default: ~/.dreamer/config.yaml)")
	return command
}

func newStartupUninstallCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "uninstall",
		Short: "Remove the Windows startup task.",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := ensureWindowsStartupSupport(); err != nil {
				return err
			}

			if output, err := runStartupCommand("schtasks.exe", "/Delete", "/TN", startupTaskName, "/F"); err != nil {
				return fmt.Errorf("uninstall startup task: %w%s", err, formatCommandOutput(output))
			}

			cmd.Printf("startup task uninstalled: %s\n", startupTaskName)
			return nil
		},
	}
}

func newStartupStatusCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show the Windows startup task status.",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := ensureWindowsStartupSupport(); err != nil {
				return err
			}

			output, err := runStartupCommand("schtasks.exe", "/Query", "/TN", startupTaskName, "/FO", "LIST", "/V")
			if err != nil {
				return fmt.Errorf("query startup task: %w%s", err, formatCommandOutput(output))
			}

			cmd.Print(string(output))
			return nil
		},
	}
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

func ensureWindowsStartupSupport() error {
	if runtime.GOOS != "windows" {
		return fmt.Errorf("startup task management is only supported on Windows")
	}
	return nil
}

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
