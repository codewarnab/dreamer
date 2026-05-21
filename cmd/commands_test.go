package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"dreamer/internal/analyzer"
	"dreamer/internal/errs"
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
