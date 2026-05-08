package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRootCommandRegistersExpectedSubcommands(t *testing.T) {
	command := newRootCommand()

	expected := map[string]bool{
		"analyze":  false,
		"daemon":   false,
		"config":   false,
		"ls-chats": false,
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

func TestConfigInitCreatesDefaultConfigFile(t *testing.T) {
	homeDir := t.TempDir()
	setTestHome(t, homeDir)

	stdout, stderr, err := executeRootCommand("config", "init")
	if err != nil {
		t.Fatalf("execute config init: %v\nstderr=%s", err, stderr)
	}

	configPath := filepath.Join(homeDir, ".dreamer", defaultConfigFileName)
	data, readErr := os.ReadFile(configPath)
	if readErr != nil {
		t.Fatalf("read generated config: %v", readErr)
	}

	content := string(data)
	if !strings.Contains(content, "projects: []") {
		t.Fatalf("generated config missing projects section: %s", content)
	}
	if !strings.Contains(stdout, configPath) {
		t.Fatalf("stdout %q does not include config path %q", stdout, configPath)
	}
}

func TestConfigInitRejectsExistingFileWithoutForce(t *testing.T) {
	homeDir := t.TempDir()
	setTestHome(t, homeDir)

	if _, _, err := executeRootCommand("config", "init"); err != nil {
		t.Fatalf("first config init failed: %v", err)
	}

	_, _, err := executeRootCommand("config", "init")
	if err == nil {
		t.Fatalf("second config init expected error")
	}
	if !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("error = %q, want existing file error", err)
	}
}

func TestConfigInitForceOverwritesExistingFile(t *testing.T) {
	homeDir := t.TempDir()
	setTestHome(t, homeDir)

	if _, _, err := executeRootCommand("config", "init"); err != nil {
		t.Fatalf("first config init failed: %v", err)
	}

	configPath := filepath.Join(homeDir, ".dreamer", defaultConfigFileName)
	if err := os.WriteFile(configPath, []byte("projects:\n  - name: stale"), 0o644); err != nil {
		t.Fatalf("overwrite config fixture: %v", err)
	}

	if _, stderr, err := executeRootCommand("config", "init", "--force"); err != nil {
		t.Fatalf("forced config init failed: %v\nstderr=%s", err, stderr)
	}

	content, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read overwritten config: %v", err)
	}
	if !strings.Contains(string(content), "projects: []") {
		t.Fatalf("forced config init did not restore default template: %s", string(content))
	}
}

func TestAnalyzeRequiresProjectFlag(t *testing.T) {
	_, _, err := executeRootCommand("analyze")
	if err == nil {
		t.Fatalf("analyze expected required project flag error")
	}
	if !strings.Contains(err.Error(), "required flag(s) \"project\" not set") {
		t.Fatalf("error = %q, want required project flag error", err)
	}
}

func TestAnalyzeReturnsAnalyzerClientStartupError(t *testing.T) {
	homeDir := t.TempDir()
	setTestHome(t, homeDir)
	t.Setenv("APPDATA", t.TempDir())
	t.Setenv("COPILOT_CLI_PATH", filepath.Join(t.TempDir(), "missing-copilot-cli"))

	projectDir := t.TempDir()
	outputRoot := t.TempDir()
	configPath := filepath.Join(t.TempDir(), "config.json")
	writeJSONConfig(t, configPath, map[string]any{
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

	chatPath := filepath.Join(homeDir, ".copilot", "session-state", "workspace", "chat.jsonl")
	if err := os.MkdirAll(filepath.Dir(chatPath), 0o755); err != nil {
		t.Fatalf("MkdirAll chat path: %v", err)
	}
	if err := os.WriteFile(chatPath, []byte(`{"role":"user","content":"hello world"}`), 0o644); err != nil {
		t.Fatalf("WriteFile chat path: %v", err)
	}

	_, stderr, err := executeRootCommand("analyze", "--config", configPath, "--project", "example")
	if err == nil {
		t.Fatalf("analyze expected analyzer client startup error")
	}
	if !strings.Contains(err.Error(), "start analyzer client") {
		t.Fatalf("error = %q, want analyzer startup message; stderr=%s", err, stderr)
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
}
