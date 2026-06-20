package handlers

import (
	"encoding/json"
	"net/http/httptest"
	"testing"

	"dreamer/internal/analyzer"
)

func TestRuleDefaults_CoversAllCategoriesWithText(t *testing.T) {
	packs, err := analyzer.LoadDefaultRulePacks()
	if err != nil {
		t.Fatalf("load default rule packs: %v", err)
	}
	got := ruleDefaults(packs)
	if len(got) != len(analyzer.AllRuleCategories()) {
		t.Fatalf("got %d categories, want %d", len(got), len(analyzer.AllRuleCategories()))
	}
	for _, c := range analyzer.AllRuleCategories() {
		dto, ok := got[string(c)]
		if !ok {
			t.Fatalf("category %q missing from defaults", c)
		}
		if dto.MistakePromptTemplate == "" {
			t.Errorf("category %q: empty mistake_prompt_template", c)
		}
		if dto.GuardrailPromptTemplate == "" {
			t.Errorf("category %q: empty guardrail_prompt_template", c)
		}
		if dto.Phase1CategoryDescription == "" {
			t.Errorf("category %q: empty phase1_category_description", c)
		}
	}
}

func TestRuleDefaults_HTTP(t *testing.T) {
	deps := Deps{}

	r := httptest.NewRequest("GET", "/api/rule-defaults", nil)
	w := httptest.NewRecorder()
	RuleDefaults(deps)(w, r)
	if w.Code != 200 {
		t.Fatalf("GET status %d body=%s", w.Code, w.Body)
	}
	var got map[string]ruleDefaultDTO
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if len(got) == 0 {
		t.Fatalf("response has no categories: %+v", got)
	}
	// Pick a category dynamically so the test survives category renames/additions.
	cats := analyzer.AllRuleCategories()
	if len(cats) == 0 {
		t.Fatal("AllRuleCategories() returned no categories")
	}
	first := string(cats[0])
	if _, ok := got[first]; !ok {
		t.Fatalf("response missing %q category: %+v", first, got)
	}

	rPost := httptest.NewRequest("POST", "/api/rule-defaults", nil)
	wPost := httptest.NewRecorder()
	RuleDefaults(deps)(wPost, rPost)
	if wPost.Code != 405 {
		t.Fatalf("POST status %d, want 405", wPost.Code)
	}
}
