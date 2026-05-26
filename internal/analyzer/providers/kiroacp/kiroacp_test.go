package kiroacp

import (
	"testing"

	"dreamer/internal/analyzer"
	"dreamer/internal/analyzer/providers/acpcore"
)

func TestProviderRegistered(t *testing.T) {
	p, err := analyzer.NewProvider(analyzer.ProviderKiroACP, analyzer.ProviderConfig{})
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}
	defer p.Close()
	if p.ID() != ID {
		t.Fatalf("ID = %q, want %q", p.ID(), ID)
	}
}

func TestCustomCommand(t *testing.T) {
	p, err := analyzer.NewProvider(analyzer.ProviderKiroACP, analyzer.ProviderConfig{
		Command: []string{"/custom/kiro-cli", "acp", "--verbose"},
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
	if len(cmd) != 3 || cmd[0] != "/custom/kiro-cli" || cmd[1] != "acp" || cmd[2] != "--verbose" {
		t.Fatalf("Command = %v, want [/custom/kiro-cli acp --verbose]", cmd)
	}
}

func TestEnvPassthrough(t *testing.T) {
	p, err := analyzer.NewProvider(analyzer.ProviderKiroACP, analyzer.ProviderConfig{
		Env: map[string]string{"KIRO_API_KEY": "test-key"},
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
	if env["KIRO_API_KEY"] != "test-key" {
		t.Fatalf("KIRO_API_KEY = %q, want %q", env["KIRO_API_KEY"], "test-key")
	}
}
