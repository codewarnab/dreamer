package backgroundjobs

import (
	"testing"

	"dreamer/internal/analyzer"
)

func TestProviderMetaByID_Existing(t *testing.T) {
	all := analyzer.RegisteredProviderMeta()
	if len(all) == 0 {
		t.Skip("no registered providers")
	}
	target := string(all[0].ID)
	meta := ProviderMetaByID(target)
	if meta == nil {
		t.Fatalf("expected non-nil for %s", target)
	}
	if meta.ID != target {
		t.Errorf("ID = %q, want %q", meta.ID, target)
	}
	if meta.DisplayName == "" {
		t.Error("DisplayName should not be empty")
	}
}

func TestProviderMetaByID_Unknown(t *testing.T) {
	meta := ProviderMetaByID("nonexistent-provider-xyz")
	if meta != nil {
		t.Errorf("expected nil for unknown provider, got %+v", meta)
	}
}

func TestProviderMetaByID_MapsFields(t *testing.T) {
	for _, m := range analyzer.RegisteredProviderMeta() {
		pm := ProviderMetaByID(string(m.ID))
		if pm == nil {
			continue
		}
		if pm.RequiresNetwork != m.Capabilities.RequiresNetwork {
			t.Errorf("ProviderMetaByID(%s).RequiresNetwork = %v, want %v",
				m.ID, pm.RequiresNetwork, m.Capabilities.RequiresNetwork)
		}
		if pm.BackgroundSafe != m.Capabilities.BackgroundSafe {
			t.Errorf("ProviderMetaByID(%s).BackgroundSafe = %v, want %v",
				m.ID, pm.BackgroundSafe, m.Capabilities.BackgroundSafe)
		}
	}
}
