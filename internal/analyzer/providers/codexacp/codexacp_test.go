package codexacp

import (
	"testing"

	"dreamer/internal/analyzer"
	"dreamer/internal/analyzer/providers/acpcore"
)

func TestProviderRegistered(t *testing.T) {
	p, err := analyzer.NewProvider(analyzer.ProviderCodexACP, analyzer.ProviderConfig{})
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}
	defer p.Close()
	if p.ID() != ID {
		t.Fatalf("ID = %q, want %q", p.ID(), ID)
	}
}

func TestCustomCommand(t *testing.T) {
	p, err := analyzer.NewProvider(analyzer.ProviderCodexACP, analyzer.ProviderConfig{
		Command: []string{"/custom/codex-acp", "--verbose"},
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
	if len(cmd) != 2 || cmd[0] != "/custom/codex-acp" || cmd[1] != "--verbose" {
		t.Fatalf("Command = %v, want [/custom/codex-acp --verbose]", cmd)
	}
}

func TestEnvPassthrough(t *testing.T) {
	p, err := analyzer.NewProvider(analyzer.ProviderCodexACP, analyzer.ProviderConfig{
		Env: map[string]string{"OPENAI_API_KEY": "test-key"},
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
	if env["OPENAI_API_KEY"] != "test-key" {
		t.Fatalf("OPENAI_API_KEY = %q, want %q", env["OPENAI_API_KEY"], "test-key")
	}
}
