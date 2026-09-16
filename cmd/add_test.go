package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dreamer/internal/config"
)

const minimalSeedConfig = `# dreamer global config.

default_provider: openclaude-cli

# Projects iterated by the daemon.
projects: []

daemon:
  frequency_seconds: 3600

logging:
  level: info
`

func TestAppendProjectToYAML_ReplacesEmptyList(t *testing.T) {
	// Test that config.AppendProjectToYAML (now centralized in internal/config) correctly parses the seed config
	// and inserts the project sequence when the projects list starts out as empty.
	out, err := config.AppendProjectToYAML([]byte(minimalSeedConfig), "alpha", "/abs/alpha", "24h")
	if err != nil {
		t.Fatalf("append: %v", err)
	}
	s := string(out)
	if !strings.Contains(s, "name: alpha") || !strings.Contains(s, "path: /abs/alpha") || !strings.Contains(s, "since: 24h") {
		t.Fatalf("rendered yaml missing fields:\n%s", s)
	}
	// Comments must be preserved.
	if !strings.Contains(s, "# dreamer global config.") {
		t.Fatalf("top comment lost:\n%s", s)
	}
	if !strings.Contains(s, "# Projects iterated by the daemon.") {
		t.Fatalf("projects section comment lost:\n%s", s)
	}
}

func TestAppendProjectToYAML_AppendsToExistingList(t *testing.T) {
	seed := strings.Replace(minimalSeedConfig, "projects: []", "projects:\n  - name: first\n    path: /abs/first\n    since: 7d\n", 1)
	out, err := config.AppendProjectToYAML([]byte(seed), "second", "/abs/second", "30d")
	if err != nil {
		t.Fatalf("append: %v", err)
	}
	s := string(out)
	if !strings.Contains(s, "first") || !strings.Contains(s, "second") {
		t.Fatalf("both projects should be present:\n%s", s)
	}
	if !strings.Contains(s, "since: 7d") || !strings.Contains(s, "since: 30d") {
		t.Fatalf("since fields lost:\n%s", s)
	}
}

func TestAppendProjectToYAML_RejectsDuplicateName(t *testing.T) {
	seed := strings.Replace(minimalSeedConfig, "projects: []", "projects:\n  - name: alpha\n    path: /abs/alpha\n    since: 24h\n", 1)
	_, err := config.AppendProjectToYAML([]byte(seed), "alpha", "/abs/different", "7d")
	if err == nil {
		t.Fatalf("expected duplicate-name error")
	}
	if !strings.Contains(err.Error(), "alpha") {
		t.Fatalf("error %q should mention 'alpha'", err)
	}
}

func TestAppendProjectToYAML_RejectsDuplicatePath(t *testing.T) {
	seed := strings.Replace(minimalSeedConfig, "projects: []", "projects:\n  - name: alpha\n    path: /abs/x\n    since: 24h\n", 1)
	_, err := config.AppendProjectToYAML([]byte(seed), "beta", "/abs/x", "7d")
	if err == nil {
		t.Fatalf("expected duplicate-path error")
	}
}

func TestResolveAddPath_DefaultsToCWD(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	got, err := resolveAddPath(".")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	// On macOS/Linux the cwd may resolve through /private; just ensure absolute + dir.
	if !filepath.IsAbs(got) {
		t.Fatalf("not absolute: %q", got)
	}
	info, _ := os.Stat(got)
	if info == nil || !info.IsDir() {
		t.Fatalf("not a dir: %q", got)
	}
}

func TestResolveAddPath_RejectsNonexistent(t *testing.T) {
	_, err := resolveAddPath("/this/path/should/not/exist/__nope__")
	if err == nil {
		t.Fatalf("expected error for nonexistent path")
	}
}

func TestAddCommand_FailsWhenConfigMissing(t *testing.T) {
	home := t.TempDir()
	setTestHome(t, home)
	dir := t.TempDir()
	_, stderr, err := executeRootCommand("add", dir)
	if err == nil {
		t.Fatalf("expected error when config.yaml missing")
	}
	// Styled output goes to stderr; err.Error() carries the plain sentinel.
	if !strings.Contains(stderr, "setup") && !strings.Contains(err.Error(), "setup") {
		t.Fatalf("error %q / stderr %q should suggest running setup", err, stderr)
	}
}

func TestAppendProjectToYAML_RejectsDuplicatePath_TildeExpanded(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home) // Windows
	absDir := filepath.Join(home, "myproj")
	if err := os.MkdirAll(absDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Seed config stores the path as ~/myproj.
	seed := strings.Replace(minimalSeedConfig, "projects: []",
		"projects:\n  - name: alpha\n    path: ~/myproj\n    since: 24h\n", 1)
	// Adding with the resolved absolute path should detect the duplicate.
	_, err := config.AppendProjectToYAML([]byte(seed), "beta", absDir, "7d")
	if err == nil {
		t.Fatalf("expected duplicate-path error for tilde-expanded path")
	}
}

func TestAddCommand_AppendsProjectToExistingConfig(t *testing.T) {
	home := t.TempDir()
	setTestHome(t, home)
	cfgPath, err := config.GlobalConfigPath()
	if err != nil {
		t.Fatalf("GlobalConfigPath: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(cfgPath, []byte(minimalSeedConfig), 0o644); err != nil {
		t.Fatalf("seed config: %v", err)
	}
	projectDir := t.TempDir()
	stdout, stderr, err := executeRootCommand("add", projectDir, "--name", "myrepo", "--since", "7d")
	if err != nil {
		t.Fatalf("add: %v\nstderr=%s", err, stderr)
	}
	if !strings.Contains(stdout, "myrepo") {
		t.Fatalf("stdout %q should mention added project", stdout)
	}
	got, _ := os.ReadFile(cfgPath)
	s := string(got)
	if !strings.Contains(s, "name: myrepo") || !strings.Contains(s, "since: 7d") {
		t.Fatalf("config not updated:\n%s", s)
	}
	if !strings.Contains(s, "# Projects iterated by the daemon.") {
		t.Fatalf("comments lost:\n%s", s)
	}
}
