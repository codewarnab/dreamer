package mcpserver_test

import (
	"testing"

	"dreamer/internal/analyzer"
	"dreamer/internal/mcpserver"
)

// TestValidCategoriesMatchesAnalyzer asserts that mcpserver.ValidCategories
// contains all built-in categories from analyzer.AllRuleCategories().
// ValidCategories may contain additional entries registered at runtime by
// user-defined rule packs, so only the subset direction is checked:
// built-ins ⊆ ValidCategories.
func TestValidCategoriesMatchesAnalyzer(t *testing.T) {
	canonical := analyzer.AllRuleCategories()
	for _, cat := range canonical {
		if !mcpserver.ValidCategories[string(cat)] {
			t.Errorf("mcpserver.ValidCategories missing category %q (present in analyzer.AllRuleCategories)", cat)
		}
	}
}
