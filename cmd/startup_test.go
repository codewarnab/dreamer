package cmd

import (
	"errors"
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
	assertContainsArgument(t, commandArgs, "/TN")
	assertContainsArgument(t, commandArgs, startupTaskName)
	assertContainsArgument(t, commandArgs, filepath.Join(homeDir, ".dreamer", defaultConfigFileName))
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
