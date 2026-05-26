package copilotacp

import (
	"testing"

	"dreamer/internal/analyzer"
)

func TestProviderRegistered(t *testing.T) {
	p, err := analyzer.NewProvider(analyzer.ProviderCopilotACP, analyzer.ProviderConfig{})
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}
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
	if p.ID() != ID {
		t.Fatalf("ID = %q, want %q", p.ID(), ID)
	}
}

func TestEnvPassthrough(t *testing.T) {
	p, err := analyzer.NewProvider(analyzer.ProviderCopilotACP, analyzer.ProviderConfig{
		Env: map[string]string{"COPILOT_TOKEN": "test-token"},
	})
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}
	if p == nil {
		t.Fatal("expected non-nil provider")
	}
}
