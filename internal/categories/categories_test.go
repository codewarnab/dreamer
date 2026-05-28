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
		LintRule:         false,
		Test:             false,
		CICheck:          false,
		Doc:              false,
		Config:           false,
		RefactorBoundary: false,
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
		{LintRule, "lint-rule"},
		{Test, "test"},
		{CICheck, "ci-check"},
		{Doc, "doc"},
		{Config, "config"},
		{RefactorBoundary, "refactor-boundary"},
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
