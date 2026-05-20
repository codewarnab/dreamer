package cmd

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

func TestBuildStartupTaskCommandQuotesExecutableAndConfig(t *testing.T) {
	command := buildStartupTaskCommand(
		`C:\Program Files\Dreamer\dreamer.exe`,
		`C:\Users\User\.dreamer\config.yaml`,
	)

	expected := `"C:\Program Files\Dreamer\dreamer.exe" daemon --config "C:\Users\User\.dreamer\config.yaml"`
	if command != expected {
		t.Fatalf("command = %q, want %q", command, expected)
	}
}

func TestStartupInstallCreatesLogonTask(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("startup task installation is Windows-specific")
	}

	homeDir := t.TempDir()
	setTestHome(t, homeDir)

	var commandName string
	var commandArgs []string
	withStartupCommandRunner(t, func(name string, args ...string) ([]byte, error) {
		commandName = name
		commandArgs = slices.Clone(args)
		return []byte("SUCCESS"), nil
	})

	stdout, stderr, err := executeRootCommand("startup", "install")
	if err != nil {
		t.Fatalf("startup install returned error: %v\nstderr=%s", err, stderr)
	}

	if commandName != "schtasks.exe" {
		t.Fatalf("command name = %q, want schtasks.exe", commandName)
	}
	assertContainsArgument(t, commandArgs, "/Create")
	assertContainsArgument(t, commandArgs, "/SC")
	assertContainsArgument(t, commandArgs, "ONLOGON")
	assertNotContainsArgument(t, commandArgs, "/RI")
	assertNotContainsArgument(t, commandArgs, "/DU")
	assertContainsArgument(t, commandArgs, "/TN")
	assertContainsArgument(t, commandArgs, startupTaskName)
	assertContainsArgument(t, commandArgs, filepath.Join(homeDir, ".config", "dreamer", defaultConfigFileName))
	if !strings.Contains(stdout, "startup task installed") {
		t.Fatalf("stdout missing install confirmation: %s", stdout)
	}
}

func TestStartupStatusReturnsSchedulerError(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("startup task status is Windows-specific")
	}

	withStartupCommandRunner(t, func(name string, args ...string) ([]byte, error) {
		return []byte("ERROR: task not found"), errors.New("exit status 1")
	})

	_, _, err := executeRootCommand("startup", "status")
	if err == nil {
		t.Fatalf("startup status expected error")
	}
	if !strings.Contains(err.Error(), "task not found") {
		t.Fatalf("error = %q, want scheduler output", err)
	}
}

func TestBuildSystemdUnitContainsExpectedFields(t *testing.T) {
	unit := buildSystemdUnit("/usr/bin/dreamer", "/home/user/.config/dreamer/config.yaml")

	assertContainsString(t, unit, `ExecStart="/usr/bin/dreamer" daemon --config "/home/user/.config/dreamer/config.yaml"`)
	assertContainsString(t, unit, "Restart=on-failure")
	assertContainsString(t, unit, "RestartSec=30")
	assertContainsString(t, unit, "StartLimitIntervalSec=600")
	assertContainsString(t, unit, "StartLimitBurst=5")
	assertContainsString(t, unit, "Type=simple")
	assertContainsString(t, unit, "After=network.target")
	assertContainsString(t, unit, "WantedBy=default.target")
	assertContainsString(t, unit, "StandardOutput=journal")
}

// TestBuildSystemdUnitQuotesPathsWithSpaces guards the ExecStart quoting that
// keeps the unit valid when the install paths happen to contain spaces.
func TestBuildSystemdUnitQuotesPathsWithSpaces(t *testing.T) {
	unit := buildSystemdUnit("/opt/My Apps/dreamer", "/home/jane doe/.config/dreamer/config.yaml")
	assertContainsString(t, unit, `ExecStart="/opt/My Apps/dreamer" daemon --config "/home/jane doe/.config/dreamer/config.yaml"`)
}

func TestStartupInstallCreatesSystemdUnit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("systemd startup is Linux-specific")
	}

	configDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configDir)

	var commands [][]string
	withStartupCommandRunner(t, func(name string, args ...string) ([]byte, error) {
		commands = append(commands, append([]string{name}, args...))
		return []byte("SUCCESS"), nil
	})

	stdout, stderr, err := executeRootCommand("startup", "install")
	if err != nil {
		t.Fatalf("startup install returned error: %v\nstderr=%s", err, stderr)
	}

	// Verify unit file was written.
	unitPath := filepath.Join(configDir, "systemd", "user", "dreamer.service")
	data, readErr := os.ReadFile(unitPath)
	if readErr != nil {
		t.Fatalf("read unit file: %v", readErr)
	}
	content := string(data)
	assertContainsString(t, content, "[Unit]")
	assertContainsString(t, content, "[Service]")
	assertContainsString(t, content, "[Install]")
	assertContainsString(t, content, "daemon --config")

	// Verify systemctl commands were called.
	if len(commands) < 2 {
		t.Fatalf("expected at least 2 commands, got %d", len(commands))
	}
	foundDaemonReload := false
	foundEnable := false
	for _, cmd := range commands {
		if len(cmd) >= 3 && cmd[0] == "systemctl" && cmd[1] == "--user" && cmd[2] == "daemon-reload" {
			foundDaemonReload = true
		}
		if len(cmd) >= 4 && cmd[0] == "systemctl" && cmd[1] == "--user" && cmd[2] == "enable" && cmd[3] == "dreamer" {
			foundEnable = true
		}
	}
	if !foundDaemonReload {
		t.Fatalf("expected systemctl --user daemon-reload")
	}
	if !foundEnable {
		t.Fatalf("expected systemctl --user enable dreamer")
	}

	if !strings.Contains(stdout, "systemd user service installed") {
		t.Fatalf("stdout missing install confirmation: %s", stdout)
	}
}

func TestStartupUninstallRemovesSystemdUnit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("systemd startup is Linux-specific")
	}

	configDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configDir)

	// Pre-create the unit file so uninstall can remove it.
	unitDir := filepath.Join(configDir, "systemd", "user")
	if err := os.MkdirAll(unitDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	unitPath := filepath.Join(unitDir, "dreamer.service")
	if err := os.WriteFile(unitPath, []byte("[Unit]\n"), 0o644); err != nil {
		t.Fatalf("write unit: %v", err)
	}

	var commands [][]string
	withStartupCommandRunner(t, func(name string, args ...string) ([]byte, error) {
		commands = append(commands, append([]string{name}, args...))
		return []byte("SUCCESS"), nil
	})

	stdout, stderr, err := executeRootCommand("startup", "uninstall")
	if err != nil {
		t.Fatalf("startup uninstall returned error: %v\nstderr=%s", err, stderr)
	}

	// Verify unit file was removed.
	if _, statErr := os.Stat(unitPath); !os.IsNotExist(statErr) {
		t.Fatalf("unit file still exists: %s", unitPath)
	}

	// Verify systemctl commands were called.
	foundDisable := false
	for _, cmd := range commands {
		if len(cmd) >= 4 && cmd[0] == "systemctl" && cmd[1] == "--user" && cmd[2] == "disable" && cmd[3] == "dreamer" {
			foundDisable = true
		}
	}
	if !foundDisable {
		t.Fatalf("expected systemctl --user disable dreamer")
	}

	if !strings.Contains(stdout, "systemd user service uninstalled") {
		t.Fatalf("stdout missing uninstall confirmation: %s", stdout)
	}
}

func withStartupCommandRunner(t *testing.T, runner commandRunner) {
	t.Helper()

	previousRunner := runStartupCommand
	runStartupCommand = runner
	t.Cleanup(func() {
		runStartupCommand = previousRunner
	})
}

func assertContainsArgument(t *testing.T, args []string, expected string) {
	t.Helper()

	for _, arg := range args {
		if arg == expected || strings.Contains(arg, expected) {
			return
		}
	}
	t.Fatalf("args %q do not contain %q", args, expected)
}

func assertContainsString(t *testing.T, content, expected string) {
	t.Helper()
	if !strings.Contains(content, expected) {
		t.Fatalf("content missing %q\ncontent:\n%s", expected, content)
	}
}

func assertNotContainsArgument(t *testing.T, args []string, banned string) {
	t.Helper()
	for _, arg := range args {
		if arg == banned {
			t.Fatalf("args %q unexpectedly contain %q", args, banned)
		}
	}
}
