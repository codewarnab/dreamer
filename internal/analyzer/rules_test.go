package analyzer

import "testing"

func TestDefaultRulesIncludesAllCategories(t *testing.T) {
	rules := DefaultRules()

	if got, want := len(rules), 9; got != want {
		t.Fatalf("len(DefaultRules()) = %d, want %d", got, want)
	}

	expectedCategories := map[RuleCategory]bool{
		RuleCategoryBugs:          false,
		RuleCategoryPerformance:   false,
		RuleCategoryDuplication:   false,
		RuleCategoryMissingTests:  false,
		RuleCategoryArchitecture:  false,
		RuleCategoryDocumentation: false,
		RuleCategoryLint:          false,
		RuleCategorySecurity:      false,
		RuleCategoryTypes:         false,
	}

	for _, rule := range rules {
		if _, ok := expectedCategories[rule.Category]; !ok {
			t.Fatalf("unexpected category in defaults: %q", rule.Category)
		}
		expectedCategories[rule.Category] = true

		if rule.PromptTemplate == "" {
			t.Fatalf("rule %q has empty prompt template", rule.Category)
		}
		if rule.Threshold < 0 || rule.Threshold > 1 {
			t.Fatalf("rule %q has invalid threshold: %f", rule.Category, rule.Threshold)
		}
		if !rule.Enabled {
			t.Fatalf("rule %q should be enabled by default", rule.Category)
		}
		if rule.Timeout <= 0 {
			t.Fatalf("rule %q has non-positive timeout: %s", rule.Category, rule.Timeout)
		}
	}

	for category, seen := range expectedCategories {
		if !seen {
			t.Fatalf("missing default rule category: %q", category)
		}
	}
}
