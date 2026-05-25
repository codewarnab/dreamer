package openclaudecli

import (
	"strings"
	"testing"
)

func TestDefaultCommandIncludesUnrestrictedFlags(t *testing.T) {
	p, err := New(Options{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer p.Close()

	cmd := p.(*provider).command
	got := strings.Join(cmd, " ")

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
	// --strict-mcp-config is a boolean flag; passing "{}" causes it to be
	// consumed as the positional prompt argument. Guard against regression.
	if strings.Contains(got, "--strict-mcp-config {}") {
		t.Errorf("default command must not pass a value to boolean flag --strict-mcp-config\ngot: %s", got)
	}
}

func TestCustomCommandOverridesDefaults(t *testing.T) {
	custom := []string{"my-openclaude", "--safe"}
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
