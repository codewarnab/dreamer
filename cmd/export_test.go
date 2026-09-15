package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExportCommand_ExportSuccess(t *testing.T) {
	home := t.TempDir()
	setTestHome(t, home)

	projectDir := filepath.Join(home, "myrepo")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatalf("mkdir projectDir: %v", err)
	}

	outputRoot := filepath.Join(home, "output")
	projOutputDir := filepath.Join(outputRoot, "myrepo")
	if err := os.MkdirAll(projOutputDir, 0o755); err != nil {
		t.Fatalf("mkdir projOutputDir: %v", err)
	}

	todosContent := "# Todos\n\n- [ ] Fix bug 123\n"
	if err := os.WriteFile(filepath.Join(projOutputDir, "todos.md"), []byte(todosContent), 0o644); err != nil {
		t.Fatalf("write todos.md: %v", err)
	}

	cfgPath := filepath.Join(home, "config.yaml")
	cfgContent := fmt.Sprintf(`default_provider: openclaude-cli
daemon:
  output_root: %q
projects:
  - name: myrepo
    path: %q
`, outputRoot, projectDir)
	if err := os.WriteFile(cfgPath, []byte(cfgContent), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	stdout, stderr, err := executeRootCommand("export", "--config", cfgPath, "--project", "myrepo")
	if err != nil {
		t.Fatalf("export failed: %v\nstderr: %s", err, stderr)
	}

	if !strings.Contains(stdout, "exported todos for project myrepo") {
		t.Fatalf("stdout %q does not contain expected message", stdout)
	}

	targetTodos := filepath.Join(projectDir, "todos.md")
	data, err := os.ReadFile(targetTodos)
	if err != nil {
		t.Fatalf("read target todos: %v", err)
	}
	if string(data) != todosContent {
		t.Fatalf("target todos content mismatch: got %q, want %q", string(data), todosContent)
	}
}

func TestExportCommand_ExportByPath(t *testing.T) {
	home := t.TempDir()
	setTestHome(t, home)

	projectDir := filepath.Join(home, "myrepo")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatalf("mkdir projectDir: %v", err)
	}

	outputRoot := filepath.Join(home, "output")
	projOutputDir := filepath.Join(outputRoot, "myrepo")
	if err := os.MkdirAll(projOutputDir, 0o755); err != nil {
		t.Fatalf("mkdir projOutputDir: %v", err)
	}

	todosContent := "# Path Todos\n"
	if err := os.WriteFile(filepath.Join(projOutputDir, "todos.md"), []byte(todosContent), 0o644); err != nil {
		t.Fatalf("write todos.md: %v", err)
	}

	cfgPath := filepath.Join(home, "config.yaml")
	cfgContent := fmt.Sprintf(`default_provider: openclaude-cli
daemon:
  output_root: %q
projects:
  - name: myrepo
    path: %q
`, outputRoot, projectDir)
	if err := os.WriteFile(cfgPath, []byte(cfgContent), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	stdout, stderr, err := executeRootCommand("export", "--config", cfgPath, "--path", projectDir)
	if err != nil {
		t.Fatalf("export by path failed: %v\nstderr: %s", err, stderr)
	}

	if !strings.Contains(stdout, "exported todos for project myrepo") {
		t.Fatalf("stdout %q does not contain expected message", stdout)
	}

	targetTodos := filepath.Join(projectDir, "todos.md")
	data, err := os.ReadFile(targetTodos)
	if err != nil {
		t.Fatalf("read target todos: %v", err)
	}
	if string(data) != todosContent {
		t.Fatalf("content mismatch: got %q, want %q", string(data), todosContent)
	}
}

func TestExportCommand_CustomDestination(t *testing.T) {
	home := t.TempDir()
	setTestHome(t, home)

	projectDir := filepath.Join(home, "myrepo")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatalf("mkdir projectDir: %v", err)
	}

	outputRoot := filepath.Join(home, "output")
	projOutputDir := filepath.Join(outputRoot, "myrepo")
	if err := os.MkdirAll(projOutputDir, 0o755); err != nil {
		t.Fatalf("mkdir projOutputDir: %v", err)
	}

	todosContent := "# Custom Destination Todos\n"
	if err := os.WriteFile(filepath.Join(projOutputDir, "todos.md"), []byte(todosContent), 0o644); err != nil {
		t.Fatalf("write todos.md: %v", err)
	}

	cfgPath := filepath.Join(home, "config.yaml")
	cfgContent := fmt.Sprintf(`default_provider: openclaude-cli
daemon:
  output_root: %q
projects:
  - name: myrepo
    path: %q
`, outputRoot, projectDir)
	if err := os.WriteFile(cfgPath, []byte(cfgContent), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	destPath := filepath.Join(home, "nested", "custom", "todos.md")
	stdout, stderr, err := executeRootCommand("export", "--config", cfgPath, "--project", "myrepo", "--output", destPath)
	if err != nil {
		t.Fatalf("export custom dest failed: %v\nstderr: %s", err, stderr)
	}

	if !strings.Contains(stdout, destPath) {
		t.Fatalf("stdout %q does not mention destination path %q", stdout, destPath)
	}

	data, err := os.ReadFile(destPath)
	if err != nil {
		t.Fatalf("read dest todos: %v", err)
	}
	if string(data) != todosContent {
		t.Fatalf("content mismatch: got %q, want %q", string(data), todosContent)
	}
}

func TestExportCommand_NoTodosFound(t *testing.T) {
	home := t.TempDir()
	setTestHome(t, home)

	projectDir := filepath.Join(home, "myrepo")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatalf("mkdir projectDir: %v", err)
	}

	outputRoot := filepath.Join(home, "output")
	cfgPath := filepath.Join(home, "config.yaml")
	cfgContent := fmt.Sprintf(`default_provider: openclaude-cli
daemon:
  output_root: %q
projects:
  - name: myrepo
    path: %q
`, outputRoot, projectDir)
	if err := os.WriteFile(cfgPath, []byte(cfgContent), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	_, _, err := executeRootCommand("export", "--config", cfgPath, "--project", "myrepo")
	if err == nil {
		t.Fatalf("expected error when todos.md missing")
	}
	if !strings.Contains(err.Error(), "no todos found for project") {
		t.Fatalf("error %q should mention no todos found", err)
	}
}

func TestExportCommand_ProjectNotFound(t *testing.T) {
	home := t.TempDir()
	setTestHome(t, home)

	outputRoot := filepath.Join(home, "output")
	cfgPath := filepath.Join(home, "config.yaml")
	cfgContent := fmt.Sprintf(`default_provider: openclaude-cli
daemon:
  output_root: %q
projects: []
`, outputRoot)
	if err := os.WriteFile(cfgPath, []byte(cfgContent), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	nonExistentDir := filepath.Join(home, "does-not-exist")
	_, _, err := executeRootCommand("export", "--config", cfgPath, "--path", nonExistentDir)
	if err == nil {
		t.Fatalf("expected error for non-existent path")
	}
}
