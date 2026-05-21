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
