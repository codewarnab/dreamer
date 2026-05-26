package cmd

import (
	"strings"
	"testing"

	"dreamer/internal/analyzer"
)

func TestRenderCommentedConfig_ListsAllRegisteredProviders(t *testing.T) {
	out, err := renderCommentedConfig(setupAnswers{})
	if err != nil {
		t.Fatalf("renderCommentedConfig: %v", err)
	}
	rendered := string(out)
	for _, m := range analyzer.RegisteredProviderMeta() {
		if !strings.Contains(rendered, string(m.ID)) {
			t.Errorf("rendered config template missing provider id %q", m.ID)
		}
	}
}

func TestRenderCommentedConfig_WithProviderOverride(t *testing.T) {
	out, err := renderCommentedConfig(setupAnswers{
		provider: "openai-codex",
	})
	if err != nil {
		t.Fatalf("renderCommentedConfig: %v", err)
	}
	rendered := string(out)
	if !strings.Contains(rendered, "openai-codex") {
		t.Error("rendered config missing selected provider")
	}
}

func TestRenderCommentedConfig_Empty(t *testing.T) {
	out, err := renderCommentedConfig(setupAnswers{})
	if err != nil {
		t.Fatalf("renderCommentedConfig empty: %v", err)
	}
	if len(out) == 0 {
		t.Fatal("expected non-empty config template")
	}
}
