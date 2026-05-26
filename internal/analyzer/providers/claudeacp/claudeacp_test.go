package claudeacp

import (
	"testing"

	"dreamer/internal/analyzer"
)

func TestProviderRegistered(t *testing.T) {
	p, err := analyzer.NewProvider(analyzer.ProviderClaudeACP, analyzer.ProviderConfig{})
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}
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
	if p.ID() != ID {
		t.Fatalf("ID = %q, want %q", p.ID(), ID)
	}
}

func TestEnvPassthrough(t *testing.T) {
	p, err := analyzer.NewProvider(analyzer.ProviderClaudeACP, analyzer.ProviderConfig{
		Env: map[string]string{"ANTHROPIC_API_KEY": "test-key"},
	})
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}
	if p == nil {
		t.Fatal("expected non-nil provider")
	}
}

func TestDefaultCommand(t *testing.T) {
	p, err := analyzer.NewProvider(analyzer.ProviderClaudeACP, analyzer.ProviderConfig{})
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}
	if p == nil {
		t.Fatal("expected non-nil provider")
	}
}
