package categories

import (
	"testing"
)

func TestAllReturnsSixCategories(t *testing.T) {
	cats := All()
	if len(cats) != 6 {
		t.Fatalf("All() returned %d categories, want 6", len(cats))
	}
}

func TestAllContainsExpectedCategories(t *testing.T) {
	cats := All()
	expected := map[Category]bool{
		CategoryLintRule:         false,
		CategoryTest:             false,
		CategoryCICheck:          false,
		CategoryDoc:              false,
		CategoryConfig:           false,
		CategoryRefactorBoundary: false,
	}
	for _, c := range cats {
		if _, ok := expected[c]; !ok {
			t.Errorf("unexpected category %q in All()", c)
		}
		expected[c] = true
	}
	for c, found := range expected {
		if !found {
			t.Errorf("missing category %q from All()", c)
		}
	}
}

func TestCategoryConstants(t *testing.T) {
	tests := []struct {
		cat  Category
		want string
	}{
		{CategoryLintRule, "lint-rule"},
		{CategoryTest, "test"},
		{CategoryCICheck, "ci-check"},
		{CategoryDoc, "doc"},
		{CategoryConfig, "config"},
		{CategoryRefactorBoundary, "refactor-boundary"},
	}
	for _, tt := range tests {
		if string(tt.cat) != tt.want {
			t.Errorf("Category %v = %q, want %q", tt.cat, string(tt.cat), tt.want)
		}
	}
}

func TestCategoryDistinct(t *testing.T) {
	cats := All()
	seen := make(map[Category]bool, len(cats))
	for _, c := range cats {
		if seen[c] {
			t.Errorf("duplicate category %q in All()", c)
		}
		seen[c] = true
	}
}
