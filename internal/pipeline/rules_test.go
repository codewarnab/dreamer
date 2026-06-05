package pipeline

import (
	"testing"

	"dreamer/internal/analyzer"
	"dreamer/internal/categories"
	"dreamer/internal/config"
)

// testPacks builds a minimal RulePack slice for unit tests.
func testPacks(t *testing.T) []analyzer.RulePack {
	t.Helper()
	return []analyzer.RulePack{
		{Category: categories.LintRule, Enabled: true},
		{Category: categories.Test, Enabled: true},
		{Category: categories.CICheck, Enabled: false},
	}
}

func boolPtr(v bool) *bool { return &v }

func TestAnyEnabled(t *testing.T) {
	tests := []struct {
		name  string
		packs []analyzer.RulePack
		want  bool
	}{
		{"empty slice", nil, false},
		{"all disabled", []analyzer.RulePack{{Category: categories.Test, Enabled: false}}, false},
		{"one enabled", []analyzer.RulePack{
			{Category: categories.Test, Enabled: false},
			{Category: categories.LintRule, Enabled: true},
		}, true},
		{"all enabled", []analyzer.RulePack{
			{Category: categories.Test, Enabled: true},
			{Category: categories.LintRule, Enabled: true},
		}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := anyEnabled(tt.packs); got != tt.want {
				t.Fatalf("anyEnabled() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestMergeRulePacksNilProject(t *testing.T) {
	cfg := &config.App{}
	packs, err := mergeRulePacks(cfg, nil, "")
	if err != nil {
		t.Fatalf("mergeRulePacks: %v", err)
	}
	if len(packs) == 0 {
		t.Fatal("expected packs from embedded defaults")
	}
}

func TestMergeRulePacksGlobalToggle(t *testing.T) {
	cfg := &config.App{
		Analyzer: config.AnalyzerConfig{
			Rules: map[string]config.RuleConfig{
				"lint-rule": {Enabled: boolPtr(false)},
			},
		},
	}
	packs, err := mergeRulePacks(cfg, nil, "")
	if err != nil {
		t.Fatalf("mergeRulePacks: %v", err)
	}
	for _, p := range packs {
		if p.Category == categories.LintRule && p.Enabled {
			t.Fatal("lint-rule should be disabled by global override")
		}
	}
}

func TestMergeRulePacksProjectToggle(t *testing.T) {
	cfg := &config.App{}
	project := &config.ProjectFileConfig{
		Rules: map[string]config.RuleConfig{
			"test": {Enabled: boolPtr(false)},
		},
	}
	packs, err := mergeRulePacks(cfg, project, "")
	if err != nil {
		t.Fatalf("mergeRulePacks: %v", err)
	}
	for _, p := range packs {
		if p.Category == categories.Test && p.Enabled {
			t.Fatal("test should be disabled by project override")
		}
	}
}

func TestMergeRulePacksTimeoutOverride(t *testing.T) {
	const customTimeout = 120
	cfg := &config.App{
		Analyzer: config.AnalyzerConfig{
			RuleTimeoutSeconds: customTimeout,
		},
	}
	packs, err := mergeRulePacks(cfg, nil, "")
	if err != nil {
		t.Fatalf("mergeRulePacks: %v", err)
	}
	for _, p := range packs {
		if p.TimeoutSeconds != customTimeout {
			t.Fatalf("pack %q: TimeoutSeconds = %d, want %d", p.Category, p.TimeoutSeconds, customTimeout)
		}
	}
}

func TestApplyRuleTogglesExactKeyMatch(t *testing.T) {
	packs := testPacks(t)
	overrides := map[string]config.RuleConfig{
		"lint-rule": {Enabled: boolPtr(false)},
	}
	applyRuleToggles(packs, overrides)
	for _, p := range packs {
		if p.Category == categories.LintRule && p.Enabled {
			t.Fatal("lint-rule should be disabled via exact key match")
		}
	}
}

func TestApplyRuleTogglesTemplateOverride(t *testing.T) {
	packs := testPacks(t)
	const customTemplate = "custom mistake prompt"
	overrides := map[string]config.RuleConfig{
		"test": {MistakePromptTemplate: customTemplate},
	}
	applyRuleToggles(packs, overrides)
	for _, p := range packs {
		if p.Category == categories.Test {
			if p.MistakePromptTemplate != customTemplate {
				t.Fatalf("template = %q, want %q", p.MistakePromptTemplate, customTemplate)
			}
		}
	}
}

func TestApplyRuleTogglesEmptyOverridesNoOp(t *testing.T) {
	original := testPacks(t)
	packs := testPacks(t)
	applyRuleToggles(packs, nil)
	for i, p := range packs {
		if p.Enabled != original[i].Enabled {
			t.Fatalf("pack %d: Enabled changed without overrides", i)
		}
	}
}

func TestApplyRuleTogglesUnknownKeyIgnored(t *testing.T) {
	original := testPacks(t)
	packs := testPacks(t)
	overrides := map[string]config.RuleConfig{
		"nonexistent-category": {Enabled: boolPtr(false)},
	}
	applyRuleToggles(packs, overrides)
	for i, p := range packs {
		if p.Enabled != original[i].Enabled {
			t.Fatalf("pack %d: Enabled changed by unknown override key", i)
		}
	}
}
