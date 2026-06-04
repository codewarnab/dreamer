package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRemoveCommand_RemovesProject(t *testing.T) {
	home := t.TempDir()
	setTestHome(t, home)
	cfgDir := filepath.Join(home, ".config", "dreamer")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	seed := strings.Replace(minimalSeedConfig, "projects: []",
		"projects:\n  - name: myrepo\n    path: /abs/myrepo\n    since: 7d\n", 1)
	cfgPath := filepath.Join(cfgDir, "config.yaml")
	if err := os.WriteFile(cfgPath, []byte(seed), 0o644); err != nil {
		t.Fatalf("seed config: %v", err)
	}
	stdout, stderr, err := executeRootCommand("remove", "myrepo")
	if err != nil {
		t.Fatalf("remove: %v\nstderr=%s", err, stderr)
	}
	if !strings.Contains(stdout, "removed project") || !strings.Contains(stdout, "myrepo") {
		t.Fatalf("stdout %q should mention removed project", stdout)
	}
	got, _ := os.ReadFile(cfgPath)
	s := string(got)
	if strings.Contains(s, "name: myrepo") {
		t.Fatalf("myrepo should be removed from config:\n%s", s)
	}
}

func TestRemoveCommand_ProjectNotFound(t *testing.T) {
	home := t.TempDir()
	setTestHome(t, home)
	cfgDir := filepath.Join(home, ".config", "dreamer")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	cfgPath := filepath.Join(cfgDir, "config.yaml")
	if err := os.WriteFile(cfgPath, []byte(minimalSeedConfig), 0o644); err != nil {
		t.Fatalf("seed config: %v", err)
	}
	_, _, err := executeRootCommand("remove", "nonexistent")
	if err == nil {
		t.Fatalf("expected error for missing project")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Fatalf("error %q should mention not found", err)
	}
}

func TestRemoveCommand_NoArgs(t *testing.T) {
	home := t.TempDir()
	setTestHome(t, home)
	cfgDir := filepath.Join(home, ".config", "dreamer")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	cfgPath := filepath.Join(cfgDir, "config.yaml")
	if err := os.WriteFile(cfgPath, []byte(minimalSeedConfig), 0o644); err != nil {
		t.Fatalf("seed config: %v", err)
	}
	_, _, err := executeRootCommand("remove")
	if err == nil {
		t.Fatalf("expected error for no args")
	}
}

func TestRemoveCommand_ConfigMissing(t *testing.T) {
	home := t.TempDir()
	setTestHome(t, home)
	_, stderr, err := executeRootCommand("remove", "myrepo")
	if err == nil {
		t.Fatalf("expected error when config.yaml missing")
	}
	// Styled output goes to stderr; err.Error() carries the plain sentinel.
	if !strings.Contains(stderr, "setup") && !strings.Contains(err.Error(), "setup") {
		t.Fatalf("error %q / stderr %q should suggest running setup", err, stderr)
	}
}
