package geminiacp

import (
	"testing"

	"dreamer/internal/analyzer"
	"dreamer/internal/analyzer/providers/acpcore"
)

func TestProviderRegistered(t *testing.T) {
	p, err := analyzer.NewProvider(analyzer.ProviderGeminiACP, analyzer.ProviderConfig{})
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}
	defer p.Close()
	if p.ID() != ID {
		t.Fatalf("ID = %q, want %q", p.ID(), ID)
	}
}

func TestCustomCommand(t *testing.T) {
	p, err := analyzer.NewProvider(analyzer.ProviderGeminiACP, analyzer.ProviderConfig{
		Command: []string{"/custom/gemini", "acp", "--verbose"},
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
	if len(cmd) != 3 || cmd[0] != "/custom/gemini" || cmd[1] != "acp" || cmd[2] != "--verbose" {
		t.Fatalf("Command = %v, want [/custom/gemini acp --verbose]", cmd)
	}
}

func TestEnvPassthrough(t *testing.T) {
	p, err := analyzer.NewProvider(analyzer.ProviderGeminiACP, analyzer.ProviderConfig{
		Env: map[string]string{"GEMINI_API_KEY": "test-key"},
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
	if env["GEMINI_API_KEY"] != "test-key" {
		t.Fatalf("GEMINI_API_KEY = %q, want %q", env["GEMINI_API_KEY"], "test-key")
	}
}

func TestDefaultCommand(t *testing.T) {
	p, err := analyzer.NewProvider(analyzer.ProviderGeminiACP, analyzer.ProviderConfig{})
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}
	defer p.Close()

	cmd, _, ok := acpcore.InspectProvider(p)
	if !ok {
		t.Fatal("expected acpcore-backed provider")
	}
	if len(cmd) == 0 {
		t.Fatal("expected non-empty default command")
	}
}
