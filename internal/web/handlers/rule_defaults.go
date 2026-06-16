package handlers

import (
	"net/http"
	"strings"

	"dreamer/internal/analyzer"
)

// ruleDefaultDTO is the read-only, per-category set of prompt defaults the
// settings UI seeds its editors with. Only the three genuinely per-category,
// low-risk fields are exposed — the global, parse-critical preamble/schema
// fields are intentionally excluded so the UI never implies they are
// per-category-editable.
type ruleDefaultDTO struct {
	MistakePromptTemplate     string `json:"mistake_prompt_template"`
	GuardrailPromptTemplate   string `json:"guardrail_prompt_template"`
	Phase1CategoryDescription string `json:"phase1_category_description"`
}

// RuleDefaults serves the embedded built-in per-category prompt defaults so the
// settings UI can display them as placeholders and seed "reset to default".
// GET only.
func RuleDefaults(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		packs, err := analyzer.LoadDefaultRulePacks()
		if err != nil {
			http.Error(w, "failed to load rule defaults", http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, ruleDefaults(packs))
	}
}

// ruleDefaults is the pure core: it maps embedded rule packs to a
// category-keyed DTO map. Keyed by the lowercase category string (e.g.
// "test", "lint-rule") to match the UI's rule catalog ids.
func ruleDefaults(packs []analyzer.RulePack) map[string]ruleDefaultDTO {
	out := make(map[string]ruleDefaultDTO, len(packs))
	for _, p := range packs {
		out[string(p.Category)] = ruleDefaultDTO{
			MistakePromptTemplate:     strings.TrimSpace(p.MistakePromptTemplate),
			GuardrailPromptTemplate:   strings.TrimSpace(p.GuardrailPromptTemplate),
			Phase1CategoryDescription: p.EffectivePhase1CategoryDescription(),
		}
	}
	return out
}
