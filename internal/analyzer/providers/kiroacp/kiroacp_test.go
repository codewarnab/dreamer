package kiroacp

import (
	"testing"

	"dreamer/internal/analyzer"
)

func TestProviderRegistered(t *testing.T) {
	p, err := analyzer.NewProvider(analyzer.ProviderKiroACP, analyzer.ProviderConfig{})
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}
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
	if p.ID() != ID {
		t.Fatalf("ID = %q, want %q", p.ID(), ID)
	}
}

func TestEnvPassthrough(t *testing.T) {
	p, err := analyzer.NewProvider(analyzer.ProviderKiroACP, analyzer.ProviderConfig{
		Env: map[string]string{"KIRO_API_KEY": "test-key"},
	})
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}
	if p == nil {
		t.Fatal("expected non-nil provider")
	}
}
