package geminicli

import (
	"context"
	"strings"
	"testing"

	"dreamer/internal/analyzer"
	"dreamer/internal/sandbox"
)

func TestDefaultCommandIncludesYoloFlag(t *testing.T) {
	p, err := New(Options{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer p.Close()

	cmd := p.(*provider).command
	got := strings.Join(cmd, " ")

	if !sandbox.Available() {
		if strings.Contains(got, "--yolo") {
			t.Errorf("default command must not include --yolo without native sandbox\ngot: %s", got)
		}
		if !strings.Contains(got, "--approval-mode=plan") {
			t.Errorf("default command missing approval-mode fallback\ngot: %s", got)
		}
		return
	}

	if !strings.Contains(got, "--yolo") {
		t.Errorf("default command missing --yolo\ngot: %s", got)
	}
	// --approval-mode=plan must NOT be present (sandbox replaces it).
	if strings.Contains(got, "--approval-mode") {
		t.Errorf("default command must not include --approval-mode (sandbox replaces policy flags)\ngot: %s", got)
	}
}

func TestCustomCommandOverridesDefaults(t *testing.T) {
	custom := []string{"my-gemini", "-p"}
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
	if strings.Contains(got, "--yolo") {
		t.Fatalf("sandbox=false should not use --yolo, got: %s", got)
	}
	if !strings.Contains(got, "--approval-mode=plan") {
		t.Fatalf("sandbox=false command missing approval-mode fallback, got: %s", got)
	}
}

func TestWritableDirsHonorGeminiHome(t *testing.T) {
	geminiHomeDir := t.TempDir()
	t.Setenv("GEMINI_HOME", geminiHomeDir)

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
		if dir == geminiHomeDir {
			return
		}
	}
	t.Fatalf("GEMINI_HOME %q not found in writable dirs: %v", geminiHomeDir, writable)
}
