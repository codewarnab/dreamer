package claudeacp

import (
	"testing"

	"dreamer/internal/analyzer"
	"dreamer/internal/analyzer/providers/acpcore"
)

func TestProviderRegistered(t *testing.T) {
	p, err := analyzer.NewProvider(analyzer.ProviderClaudeACP, analyzer.ProviderConfig{})
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}
	defer p.Close()
	if p.ID() != ID {
		t.Fatalf("ID = %q, want %q", p.ID(), ID)
	}
}

func TestCustomCommand(t *testing.T) {
	p, err := analyzer.NewProvider(analyzer.ProviderClaudeACP, analyzer.ProviderConfig{
		Command: []string{"/custom/claude-acp", "--verbose"},
	})
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}
	defer p.Close()

	// Verify the custom command actually reached the underlying acpcore provider.
	cmd, _, ok := acpcore.InspectProvider(p)
	if !ok {
		t.Fatal("expected acpcore-backed provider")
	}
	if len(cmd) != 2 || cmd[0] != "/custom/claude-acp" || cmd[1] != "--verbose" {
		t.Fatalf("Command = %v, want [/custom/claude-acp --verbose]", cmd)
	}
}

func TestEnvPassthrough(t *testing.T) {
	p, err := analyzer.NewProvider(analyzer.ProviderClaudeACP, analyzer.ProviderConfig{
		Env: map[string]string{"ANTHROPIC_API_KEY": "test-key"},
	})
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}
	defer p.Close()

	// Verify the env vars reached the underlying acpcore provider.
	_, env, ok := acpcore.InspectProvider(p)
	if !ok {
		t.Fatal("expected acpcore-backed provider")
	}
	if env["ANTHROPIC_API_KEY"] != "test-key" {
		t.Fatalf("ANTHROPIC_API_KEY = %q, want %q", env["ANTHROPIC_API_KEY"], "test-key")
	}
}

func TestDefaultCommand(t *testing.T) {
	p, err := analyzer.NewProvider(analyzer.ProviderClaudeACP, analyzer.ProviderConfig{})
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}
	defer p.Close()

	// Default command should include "npx".
	cmd, _, ok := acpcore.InspectProvider(p)
	if !ok {
		t.Fatal("expected acpcore-backed provider")
	}
	if len(cmd) == 0 {
		t.Fatal("expected non-empty default command")
	}
}
