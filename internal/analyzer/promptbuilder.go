package analyzer

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// PromptBuilder assembles multi-category phase-1 and phase-2 prompts.
type PromptBuilder struct {
	Packs []RulePack
}

// NewPromptBuilder returns a builder over the enabled packs in `packs`.
func NewPromptBuilder(packs []RulePack) *PromptBuilder {
	return &PromptBuilder{Packs: packs}
}

// EnabledCategories returns the ordered list of enabled rule categories.
func (b *PromptBuilder) EnabledCategories() []RuleCategory {
	out := make([]RuleCategory, 0, len(b.Packs))
	for _, p := range b.Packs {
		if p.Enabled {
			out = append(out, p.Category)
		}
	}
	return out
}

// writeGroundingPreamble writes the project-root + toolchain block shared by
// phase-1 and phase-2 prompts. Empty fields are skipped.
func writeGroundingPreamble(sb *strings.Builder, req PhaseRequest) {
	if s := strings.TrimSpace(req.ProjectRoot); s != "" {
		fmt.Fprintf(sb, "Project root: %s\n", s)
	}
	if s := strings.TrimSpace(req.ToolchainSummary); s != "" {
		fmt.Fprintf(sb, "Toolchain: %s\n", s)
	}
	if s := strings.TrimSpace(req.PrimaryLinter); s != "" {
		fmt.Fprintf(sb, "Primary linter: %s\n", s)
	}
	if s := strings.TrimSpace(req.TestFramework); s != "" {
		fmt.Fprintf(sb, "Test framework: %s\n", s)
	}
}

// firstEnabledPack returns a pointer to the first enabled pack, or nil.
func firstEnabledPack(packs []RulePack) *RulePack {
	for i := range packs {
		if packs[i].Enabled {
			return &packs[i]
		}
	}
	return nil
}

// BuildPhase1 builds the prompt for one chunk. chunkIndex is 0-based; total is K.
// priorSummary is the prior chunk's summary (sequential K>1 only); empty otherwise.
// Prompt assembly uses YAML-driven preamble, category descriptions, and response schema.
func (b *PromptBuilder) BuildPhase1(chunk Chunk, req PhaseRequest, priorSummary string, total int) string {
	var sb strings.Builder

	// Preamble from first enabled pack (all packs share the same preamble).
	if p := firstEnabledPack(b.Packs); p != nil {
		sb.WriteString(p.EffectivePhase1Preamble())
		sb.WriteString("\n")
	}
	writeGroundingPreamble(&sb, req)
	sb.WriteString("\nCodebase context:\n")
	sb.WriteString(req.CodebaseContext)
	sb.WriteString("\n\n")

	if priorSummary != "" {
		sb.WriteString("<rolling_context>\nPrior chunks covered:\n")
		sb.WriteString(priorSummary)
		sb.WriteString("\n</rolling_context>\n\n")
	}

	sb.WriteString("Identify recurring mistakes the assistant made, across these categories:\n")
	for _, p := range b.Packs {
		if p.Enabled {
			fmt.Fprintf(&sb, "- %s: %s\n", p.Category, p.EffectivePhase1CategoryDescription())
		}
	}
	sb.WriteString("\n")

	// Per-category mistake prompt hints (optional detail from YAML).
	for _, p := range b.Packs {
		if p.Enabled && p.MistakePromptTemplate != "" {
			fmt.Fprintf(&sb, "<%s_guidance>\n", p.Category)
			sb.WriteString(strings.TrimSpace(p.MistakePromptTemplate))
			fmt.Fprintf(&sb, "\n</%s_guidance>\n\n", p.Category)
		}
	}

	labels := strings.Join(chunk.SourceLabels, ",")
	fmt.Fprintf(&sb, "<transcript_chunk index=\"%d\" of=\"%d\" sources=\"%s\">\n", chunk.Index+1, total, labels)
	sb.WriteString(chunk.Transcript)
	sb.WriteString("\n</transcript_chunk>\n\n")

	sb.WriteString("Return JSON only with this exact shape:\n")
	// Response schema from first enabled pack.
	if p := firstEnabledPack(b.Packs); p != nil {
		sb.WriteString(p.EffectivePhase1ResponseSchema())
	} else {
		sb.WriteString(defaultPhase1ResponseSchema)
	}
	sb.WriteString("\n\n")
	sb.WriteString("Only emit mistakes you can quote evidence for. Omit a category if no mistakes apply.\n")
	sb.WriteString("The \"summary\" field is REQUIRED on every chunk.\n")
	return sb.String()
}

// BuildPhase2 builds the single guardrail-synthesis prompt. Instead of dumping
// file paths, tool-use instructions tell the LLM to verify findings with
// Grep/Read/Glob. Per-category guardrail_prompt_template provides the
// verification strategy and output schema.
func (b *PromptBuilder) BuildPhase2(mistakesByCategory map[RuleCategory][]Mistake, _ []string, req PhaseRequest) (string, []string) {
	enabled := b.EnabledCategories()

	var sb strings.Builder

	// Preamble from first enabled pack.
	if p := firstEnabledPack(b.Packs); p != nil {
		sb.WriteString(p.EffectivePhase2Preamble())
		sb.WriteString("\n")
	}
	writeGroundingPreamble(&sb, req)
	sb.WriteString("\n")

	// Tool-use instructions: aggregate from enabled packs.
	seenInstructions := map[string]bool{}
	for _, p := range b.Packs {
		if !p.Enabled {
			continue
		}
		instr := p.EffectiveToolUseInstructions()
		if instr == "" || seenInstructions[instr] {
			continue
		}
		seenInstructions[instr] = true
		sb.WriteString(instr)
		sb.WriteString("\n\n")
	}

	// Per-category guardrail prompt templates.
	for _, p := range b.Packs {
		if !p.Enabled {
			continue
		}
		if tmpl := strings.TrimSpace(p.GuardrailPromptTemplate); tmpl != "" {
			fmt.Fprintf(&sb, "<%s_guardrail>\n", p.Category)
			sb.WriteString(tmpl)
			fmt.Fprintf(&sb, "\n</%s_guardrail>\n\n", p.Category)
		}
	}

	sb.WriteString("Mistakes detected (per category):\n")
	payload := orderMistakesByCategory(mistakesByCategory, enabled)
	rendered, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		rendered = []byte("{}")
	}
	sb.Write(rendered)
	sb.WriteString("\n\n")

	sb.WriteString("Return JSON only with this exact shape:\n")
	// Response schema from first enabled pack.
	if p := firstEnabledPack(b.Packs); p != nil {
		sb.WriteString(p.EffectivePhase2ResponseSchema())
	} else {
		sb.WriteString(defaultPhase2ResponseSchema)
	}
	sb.WriteString("\n")

	return sb.String(), nil
}

// orderMistakesByCategory returns an ordered map keyed by enabled category.
// Empty categories are omitted. All mistakes are included (no cap).
func orderMistakesByCategory(in map[RuleCategory][]Mistake, order []RuleCategory) map[string][]Mistake {
	out := map[string][]Mistake{}
	for _, c := range order {
		ms := in[c]
		if len(ms) == 0 {
			continue
		}
		out[string(c)] = ms
	}
	return out
}

// parsePhase1Response parses {"summary": "...", "mistakes": {cat: [...]}}.
// Returns mistakes-by-category, the summary string, and a list of per-pack warnings.
func parsePhase1Response(raw string, packs []RulePack) (map[RuleCategory][]Mistake, string, []string, error) {
	payload := stripCodeFence(raw)
	if payload == "" {
		return map[RuleCategory][]Mistake{}, "", nil, fmt.Errorf("empty phase-1 response")
	}
	var parsed struct {
		Summary  string                  `json:"summary"`
		Mistakes map[string][]rawMistake `json:"mistakes"`
	}
	if err := json.Unmarshal([]byte(payload), &parsed); err != nil {
		return nil, "", nil, fmt.Errorf("invalid phase-1 JSON: %w", err)
	}

	categoryByID := map[string]RulePack{}
	for _, p := range packs {
		categoryByID[string(p.Category)] = p
	}
	out := map[RuleCategory][]Mistake{}
	warnings := []string{}
	for catID, rawList := range parsed.Mistakes {
		pack, ok := categoryByID[catID]
		if !ok || !pack.Enabled {
			warnings = append(warnings, fmt.Sprintf("phase-1 returned unknown/disabled category %q; dropped", catID))
			continue
		}
		ms := normalizeMistakes(toMistakes(rawList, pack.Category), pack.Category)
		ms = filterMistakesByThreshold(ms, pack.Threshold)
		if len(ms) > 0 {
			out[pack.Category] = append(out[pack.Category], ms...)
		}
	}
	summary := clampSummary(parsed.Summary)
	return out, summary, warnings, nil
}

// rawMistake mirrors phase-1 JSON; tolerant of missing fields.
type rawMistake struct {
	Category        string  `json:"category"`
	Summary         string  `json:"summary"`
	EvidenceExcerpt string  `json:"evidence_excerpt"`
	Confidence      float64 `json:"confidence"`
}

func toMistakes(raws []rawMistake, defaultCategory RuleCategory) []Mistake {
	out := make([]Mistake, 0, len(raws))
	for _, r := range raws {
		out = append(out, Mistake{
			Category:        RuleCategory(strings.TrimSpace(r.Category)),
			Summary:         r.Summary,
			EvidenceExcerpt: r.EvidenceExcerpt,
			Confidence:      r.Confidence,
		})
	}
	return out
}

const phase1SummaryMaxRunes = 2000

func clampSummary(s string) string {
	s = strings.TrimSpace(s)
	runes := []rune(s)
	if len(runes) <= phase1SummaryMaxRunes {
		return s
	}
	return string(runes[:phase1SummaryMaxRunes])
}

// parsePhase2Response parses {"findings": {cat: [...]}}.
func parsePhase2Response(raw string, packs []RulePack) (map[RuleCategory][]Finding, []string, error) {
	payload := stripCodeFence(raw)
	if payload == "" {
		return map[RuleCategory][]Finding{}, nil, nil
	}
	var parsed struct {
		Findings map[string][]rawFinding `json:"findings"`
	}
	if err := json.Unmarshal([]byte(payload), &parsed); err != nil {
		return nil, nil, fmt.Errorf("invalid phase-2 JSON: %w", err)
	}

	categoryByID := map[string]RulePack{}
	for _, p := range packs {
		categoryByID[string(p.Category)] = p
	}
	out := map[RuleCategory][]Finding{}
	warnings := []string{}
	for catID, rawList := range parsed.Findings {
		pack, ok := categoryByID[catID]
		if !ok || !pack.Enabled {
			warnings = append(warnings, fmt.Sprintf("phase-2 returned unknown/disabled category %q; dropped", catID))
			continue
		}
		mat := materializeFindings(rawList, pack.Category)
		if len(mat) > 0 {
			out[pack.Category] = append(out[pack.Category], mat...)
		}
	}
	return out, warnings, nil
}

// orderedByCategory flattens a category map in canonical RuleCategory order.
// Empty categories produce no output; absent categories are silently skipped.
func orderedByCategory[T any](in map[RuleCategory][]T, order []RuleCategory) []T {
	out := []T{}
	for _, c := range order {
		out = append(out, in[c]...)
	}
	return out
}

// sortedCategoryKeys returns sorted keys, for stable JSON serialization in tests.
func sortedCategoryKeys(m map[RuleCategory][]Mistake) []RuleCategory {
	keys := make([]RuleCategory, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return string(keys[i]) < string(keys[j]) })
	return keys
}
