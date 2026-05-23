package analyzer

import (
	"reflect"
	"strings"
	"testing"
)

// TestApplyDefaultsCopiesAllFields is a compile-time sync guard.
// If you add a field to PromptDefaults and forget to update applyDefaults,
// this test will fail, not the runtime code.
func TestApplyDefaultsCopiesAllFields(t *testing.T) {
	d := PromptDefaults{
		Phase1Preamble:              "p1p",
		Phase1ResponseSchema:        "p1s",
		Phase2Preamble:              "p2p",
		Phase2ResponseSchema:        "p2s",
		ToolUseInstructions:         "tui",
		Phase2RecordingInstructions: "p2ri",
	}
	empty := RulePack{Category: RuleCategoryTest}
	pack := empty
	applyDefaults(&pack, d)

	defaultsVal := reflect.ValueOf(d)
	packVal := reflect.ValueOf(pack)

	for i := 0; i < defaultsVal.NumField(); i++ {
		fieldName := defaultsVal.Type().Field(i).Name
		df := defaultsVal.Field(i).String()
		pf := packVal.FieldByName(fieldName).String()
		if pf != df {
			t.Errorf("field %s: defaults=%q, pack=%q — applyDefaults did not copy it", fieldName, df, pf)
		}
	}

	// Also verify per-category YAML values are not clobbered by defaults.
	withValue := RulePack{
		Category:         RuleCategoryTest,
		Phase1Preamble:   "custom p1p",
		ToolUseInstructions: "custom tui",
	}
	applyDefaults(&withValue, d)
	if withValue.Phase1Preamble != "custom p1p" {
		t.Errorf("applyDefaults overwrote non-empty Phase1Preamble: got %q", withValue.Phase1Preamble)
	}
	if withValue.ToolUseInstructions != "custom tui" {
		t.Errorf("applyDefaults overwrote non-empty ToolUseInstructions: got %q", withValue.ToolUseInstructions)
	}
}

// TestLoadDefaultsYAML verifies the embedded defaults.yaml parses cleanly
// and all expected fields are populated.
func TestLoadDefaultsYAML(t *testing.T) {
	d, err := loadDefaultsYAML()
	if err != nil {
		t.Fatalf("loadDefaultsYAML: %v", err)
	}
	if d.Phase1Preamble == "" {
		t.Error("Phase1Preamble is empty")
	}
	if d.Phase1ResponseSchema == "" {
		t.Error("Phase1ResponseSchema is empty")
	}
	if d.Phase2Preamble == "" {
		t.Error("Phase2Preamble is empty")
	}
	if d.Phase2ResponseSchema == "" {
		t.Error("Phase2ResponseSchema is empty")
	}
	if d.ToolUseInstructions == "" {
		t.Error("ToolUseInstructions is empty")
	}
	// Phase2RecordingInstructions is intentionally optional.
}

// TestLoadDefaultRulePacksAppliesDefaults verifies that defaults.yaml values
// are propagated to every pack.
func TestLoadDefaultRulePacksAppliesDefaults(t *testing.T) {
	packs, err := LoadDefaultRulePacks()
	if err != nil {
		t.Fatalf("LoadDefaultRulePacks: %v", err)
	}
	if len(packs) == 0 {
		t.Fatal("no packs loaded")
	}
	for _, p := range packs {
		if p.EffectivePhase1Preamble() == "" {
			t.Errorf("pack %s has empty Phase1Preamble after defaults", p.Category)
		}
		if p.EffectivePhase1ResponseSchema() == "" {
			t.Errorf("pack %s has empty Phase1ResponseSchema after defaults", p.Category)
		}
		if p.EffectivePhase2Preamble() == "" {
			t.Errorf("pack %s has empty Phase2Preamble after defaults", p.Category)
		}
		if p.EffectivePhase2ResponseSchema() == "" {
			t.Errorf("pack %s has empty Phase2ResponseSchema after defaults", p.Category)
		}
		if p.EffectiveToolUseInstructions() == "" {
			t.Errorf("pack %s has empty ToolUseInstructions after defaults", p.Category)
		}
	}
}

// TestBuildPhase2UsesToolUseInstructionsFromPack verifies that per-pack
// ToolUseInstructions are used in the phase-2 prompt.
func TestBuildPhase2UsesToolUseInstructionsFromPack(t *testing.T) {
	packs := []RulePack{
		{
			Category:            RuleCategoryTest,
			Enabled:             true,
			ToolUseInstructions: "custom tool instructions",
			Phase2Preamble:      "custom p2 preamble",
			GuardrailPromptTemplate: "guardrail template with {{test_framework}}",
		},
	}
	builder := NewPromptBuilder(packs)
	mistakes := map[RuleCategory][]Mistake{
		RuleCategoryTest: {{Summary: "m1", Confidence: 0.9}},
	}
	prompt, _ := builder.BuildPhase2(mistakes, PhaseRequest{TestFramework: "go test"})

	if !strings.Contains(prompt, "custom tool instructions") {
		t.Fatalf("BuildPhase2 missing custom tool use instructions:\n%s", prompt)
	}
	if !strings.Contains(prompt, "custom p2 preamble") {
		t.Fatalf("BuildPhase2 missing custom preamble:\n%s", prompt)
	}
	if !strings.Contains(prompt, "guardrail template with go test") {
		t.Fatalf("BuildPhase2 did not resolve {{test_framework}} placeholder:\n%s", prompt)
	}
}

// TestBuildPhase2RecordingInstructions verifies that when
// Phase2RecordingInstructions is set, it replaces the "Return JSON" block.
func TestBuildPhase2RecordingInstructions(t *testing.T) {
	packs := []RulePack{
		{
			Category:                    RuleCategoryTest,
			Enabled:                     true,
			ToolUseInstructions:         "tool use",
			Phase2Preamble:              "preamble",
			GuardrailPromptTemplate:     "template",
			Phase2ResponseSchema:        "should not appear",
			Phase2RecordingInstructions: "use MCP tools to record findings",
		},
	}
	builder := NewPromptBuilder(packs)
	mistakes := map[RuleCategory][]Mistake{
		RuleCategoryTest: {{Summary: "m1", Confidence: 0.9}},
	}
	prompt, _ := builder.BuildPhase2(mistakes, PhaseRequest{})

	if !strings.Contains(prompt, "use MCP tools to record findings") {
		t.Fatalf("BuildPhase2 missing recording instructions:\n%s", prompt)
	}
	if strings.Contains(prompt, "should not appear") {
		t.Fatalf("BuildPhase2 used Phase2ResponseSchema when recording instructions present:\n%s", prompt)
	}
}
