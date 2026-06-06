package acpcore

import (
	"encoding/json"
	"testing"
)

// --- extractAvailableModelIDs ---

func TestExtractAvailableModelIDs_NilInput(t *testing.T) {
	if got := extractAvailableModelIDs(nil); got != nil {
		t.Fatalf("nil input: got %v, want nil", got)
	}
}

func TestExtractAvailableModelIDs_EmptyJSON(t *testing.T) {
	if got := extractAvailableModelIDs(json.RawMessage(`{}`)); got != nil {
		t.Fatalf("empty object: got %v, want nil", got)
	}
}

func TestExtractAvailableModelIDs_MalformedJSON(t *testing.T) {
	if got := extractAvailableModelIDs(json.RawMessage(`{not valid json`)); got != nil {
		t.Fatalf("malformed JSON: got %v, want nil", got)
	}
}

func TestExtractAvailableModelIDs_EmptyAvailableModels(t *testing.T) {
	raw := json.RawMessage(`{"models":{"availableModels":[]}}`)
	if got := extractAvailableModelIDs(raw); got != nil {
		t.Fatalf("empty availableModels: got %v, want nil", got)
	}
}

func TestExtractAvailableModelIDs_SingleModel(t *testing.T) {
	raw := json.RawMessage(`{"models":{"availableModels":[{"modelId":"claude-sonnet-4-5"}]}}`)
	got := extractAvailableModelIDs(raw)
	if len(got) != 1 || got[0] != "claude-sonnet-4-5" {
		t.Fatalf("single model: got %v, want [claude-sonnet-4-5]", got)
	}
}

func TestExtractAvailableModelIDs_MultipleModels(t *testing.T) {
	raw := json.RawMessage(`{"models":{"availableModels":[
		{"modelId":"claude-haiku-4-5"},
		{"modelId":"claude-sonnet-4-5"},
		{"modelId":"claude-opus-4-5"}
	]}}`)
	got := extractAvailableModelIDs(raw)
	if len(got) != 3 {
		t.Fatalf("three models: got %d entries, want 3: %v", len(got), got)
	}
	want := map[string]bool{"claude-haiku-4-5": true, "claude-sonnet-4-5": true, "claude-opus-4-5": true}
	for _, id := range got {
		if !want[id] {
			t.Fatalf("unexpected model id %q", id)
		}
	}
}

func TestExtractAvailableModelIDs_SkipsEmptyModelIDs(t *testing.T) {
	raw := json.RawMessage(`{"models":{"availableModels":[
		{"modelId":"valid-model"},
		{"modelId":""},
		{"modelId":"another-model"}
	]}}`)
	got := extractAvailableModelIDs(raw)
	if len(got) != 2 {
		t.Fatalf("empty modelId filtered: got %v, want 2 entries", got)
	}
	for _, id := range got {
		if id == "" {
			t.Fatalf("empty modelId leaked into result: %v", got)
		}
	}
}

func TestExtractAvailableModelIDs_NoModelsField(t *testing.T) {
	// session/new response that omits the models field entirely (e.g. codex-acp).
	raw := json.RawMessage(`{"sessionId":"abc123"}`)
	if got := extractAvailableModelIDs(raw); got != nil {
		t.Fatalf("missing models field: got %v, want nil", got)
	}
}
