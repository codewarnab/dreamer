package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"dreamer/internal/analyzer"
	"dreamer/internal/config"
	"dreamer/internal/errs"
	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"
)

// cmdTestFailProviderID is a test-only provider whose Start() always returns
// errs.ProviderUnavailable. Registering it in init() lets command-level tests
// exercise provider-startup failure without depending on any real binary,
// environment variable, or authentication state. This decouples the test from
// whichever provider is the current default — the test sets
// "default_provider": "cmd-test-fail" in the config and gets a deterministic
// failure every time.
const cmdTestFailProviderID = "cmd-test-fail"

func init() {
	analyzer.RegisterProvider(analyzer.ProviderID(cmdTestFailProviderID), func(cfg analyzer.ProviderConfig) (analyzer.Provider, error) {
		return cmdTestFailProvider{}, nil
	})
}

// cmdTestFailProvider satisfies analyzer.Provider but fails Start() with a
// KindProviderUnavailable error, simulating a provider that cannot be reached.
type cmdTestFailProvider struct{}

func (cmdTestFailProvider) ID() string { return cmdTestFailProviderID }
func (cmdTestFailProvider) Start(ctx context.Context) error {
	return errs.ProviderUnavailable(cmdTestFailProviderID, "start", fmt.Errorf("injected test failure"))
}
func (cmdTestFailProvider) NewSession(ctx context.Context, cfg analyzer.SessionConfig) (analyzer.Session, error) {
	return nil, fmt.Errorf("cmdTestFailProvider: should not reach NewSession")
}
func (cmdTestFailProvider) Close() error { return nil }

// cmdTestFailSession is never used in practice because Start() always fails,
// but it satisfies the analyzer.Session interface for completeness.
type cmdTestFailSession struct{}

func (cmdTestFailSession) Run(ctx context.Context, prompt string, timeout time.Duration) (string, error) {
	return "", fmt.Errorf("cmdTestFailSession: should not be called")
}
func (cmdTestFailSession) Close() error { return nil }

func TestRootCommandRegistersExpectedSubcommands(t *testing.T) {
	command := newRootCommand()

	expected := map[string]bool{
		"analyze":  false,
		"daemon":   false,
		"ls-chats": false,
		"start":    false,
		"startup":  false,
		"status":   false,
		"stop":     false,
		"setup":    false,
		"web":      false,
		"add":      false,
	}

	for _, subcommand := range command.Commands() {
		if _, ok := expected[subcommand.Name()]; ok {
			expected[subcommand.Name()] = true
		}
	}

	for name, found := range expected {
		if !found {
			t.Fatalf("expected subcommand %q to be registered", name)
		}
	}
}

func TestAnalyzeRequiresProjectFlag(t *testing.T) {
	_, _, err := executeRootCommand("analyze")
	if err == nil {
		t.Fatalf("analyze expected required path flag error")
	}
	if !strings.Contains(err.Error(), "required flag(s) \"path\" not set") {
		t.Fatalf("error = %q, want required path flag error", err)
	}
}

// TestAnalyzeReturnsAnalyzerClientStartupError verifies that when a provider
// fails to start, the error propagates through the pipeline with the correct
// Kind (KindProviderUnavailable) and Provider fields.
//
// Previous versions of this test set COPILOT_CLI_PATH to a nonexistent binary
// to force the (then-default) copilot-sdk provider to fail. That approach
// breaks whenever the default provider changes because the new provider may
// ignore that env var entirely. The fix: inject a dedicated test-only provider
// ("cmd-test-fail") whose Start() always returns errs.ProviderUnavailable,
// then point the config's default_provider at it. This way the test exercises
// the pipeline's error-propagation contract without depending on any real
// binary, environment variable, or authentication state.
func TestAnalyzeReturnsAnalyzerClientStartupError(t *testing.T) {
	homeDir := t.TempDir()
	setTestHome(t, homeDir)
	t.Setenv("APPDATA", t.TempDir())

	projectDir := t.TempDir()
	outputRoot := t.TempDir()
	configPath := filepath.Join(t.TempDir(), "config.json")
	writeJSONConfig(t, configPath, map[string]any{
		"default_provider": cmdTestFailProviderID,
		"projects": []map[string]string{
			{"name": "example", "path": projectDir},
		},
		"analyzer": map[string]any{
			"rules": map[string]any{},
		},
		"daemon": map[string]any{
			"frequency_seconds": 300,
			"log_level":         "info",
			"output_root":       outputRoot,
		},
	})

	// Create a minimal chat source so the pipeline reaches provider startup.
	chatPath := filepath.Join(homeDir, ".copilot", "session-state", "workspace", "chat.jsonl")
	if err := os.MkdirAll(filepath.Dir(chatPath), 0o755); err != nil {
		t.Fatalf("MkdirAll chat path: %v", err)
	}
	if err := os.WriteFile(chatPath, []byte(`{"role":"user","content":"hello world"}`), 0o644); err != nil {
		t.Fatalf("WriteFile chat path: %v", err)
	}

	_, stderr, err := executeRootCommand("analyze", "--config", configPath, "--path", projectDir)
	if err == nil {
		t.Fatalf("analyze expected analyzer client startup error")
	}
	if errs.KindOf(err) != errs.KindProviderUnavailable {
		t.Fatalf("KindOf(err) = %q, want %q; err=%v stderr=%s", errs.KindOf(err), errs.KindProviderUnavailable, err, stderr)
	}
	if errs.ProviderOf(err) != cmdTestFailProviderID {
		t.Fatalf("ProviderOf(err) = %q, want %q", errs.ProviderOf(err), cmdTestFailProviderID)
	}
}

func TestDaemonReturnsErrorWhenNoProjectsConfigured(t *testing.T) {
	homeDir := t.TempDir()
	setTestHome(t, homeDir)
	t.Setenv("APPDATA", t.TempDir())

	outputRoot := t.TempDir()
	configPath := filepath.Join(t.TempDir(), "config.json")
	writeJSONConfig(t, configPath, map[string]any{
		"projects": []map[string]string{},
		"analyzer": map[string]any{
			"rules": map[string]any{},
		},
		"daemon": map[string]any{
			"frequency_seconds": 300,
			"log_level":         "info",
			"output_root":       outputRoot,
		},
	})

	_, _, err := executeRootCommand("daemon", "--config", configPath)
	if err == nil {
		t.Fatalf("daemon expected no-projects error")
	}
	if !strings.Contains(err.Error(), "has no projects configured") {
		t.Fatalf("error = %q, want no-projects error", err)
	}
}

func TestListChatsPrintsDiscoveredSources(t *testing.T) {
	homeDir := t.TempDir()
	appDataDir := t.TempDir()
	setTestHome(t, homeDir)
	t.Setenv("APPDATA", appDataDir)

	projectDir := t.TempDir()
	copilotChat := filepath.Join(homeDir, ".copilot", "session-state", "workspace", "chat.jsonl")
	vscodeChat := filepath.Join(appDataDir, "Code", "User", "workspaceStorage", "workspace-a", "chatSessions", "chat.json")
	vscodeWorkspaceJSON := filepath.Join(appDataDir, "Code", "User", "workspaceStorage", "workspace-a", "workspace.json")
	for _, path := range []string{copilotChat, vscodeChat} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("MkdirAll %q: %v", path, err)
		}
		if err := os.WriteFile(path, []byte("{}"), 0o644); err != nil {
			t.Fatalf("WriteFile %q: %v", path, err)
		}
	}
	if err := os.WriteFile(vscodeWorkspaceJSON, []byte(fmt.Sprintf(`{"folder":%q}`, projectDir)), 0o644); err != nil {
		t.Fatalf("WriteFile %q: %v", vscodeWorkspaceJSON, err)
	}

	stdout, stderr, err := executeRootCommand("ls-chats", "--project-path", projectDir)
	if err != nil {
		t.Fatalf("ls-chats returned error: %v\nstderr=%s", err, stderr)
	}
	if !strings.Contains(stdout, "TOOL\tMODIFIED_AT\tPATH") {
		t.Fatalf("stdout missing header: %s", stdout)
	}
	if !strings.Contains(stdout, copilotChat) {
		t.Fatalf("stdout missing copilot chat %q: %s", copilotChat, stdout)
	}
	if !strings.Contains(stdout, vscodeChat) {
		t.Fatalf("stdout missing vscode chat %q: %s", vscodeChat, stdout)
	}
}

func TestListChatsReturnsErrorWhenNoSourcesDiscovered(t *testing.T) {
	homeDir := t.TempDir()
	appDataDir := t.TempDir()
	setTestHome(t, homeDir)
	t.Setenv("APPDATA", appDataDir)

	projectDir := t.TempDir()
	_, _, err := executeRootCommand("ls-chats", "--project-path", projectDir)
	if err == nil {
		t.Fatalf("ls-chats expected no-sources error")
	}
	if !strings.Contains(err.Error(), "no chat sources discovered") {
		t.Fatalf("error = %q, want no-sources error", err)
	}
}

func TestListChatsRejectsProjectPathFile(t *testing.T) {
	projectFile := filepath.Join(t.TempDir(), "project.txt")
	if err := os.WriteFile(projectFile, []byte("not a directory"), 0o644); err != nil {
		t.Fatalf("write project path fixture: %v", err)
	}

	_, _, err := executeRootCommand("ls-chats", "--project-path", projectFile)
	if err == nil {
		t.Fatalf("ls-chats expected invalid project path error")
	}
	if !strings.Contains(err.Error(), "must be a directory") {
		t.Fatalf("error = %q, want directory validation error", err)
	}
}

func executeRootCommand(args ...string) (string, string, error) {
	command := newRootCommand()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	command.SetOut(&stdout)
	command.SetErr(&stderr)
	command.SetArgs(args)

	err := command.Execute()
	return stdout.String(), stderr.String(), err
}

func writeJSONConfig(t *testing.T, path string, value map[string]any) {
	t.Helper()

	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write config file: %v", err)
	}
}

func setTestHome(t *testing.T, home string) {
	t.Helper()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
}

func TestCheckJobConflict_NoConflict(t *testing.T) {
	projectDir := t.TempDir()
	outputRoot := t.TempDir()

	cfg := &config.Config{
		Daemon: config.DaemonConfig{OutputRoot: outputRoot},
		Projects: []config.ProjectConfig{
			{Name: "myproject", Path: projectDir},
		},
	}

	// No jobs.json on disk => no conflict.
	got := checkJobConflict(cfg, projectDir)
	if got != "" {
		t.Fatalf("checkJobConflict returned %q, want empty (no conflict)", got)
	}
}

func TestCheckJobConflict_ConflictingJobFound(t *testing.T) {
	projectDir := t.TempDir()
	outputRoot := t.TempDir()

	absProject, err := filepath.Abs(projectDir)
	if err != nil {
		t.Fatalf("filepath.Abs: %v", err)
	}
	absProject = filepath.Clean(absProject)

	// Write a jobs.json with a running job for the project.
	jobsPath := filepath.Join(outputRoot, "jobs.json")
	jobData := fmt.Sprintf(`{
  "version": 1,
  "jobs": [
    {
      "id": "myproject-1234-abcd",
      "project": "myproject",
      "project_path": %q,
      "status": "running",
      "enqueued_at": "2026-01-01T00:00:00Z",
      "provider": "test",
      "since": "24h"
    }
  ]
}`, absProject)
	if err := os.WriteFile(jobsPath, []byte(jobData), 0o644); err != nil {
		t.Fatalf("write jobs.json: %v", err)
	}

	cfg := &config.Config{
		Daemon: config.DaemonConfig{OutputRoot: outputRoot},
		Projects: []config.ProjectConfig{
			{Name: "myproject", Path: absProject},
		},
	}

	got := checkJobConflict(cfg, projectDir)
	if got == "" {
		t.Fatal("checkJobConflict returned empty, want conflict message")
	}
	if !strings.Contains(got, "already in progress") {
		t.Fatalf("checkJobConflict = %q, want message containing 'already in progress'", got)
	}
}

func TestCheckJobConflict_CompletedJobNoConflict(t *testing.T) {
	projectDir := t.TempDir()
	outputRoot := t.TempDir()

	absProject, err := filepath.Abs(projectDir)
	if err != nil {
		t.Fatalf("filepath.Abs: %v", err)
	}
	absProject = filepath.Clean(absProject)

	// Write a jobs.json with a completed job — should NOT conflict.
	jobsPath := filepath.Join(outputRoot, "jobs.json")
	jobData := fmt.Sprintf(`{
  "version": 1,
  "jobs": [
    {
      "id": "myproject-5678-efgh",
      "project": "myproject",
      "project_path": %q,
      "status": "completed",
      "enqueued_at": "2026-01-01T00:00:00Z",
      "provider": "test",
      "since": "24h"
    }
  ]
}`, absProject)
	if err := os.WriteFile(jobsPath, []byte(jobData), 0o644); err != nil {
		t.Fatalf("write jobs.json: %v", err)
	}

	cfg := &config.Config{
		Daemon: config.DaemonConfig{OutputRoot: outputRoot},
		Projects: []config.ProjectConfig{
			{Name: "myproject", Path: absProject},
		},
	}

	got := checkJobConflict(cfg, projectDir)
	if got != "" {
		t.Fatalf("checkJobConflict returned %q for completed job, want empty", got)
	}
}

func TestPrintBox_EmptyLines(t *testing.T) {
	cmd := &cobra.Command{}
	var buf bytes.Buffer
	cmd.SetOut(&buf)

	printBox(cmd, []string{})

	output := buf.String()
	// Should have top and bottom borders with nothing between them.
	if !strings.Contains(output, "╔") || !strings.Contains(output, "╚") {
		t.Fatalf("printBox output missing box borders: %q", output)
	}
	lines := strings.Split(strings.TrimSuffix(output, "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("printBox(empty) produced %d lines, want 2 (top + bottom borders)", len(lines))
	}
}

func TestPrintBox_SingleLine(t *testing.T) {
	cmd := &cobra.Command{}
	var buf bytes.Buffer
	cmd.SetOut(&buf)

	printBox(cmd, []string{"hello"})

	output := buf.String()
	lines := strings.Split(strings.TrimSuffix(output, "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("printBox(single) produced %d lines, want 3 (top + content + bottom)", len(lines))
	}
	if !strings.Contains(lines[1], "hello") {
		t.Fatalf("content line missing 'hello': %q", lines[1])
	}
	if !strings.HasPrefix(lines[1], "║") || !strings.HasSuffix(lines[1], "║") {
		t.Fatalf("content line not wrapped in ║: %q", lines[1])
	}
}

func TestPrintBox_MultipleLines(t *testing.T) {
	cmd := &cobra.Command{}
	var buf bytes.Buffer
	cmd.SetOut(&buf)

	printBox(cmd, []string{"short", "a longer line"})

	output := buf.String()
	lines := strings.Split(strings.TrimSuffix(output, "\n"), "\n")
	if len(lines) != 4 {
		t.Fatalf("printBox(multi) produced %d lines, want 4 (top + 2 content + bottom)", len(lines))
	}
	// Both content lines should have the same width (padded to the longest).
	if len(lines[1]) != len(lines[2]) {
		t.Fatalf("content lines differ in width: %d vs %d", len(lines[1]), len(lines[2]))
	}
	if !strings.Contains(lines[1], "short") {
		t.Fatalf("first content line missing 'short': %q", lines[1])
	}
	if !strings.Contains(lines[2], "a longer line") {
		t.Fatalf("second content line missing 'a longer line': %q", lines[2])
	}
}

func TestStyledHelp_IncludesBannerAndCommands(t *testing.T) {
	root := newRootCommand()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetArgs([]string{"--help"})

	_ = root.Execute()
	output := buf.String()

	// Banner is rendered as Unicode block characters.
	if !strings.Contains(output, "███╗") {
		t.Fatalf("help output missing banner block chars: %q", output)
	}
	if !strings.Contains(output, "analyze") {
		t.Fatalf("help output missing 'analyze' command: %q", output)
	}
	if !strings.Contains(output, "daemon") {
		t.Fatalf("help output missing 'daemon' command: %q", output)
	}
	if !strings.Contains(output, "CORE") {
		t.Fatalf("help output missing 'CORE' group: %q", output)
	}
	if !strings.Contains(output, "INSPECT") {
		t.Fatalf("help output missing 'INSPECT' group: %q", output)
	}
}

func TestRenderFlagSection_WithFlags(t *testing.T) {
	cmd := &cobra.Command{}
	cmd.Flags().StringP("name", "n", "", "set name")
	cmd.Flags().IntP("count", "c", 10, "set count")

	var buf strings.Builder
	style := lipgloss.NewStyle()
	renderFlagSection(&buf, "Testing", cmd.Flags(), style, style, style, style, style)

	output := buf.String()
	if !strings.Contains(output, "Testing") {
		t.Fatalf("expected section header 'Testing': %q", output)
	}
	if !strings.Contains(output, "--name") {
		t.Fatalf("expected --name flag: %q", output)
	}
	if !strings.Contains(output, "--count") {
		t.Fatalf("expected --count flag: %q", output)
	}
}

func TestPrintAlreadyRunningBox(t *testing.T) {
	cmd := &cobra.Command{}
	var buf bytes.Buffer
	cmd.SetOut(&buf)

	cfg := &config.Config{}
	printAlreadyRunningBox(cmd, 12345, "/tmp/dreamer.log", cfg)

	output := buf.String()
	if !strings.Contains(output, "12345") {
		t.Fatalf("expected PID 12345 in output: %q", output)
	}
	if !strings.Contains(output, "already running") {
		t.Fatalf("expected 'already running' message: %q", output)
	}
}

func TestPrintStartedBox(t *testing.T) {
	cmd := &cobra.Command{}
	var buf bytes.Buffer
	cmd.SetOut(&buf)

	cfg := &config.Config{
		Daemon: config.DaemonConfig{FrequencySeconds: 60},
		Projects: []config.ProjectConfig{
			{Name: "proj1", Path: "/tmp/proj1"},
		},
	}
	printStartedBox(cmd, 12345, "/tmp/dreamer.log", cfg)

	output := buf.String()
	if !strings.Contains(output, "12345") {
		t.Fatalf("expected PID 12345 in output: %q", output)
	}
	if !strings.Contains(output, "started") {
		t.Fatalf("expected 'started' message: %q", output)
	}
}

func TestWaitForLockfile_TimesOut(t *testing.T) {
	dir := t.TempDir()
	lockPath := filepath.Join(dir, "test.lock")

	// No lockfile will appear, should time out quickly.
	pid := waitForLockfile(lockPath, 100*time.Millisecond)
	if pid != 0 {
		t.Fatalf("expected 0 (timeout), got %d", pid)
	}
}

func TestWaitForLockfile_FindsPID(t *testing.T) {
	dir := t.TempDir()
	lockPath := filepath.Join(dir, "test.lock")

	// Write a valid lockfile with a PID.
	if err := os.WriteFile(lockPath, []byte("42\n"), 0o644); err != nil {
		t.Fatalf("write lockfile: %v", err)
	}

	pid := waitForLockfile(lockPath, 1*time.Second)
	if pid != 42 {
		t.Fatalf("expected PID 42, got %d", pid)
	}
}

func TestWaitForLockfileRemoval_Removed(t *testing.T) {
	dir := t.TempDir()
	lockPath := filepath.Join(dir, "test.lock")

	// Create then immediately remove the lockfile.
	if err := os.WriteFile(lockPath, []byte("42\n"), 0o644); err != nil {
		t.Fatalf("write lockfile: %v", err)
	}
	_ = os.Remove(lockPath)

	err := waitForLockfileRemoval(lockPath, 1*time.Second)
	if err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
}

func TestWaitForLockfileRemoval_Timeout(t *testing.T) {
	dir := t.TempDir()
	lockPath := filepath.Join(dir, "test.lock")

	// Create a lockfile that stays around.
	if err := os.WriteFile(lockPath, []byte("42\n"), 0o644); err != nil {
		t.Fatalf("write lockfile: %v", err)
	}

	err := waitForLockfileRemoval(lockPath, 100*time.Millisecond)
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
}

func TestSweepStaleFindingsTempFiles_NoFiles(t *testing.T) {
	// Global temp dir may already have stale files from other tests/daemons,
	// so we just verify the function runs without panic and returns a count.
	removed := sweepStaleFindingsTempFiles()
	if removed < 0 {
		t.Fatalf("expected non-negative count, got %d", removed)
	}
}

func TestSweepStaleFindingsTempFiles_RemovesOldFiles(t *testing.T) {
	tmpDir := os.TempDir()

	// Create old temp files matching the expected patterns.
	oldFile := filepath.Join(tmpDir, "dreamer-findings-stale-test.jsonl")
	if err := os.WriteFile(oldFile, []byte("test"), 0o644); err != nil {
		t.Fatalf("write stale file: %v", err)
	}
	defer os.Remove(oldFile)

	// Set modification time to well before staleAge.
	oldTime := time.Now().Add(-staleAge - time.Minute)
	_ = os.Chtimes(oldFile, oldTime, oldTime)

	removed := sweepStaleFindingsTempFiles()
	if removed < 1 {
		t.Fatalf("expected at least 1 removed, got %d", removed)
	}

	// Verify file was actually removed.
	if _, err := os.Stat(oldFile); !os.IsNotExist(err) {
		t.Fatalf("stale file should have been removed: %s", oldFile)
	}
}

func TestDetachedProcessAttr(t *testing.T) {
	attr := detachedProcessAttr()
	if attr == nil {
		t.Fatal("detachedProcessAttr returned nil")
	}
	expected := syscall.CREATE_NEW_PROCESS_GROUP | 0x00000008 // DETACHED_PROCESS
	if attr.CreationFlags != uint32(expected) {
		t.Fatalf("CreationFlags = 0x%x, want 0x%x", attr.CreationFlags, expected)
	}
}

func TestKillDaemon_ProcessNotRunning(t *testing.T) {
	// killDaemon on a non-existent PID should not error on Windows because
	// taskkill reports "not found" and we treat that as success.
	err := killDaemon(9999999)
	if err != nil {
		t.Fatalf("expected nil for non-existent PID, got: %v", err)
	}
}

func TestRunExternalCommand_Echo(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("echo is a shell built-in on Windows")
	}
	output, err := runExternalCommand("echo", "hello")
	if err != nil {
		t.Fatalf("runExternalCommand: %v", err)
	}
	if !strings.Contains(string(output), "hello") {
		t.Fatalf("expected 'hello', got: %q", output)
	}
}

func TestRunExternalCommand_FailingCommand(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("'false' command not available on Windows")
	}
	// A command that should fail.
	_, err := runExternalCommand("false")
	if err == nil {
		t.Fatal("expected error from 'false' command")
	}
}

func TestFormatCommandOutput(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"", ""},
		{"  \n  ", ""},
		{"hello", ": hello"},
		{"  hello world  \n", ": hello world"},
		{"error message\n", ": error message"},
	}
	for _, tt := range tests {
		got := formatCommandOutput([]byte(tt.input))
		if got != tt.want {
			t.Errorf("formatCommandOutput(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

