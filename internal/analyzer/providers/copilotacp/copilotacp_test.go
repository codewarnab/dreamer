package copilotacp

import (
	"testing"

	"dreamer/internal/analyzer"
	"dreamer/internal/analyzer/providers/acpcore"
)

func TestProviderRegistered(t *testing.T) {
	p, err := analyzer.NewProvider(analyzer.ProviderCopilotACP, analyzer.ProviderConfig{})
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}
	defer p.Close()
	if p.ID() != ID {
		t.Fatalf("ID = %q, want %q", p.ID(), ID)
	}
}

func TestCustomCommand(t *testing.T) {
	p, err := analyzer.NewProvider(analyzer.ProviderCopilotACP, analyzer.ProviderConfig{
		Command: []string{"/custom/copilot", "acp", "--verbose"},
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
	if len(cmd) != 3 || cmd[0] != "/custom/copilot" || cmd[1] != "acp" || cmd[2] != "--verbose" {
		t.Fatalf("Command = %v, want [/custom/copilot acp --verbose]", cmd)
	}
}

func TestEnvPassthrough(t *testing.T) {
	p, err := analyzer.NewProvider(analyzer.ProviderCopilotACP, analyzer.ProviderConfig{
		Env: map[string]string{"COPILOT_TOKEN": "test-token"},
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
	if env["COPILOT_TOKEN"] != "test-token" {
		t.Fatalf("COPILOT_TOKEN = %q, want %q", env["COPILOT_TOKEN"], "test-token")
	}
}
