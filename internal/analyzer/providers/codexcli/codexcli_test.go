package codexcli

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

func TestDefaultCommandIncludesYoloFlag(t *testing.T) {
	p, err := New(Options{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer p.Close()

	cmd := p.(*provider).command
	got := strings.Join(cmd, " ")

	if !strings.Contains(got, "--yolo") {
		t.Errorf("default command missing --yolo\ngot: %s", got)
	}
	// --sandbox read-only must NOT be present (sandbox replaces it).
	if strings.Contains(got, "--sandbox") {
		t.Errorf("default command must not include --sandbox (OS sandbox replaces it)\ngot: %s", got)
	}
	// --ask-for-approval is not a valid `codex exec` flag.
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
