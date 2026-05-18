package analyzer

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// phase1MaxMistakesPerCategory caps the union of phase-1 mistakes sent to phase 2.
// Prevents one runaway category from dominating the phase-2 prompt budget.
const phase1MaxMistakesPerCategory = 20

// phase2MaxCodebaseFiles hard-caps the file list shown to phase 2.
const phase2MaxCodebaseFiles = 500

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

// BuildPhase1 builds the prompt for one chunk. chunkIndex is 0-based; total is K.
// priorSummary is the prior chunk's summary (sequential K>1 only); empty otherwise.
func (b *PromptBuilder) BuildPhase1(chunk Chunk, req PhaseRequest, priorSummary string, total int) string {
	enabled := b.EnabledCategories()
	var sb strings.Builder
	sb.WriteString("You are auditing chat transcripts of a developer working with an AI coding assistant.\n\n")
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
	for _, c := range enabled {
		fmt.Fprintf(&sb, "- %s: %s\n", c, categoryDescription(c))
	}
	sb.WriteString("\n")

	labels := strings.Join(chunk.SourceLabels, ",")
	fmt.Fprintf(&sb, "<transcript_chunk index=\"%d\" of=\"%d\" sources=\"%s\">\n", chunk.Index+1, total, labels)
	sb.WriteString(chunk.Transcript)
	sb.WriteString("\n</transcript_chunk>\n\n")

	sb.WriteString("Return JSON only with this exact shape:\n")
	sb.WriteString(`{
  "summary": "<= 2000 chars summarizing themes, in-progress threads, and the mistakes you flagged>",
  "mistakes": {
    "<category-id>": [
      {"category": "<category-id>", "summary": "<one sentence>",
       "evidence_excerpt": "<short quote from chat>", "confidence": 0.0-1.0}
    ]
  }
}` + "\n\n")
	sb.WriteString("Only emit mistakes you can quote evidence for. Omit a category if no mistakes apply.\n")
	sb.WriteString("The \"summary\" field is REQUIRED on every chunk.\n")
	return sb.String()
}

// BuildPhase2 builds the single guardrail-synthesis prompt. Codebase grounding
// is files-only (one path per line, capped at phase2MaxCodebaseFiles).
func (b *PromptBuilder) BuildPhase2(mistakesByCategory map[RuleCategory][]Mistake, files []string, req PhaseRequest) (string, []string) {
	enabled := b.EnabledCategories()
	capped, fileWarnings := capFileList(files)

	var sb strings.Builder
	sb.WriteString("You are synthesizing guardrails from mistakes found across multiple transcript chunks.\n\n")
	writeGroundingPreamble(&sb, req)
	sb.WriteString("\nCodebase files (path-only):\n")
	for _, p := range capped {
		sb.WriteString(p)
		sb.WriteByte('\n')
	}
	sb.WriteString("\n")

	sb.WriteString("Mistakes detected (top-K per category):\n")
	payload := capMistakesPerCategory(mistakesByCategory, enabled, phase1MaxMistakesPerCategory)
	rendered, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		rendered = []byte("{}")
	}
	sb.Write(rendered)
	sb.WriteString("\n\n")

	sb.WriteString("Return JSON only with this exact shape:\n")
	sb.WriteString(`{
  "findings": {
    "<category-id>": [
      {"category": "<category-id>", "mistake": "<one sentence>",
       "guardrail": {"kind": "<category>", "tool": "...", "rule": "...", "config_snippet": "..."},
       "codebase_evidence": [{"path": "...", "lines": "1-10", "symbol": "..."}],
       "confidence": 0.0-1.0}
    ]
  }
}` + "\n")

	return sb.String(), fileWarnings
}

// capFileList truncates files to phase2MaxCodebaseFiles and emits a warning.
func capFileList(files []string) ([]string, []string) {
	if len(files) <= phase2MaxCodebaseFiles {
		return files, nil
	}
	return files[:phase2MaxCodebaseFiles], []string{
		fmt.Sprintf("phase2 file-list truncated original=%d kept=%d", len(files), phase2MaxCodebaseFiles),
	}
}

// capMistakesPerCategory returns an ordered map keyed by enabled category,
// with each list capped at maxPerCategory. Empty categories are omitted.
func capMistakesPerCategory(in map[RuleCategory][]Mistake, order []RuleCategory, maxPerCategory int) map[string][]Mistake {
	out := map[string][]Mistake{}
	for _, c := range order {
		ms := in[c]
		if len(ms) == 0 {
			continue
		}
		if len(ms) > maxPerCategory {
			ms = ms[:maxPerCategory]
		}
		out[string(c)] = ms
	}
	return out
}

// categoryDescription returns the one-line task summary used in phase-1 prompts.
func categoryDescription(c RuleCategory) string {
	switch c {
	case RuleCategoryLintRule:
		return "static-analysis miss the assistant kept making (would a lint rule have caught it?)"
	case RuleCategoryTest:
		return "regressions or edge cases that a new automated test would have prevented"
	case RuleCategoryCICheck:
		return "process gaps a CI/pre-commit check would have caught before merge"
	case RuleCategoryDoc:
		return "behavior the assistant misunderstood because the docs/comments were missing or wrong"
	case RuleCategoryConfig:
		return "misconfiguration the assistant repeated because the config schema or example lacked a guardrail"
	case RuleCategoryRefactorBoundary:
		return "abstraction or module-boundary violations a structural rule would have flagged"
	default:
		return string(c)
	}
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
