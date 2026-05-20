package codexcli

import (
	"strings"
	"testing"
)

func TestDefaultCommandIncludesSandboxFlags(t *testing.T) {
	p, err := New(Options{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer p.Close()

	cmd := p.(*provider).command
	got := strings.Join(cmd, " ")

	requiredFlags := []string{
		"--sandbox read-only",
	}
	for _, flag := range requiredFlags {
		if !strings.Contains(got, flag) {
			t.Errorf("default command missing %q\ngot: %s", flag, got)
		}
	}
	// --ask-for-approval is not a valid `codex exec` flag; it lives on the
	// interactive `codex` command. Guard against re-introducing it.
	if strings.Contains(got, "--ask-for-approval") {
		t.Errorf("default command must not include --ask-for-approval (rejected by codex exec)\ngot: %s", got)
	}
}

func TestCustomCommandOverridesDefaults(t *testing.T) {
	custom := []string{"my-codex", "exec"}
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
