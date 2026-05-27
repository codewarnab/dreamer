package analyzer_test

import (
	"testing"

	"dreamer/internal/analyzer"

	_ "dreamer/internal/analyzer/providers/claudeacp"
	_ "dreamer/internal/analyzer/providers/claudecli"
	_ "dreamer/internal/analyzer/providers/codebuffsdk"
	_ "dreamer/internal/analyzer/providers/codexacp"
	_ "dreamer/internal/analyzer/providers/codexcli"
	_ "dreamer/internal/analyzer/providers/copilotacp"
	_ "dreamer/internal/analyzer/providers/copilotsdk"
	_ "dreamer/internal/analyzer/providers/geminiacp"
	_ "dreamer/internal/analyzer/providers/geminicli"
	_ "dreamer/internal/analyzer/providers/kiroacp"
	_ "dreamer/internal/analyzer/providers/openclaudecli"
	_ "dreamer/internal/analyzer/providers/opencodeacp"
	_ "dreamer/internal/analyzer/providers/opencodehttp"
)

func TestEveryRegisteredProviderHasMeta(t *testing.T) {
	metaByID := map[analyzer.ProviderID]bool{}
	for _, m := range analyzer.RegisteredProviderMeta() {
		metaByID[m.ID] = true
	}
	for _, id := range analyzer.RegisteredProviders() {
		if !metaByID[id] {
			t.Errorf("provider %q registered without ProviderMeta (display name / order)", id)
		}
	}
}

func TestProviderMetaOrdersUnique(t *testing.T) {
	seen := map[int]analyzer.ProviderID{}
	for _, m := range analyzer.RegisteredProviderMeta() {
		if prev, ok := seen[m.Order]; ok {
			t.Errorf("Order %d duplicated between %q and %q (sort would be non-deterministic)", m.Order, prev, m.ID)
		}
		seen[m.Order] = m.ID
	}
}

func TestProviderMetaDisplayNameNonEmpty(t *testing.T) {
	for _, m := range analyzer.RegisteredProviderMeta() {
		if m.DisplayName == "" {
			t.Errorf("provider %q has empty DisplayName", m.ID)
		}
	}
}

func TestCLIBackgroundSafeProviders(t *testing.T) {
	cliProviders := []analyzer.ProviderID{
		analyzer.ProviderOpenClaudeCLI,
		analyzer.ProviderClaudeCLI,
		analyzer.ProviderCodexCLI,
		analyzer.ProviderGeminiCLI,
	}
	for _, id := range cliProviders {
		caps := analyzer.LookupProviderCapabilities(id)
		if !caps.BackgroundSafe {
			t.Errorf("CLI provider %q: BackgroundSafe = false, want true", id)
		}
		if !caps.RequiresNetwork {
			t.Errorf("CLI provider %q: RequiresNetwork = false, want true", id)
		}
		if !caps.SupportsBackgroundWrites {
			t.Errorf("CLI provider %q: SupportsBackgroundWrites = false, want true", id)
		}
		if !caps.NeedsNativeSandbox {
			t.Errorf("CLI provider %q: NeedsNativeSandbox = false, want true", id)
		}
	}
}

func TestACPAndSDKProvidersNotBackgroundSafe(t *testing.T) {
	nonCLIProviders := []analyzer.ProviderID{
		analyzer.ProviderCopilotSDK,
		analyzer.ProviderCopilotACP,
		analyzer.ProviderClaudeACP,
		analyzer.ProviderGeminiACP,
		analyzer.ProviderKiroACP,
		analyzer.ProviderCodexACP,
		analyzer.ProviderOpenCodeACP,
		analyzer.ProviderOpenCodeServer,
		analyzer.ProviderCodebuffSDK,
	}
	for _, id := range nonCLIProviders {
		caps := analyzer.LookupProviderCapabilities(id)
		if caps.BackgroundSafe {
			t.Errorf("non-CLI provider %q: BackgroundSafe = true, want false", id)
		}
		if !caps.RequiresNetwork {
			t.Errorf("provider %q: RequiresNetwork = false, want true", id)
		}
	}
}

func TestLookupProviderCapabilities_Unknown(t *testing.T) {
	caps := analyzer.LookupProviderCapabilities("nonexistent-provider")
	if caps.BackgroundSafe {
		t.Error("unknown provider should have zero-value capabilities (BackgroundSafe=false)")
	}
}
