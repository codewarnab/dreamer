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
		"--ask-for-approval never",
	}
	for _, flag := range requiredFlags {
		if !strings.Contains(got, flag) {
			t.Errorf("default command missing %q\ngot: %s", flag, got)
		}
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
