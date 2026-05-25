package claudecli

import (
	"context"
	"strings"
	"testing"

	"dreamer/internal/analyzer"
	"dreamer/internal/sandbox"
)

func TestDefaultCommandIncludesUnrestrictedFlags(t *testing.T) {
	p, err := New(Options{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer p.Close()

	cmd := p.(*provider).command
	got := strings.Join(cmd, " ")

	if !sandbox.Available() {
		if strings.Contains(got, "--dangerously-skip-permissions") {
			t.Errorf("default command must not include unrestricted flags without native sandbox\ngot: %s", got)
		}
		if !strings.Contains(got, "--permission-mode plan") {
			t.Errorf("default command missing policy fallback\ngot: %s", got)
		}
		return
	}

	requiredFlags := []string{
		"--dangerously-skip-permissions",
		"--bare",
	}
	for _, flag := range requiredFlags {
		if !strings.Contains(got, flag) {
			t.Errorf("default command missing %q\ngot: %s", flag, got)
		}
	}
	// Policy-only flags must NOT be present (sandbox replaces them).
	forbiddenFlags := []string{
		"--permission-mode",
		"--tools Read,Grep,Glob",
	}
	for _, flag := range forbiddenFlags {
		if strings.Contains(got, flag) {
			t.Errorf("default command must not include %q (sandbox replaces policy flags)\ngot: %s", flag, got)
		}
	}
	// --strict-mcp-config is a boolean flag on Claude Code; passing it with
	// "{}" causes the value to be consumed as the positional prompt argument.
	if strings.Contains(got, "--strict-mcp-config {}") {
		t.Errorf("default command must not pass a value to boolean flag --strict-mcp-config\ngot: %s", got)
	}
}

func TestCustomCommandOverridesDefaults(t *testing.T) {
	custom := []string{"my-claude", "--safe"}
	p, err := New(Options{Command: custom})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer p.Close()

	cmd := p.(*provider).command
	if len(cmd) != len(custom) {
		t.Fatalf("custom command not applied: got %v, want %v", cmd, custom)
	}
}

func TestSandboxOffDefaultCommandUsesPolicyFlags(t *testing.T) {
	p, err := New(Options{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer p.Close()

	sess, err := p.NewSession(context.Background(), analyzer.SessionConfig{
		WorkingDirectory: t.TempDir(),
		Sandbox:          "false",
	})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	got := strings.Join(sess.(*session).command, " ")
	if strings.Contains(got, "--dangerously-skip-permissions") {
		t.Fatalf("sandbox=false should use policy flags, got: %s", got)
	}
	if !strings.Contains(got, "--permission-mode plan") {
		t.Fatalf("sandbox=false command missing policy mode, got: %s", got)
	}
}

func TestWritableDirsHonorClaudeConfigDir(t *testing.T) {
	claudeConfigDir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", claudeConfigDir)

	p, err := New(Options{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer p.Close()

	sess, err := p.NewSession(context.Background(), analyzer.SessionConfig{
		WorkingDirectory: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	writable := sess.(*session).sandboxCfg.WritableDirs
	for _, dir := range writable {
		if dir == claudeConfigDir {
			return
		}
	}
	t.Fatalf("CLAUDE_CONFIG_DIR %q not found in writable dirs: %v", claudeConfigDir, writable)
}
