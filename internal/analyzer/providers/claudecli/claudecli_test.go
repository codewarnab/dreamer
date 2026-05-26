package claudecli

import (
	"strings"
	"testing"
)

func TestReadStreamJSON_SchemaChange(t *testing.T) {
	input := strings.NewReader("{\"bad\"}\nnope\n")
	text, err := readStreamJSON(input)
	if err == nil {
		t.Fatal("expected error for all lines failing to parse")
	}
	if text != "" {
		t.Errorf("expected empty text, got %q", text)
	}
	if !strings.Contains(err.Error(), "all 2 output lines failed to parse") {
		t.Errorf("error missing line count: %v", err)
	}
	if !strings.Contains(err.Error(), "provider schema change?") {
		t.Errorf("error missing schema change hint: %v", err)
	}
}

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
