package mcpserver_test

import (
	"testing"

	"dreamer/internal/analyzer"
	"dreamer/internal/mcpserver"
)

// TestValidCategoriesMatchesAnalyzer asserts that mcpserver.ValidCategories
// stays in sync with the canonical list in analyzer.AllRuleCategories().
// If a new category is added to rules.go but not to validation.go,
// validation will reject the new category at record time.
func TestValidCategoriesMatchesAnalyzer(t *testing.T) {
	canonical := analyzer.AllRuleCategories()
	for _, cat := range canonical {
		if !mcpserver.ValidCategories[string(cat)] {
			t.Errorf("mcpserver.ValidCategories missing category %q (present in analyzer.AllRuleCategories)", cat)
		}
	}
	for cat := range mcpserver.ValidCategories {
		found := false
		for _, c := range canonical {
			if string(c) == cat {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("mcpserver.ValidCategories has extra category %q (not in analyzer.AllRuleCategories)", cat)
		}
	}
}
