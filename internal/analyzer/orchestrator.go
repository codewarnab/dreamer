package analyzer

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"math"
	"strings"
)

// Mistake is a phase-1 output: a recurring failure mode of the assistant
// against the current codebase.
type Mistake struct {
	Category        RuleCategory `json:"category"`
	Summary         string       `json:"summary"`
	EvidenceExcerpt string       `json:"evidence_excerpt"`
	Confidence      float64      `json:"confidence"`
}

// Guardrail is the spec §3.4 guardrail object emitted by phase 2.
type Guardrail struct {
	Kind          string     `json:"kind"`
	Tool          string     `json:"tool"`
	Rule          string     `json:"rule"`
	ConfigSnippet string     `json:"config_snippet,omitempty"`
	Apply         *ApplySpec `json:"apply,omitempty"`
}

// ApplySpec describes how the web UI can write a guardrail to disk.
type ApplySpec struct {
	TargetFile string `json:"target_file"`
	Strategy   string `json:"strategy"`
	Anchor     string `json:"anchor,omitempty"`
	Snippet    string `json:"snippet"`
}

// CodebaseEvidence is one entry in a finding's evidence array.
type CodebaseEvidence struct {
	Path   string `json:"path"`
	Lines  string `json:"lines,omitempty"`
	Symbol string `json:"symbol,omitempty"`
}

// Finding is a validated, hashed phase-2 output ready to render to todos.md.
type Finding struct {
	Category   RuleCategory       `json:"category"`
	Mistake    string             `json:"mistake"`
	Guardrail  Guardrail          `json:"guardrail"`
	Evidence   []CodebaseEvidence `json:"codebase_evidence,omitempty"`
	Confidence float64            `json:"confidence"`
	Hash       string             `json:"-"`
	Unverified bool               `json:"-"` // permissive allow-list flag
}

// AnalysisResult bundles the orchestrator's outputs.
type AnalysisResult struct {
	Mistakes []Mistake
	Findings []Finding
	Warnings []string
	// CompletedCategories: rule categories that ran to completion without
	// timeout or provider error. Empty findings still counts as completed.
	CompletedCategories []string
}

// PhaseRequest groups grounding + validation inputs shared by all chunks.
// Transcript content lives on the per-chunk Chunk.Transcript field instead.
type PhaseRequest struct {
	ProjectRoot       string
	ToolchainSummary  string
	PrimaryLinter     string
	TestFramework     string
	CodebaseContext   string
	DryRun            bool
	StrictLintRules   bool
	LintRuleValidator LintRuleValidator
	ExistingHashes    map[string]struct{}
}

// LintRuleValidator returns true when tool/rule are in the allow-list for the
// matching linter. Tools without an allow-list table return ok=true.
type LintRuleValidator interface {
	IsAllowed(tool, rule string) (allowed bool, known bool)
}

// Orchestrator runs the two-phase pipeline (spec §7) against a Provider.
type Orchestrator struct {
	Packs []RulePack
}

// NewOrchestrator builds an Orchestrator with the given rule packs (defaults
// if empty).
func NewOrchestrator(packs []RulePack) *Orchestrator {
	if len(packs) == 0 {
		defaults, err := LoadDefaultRulePacks()
		if err != nil {
			// Safe: LoadDefaultRulePacks only fails on go:embed corruption (build-time guarantee).
			panic(fmt.Sprintf("dreamer: load default rule packs: %v", err))
		}
		packs = defaults
	}
	cloned := make([]RulePack, len(packs))
	copy(cloned, packs)
	return &Orchestrator{Packs: cloned}
}

type rawFinding struct {
	Mistake          string             `json:"mistake"`
	Guardrail        Guardrail          `json:"guardrail"`
	CodebaseEvidence []CodebaseEvidence `json:"codebase_evidence"`
	Confidence       float64            `json:"confidence"`
}

func materializeFindings(raws []rawFinding, defaultCategory RuleCategory) []Finding {
	findings := make([]Finding, 0, len(raws))
	for _, raw := range raws {
		mistake := strings.TrimSpace(raw.Mistake)
		if mistake == "" {
			continue
		}
		guardrail := Guardrail{
			Kind:          strings.TrimSpace(raw.Guardrail.Kind),
			Tool:          strings.TrimSpace(raw.Guardrail.Tool),
			Rule:          strings.TrimSpace(raw.Guardrail.Rule),
			ConfigSnippet: strings.TrimSpace(raw.Guardrail.ConfigSnippet),
		}
		if guardrail.Kind == "" {
			guardrail.Kind = string(defaultCategory)
		}
		if raw.Guardrail.Apply != nil {
			guardrail.Apply = &ApplySpec{
				TargetFile: strings.TrimSpace(raw.Guardrail.Apply.TargetFile),
				Strategy:   strings.TrimSpace(raw.Guardrail.Apply.Strategy),
				Anchor:     strings.TrimSpace(raw.Guardrail.Apply.Anchor),
				Snippet:    raw.Guardrail.Apply.Snippet,
			}
			if guardrail.Apply.TargetFile == "" || guardrail.Apply.Snippet == "" {
				guardrail.Apply = nil
			}
		}
		evidence := make([]CodebaseEvidence, 0, len(raw.CodebaseEvidence))
		for _, evidenceItem := range raw.CodebaseEvidence {
			path := strings.TrimSpace(evidenceItem.Path)
			if path == "" {
				continue
			}
			evidence = append(evidence, CodebaseEvidence{
				Path:   path,
				Lines:  strings.TrimSpace(evidenceItem.Lines),
				Symbol: strings.TrimSpace(evidenceItem.Symbol),
			})
		}
		finding := Finding{
			Category:   defaultCategory,
			Mistake:    mistake,
			Guardrail:  guardrail,
			Evidence:   evidence,
			Confidence: raw.Confidence,
		}
		finding.Hash = ComputeFindingHash(finding)
		findings = append(findings, finding)
	}
	return findings
}

func normalizeMistakes(mistakes []Mistake, defaultCategory RuleCategory) []Mistake {
	out := make([]Mistake, 0, len(mistakes))
	for _, mistake := range mistakes {
		summary := strings.TrimSpace(mistake.Summary)
		if summary == "" {
			continue
		}
		category := RuleCategory(strings.TrimSpace(string(mistake.Category)))
		if category == "" {
			category = defaultCategory
		}
		out = append(out, Mistake{
			Category:        category,
			Summary:         summary,
			EvidenceExcerpt: strings.TrimSpace(mistake.EvidenceExcerpt),
			Confidence:      mistake.Confidence,
		})
	}
	return out
}

func filterMistakesByThreshold(mistakes []Mistake, threshold float64) []Mistake {
	if threshold <= 0 {
		return mistakes
	}
	filtered := make([]Mistake, 0, len(mistakes))
	for _, mistake := range mistakes {
		// B14: drop NaN and missing-or-zero confidence; admit only items
		// at or above the threshold. The previous `> 0 && < threshold`
		// guard let `0` and `NaN` slip through.
		if math.IsNaN(mistake.Confidence) || mistake.Confidence < threshold {
			continue
		}
		filtered = append(filtered, mistake)
	}
	return filtered
}

func validateFindings(findings []Finding, pack RulePack, req PhaseRequest) ([]Finding, []string) {
	out := make([]Finding, 0, len(findings))
	warnings := []string{}
	for _, finding := range findings {
		// B14: drop NaN and below-threshold (including missing/zero). The
		// previous `> 0 && < threshold` admitted zero and NaN.
		if pack.Threshold > 0 && (math.IsNaN(finding.Confidence) || finding.Confidence < pack.Threshold) {
			continue
		}
		if pack.Category == RuleCategoryLintRule && req.LintRuleValidator != nil {
			allowed, known := req.LintRuleValidator.IsAllowed(finding.Guardrail.Tool, finding.Guardrail.Rule)
			if known && !allowed {
				if req.StrictLintRules {
					warnings = append(warnings,
						fmt.Sprintf("dropped lint-rule finding %q (rule %q not in %q allow-list)", finding.Mistake, finding.Guardrail.Rule, finding.Guardrail.Tool))
					continue
				}
				finding.Unverified = true
			}
		}
		if _, dup := req.ExistingHashes[finding.Hash]; dup {
			continue
		}
		out = append(out, finding)
	}
	return out, warnings
}

// ComputeFindingHash returns the stable identifier for a finding (spec §11.2).
func ComputeFindingHash(f Finding) string {
	hasher := sha256.New()
	// io.WriteString avoids the []byte(str) allocation on every call;
	// sha256.Hash implements io.StringWriter internally.
	io.WriteString(hasher, strings.ToLower(strings.TrimSpace(string(f.Category)))) //nolint:errcheck
	hasher.Write([]byte{'|'})
	io.WriteString(hasher, strings.ToLower(normalizeWhitespace(f.Mistake))) //nolint:errcheck
	hasher.Write([]byte{'|'})
	io.WriteString(hasher, strings.ToLower(strings.TrimSpace(f.Guardrail.Tool))) //nolint:errcheck
	hasher.Write([]byte{'|'})
	io.WriteString(hasher, strings.ToLower(strings.TrimSpace(f.Guardrail.Rule))) //nolint:errcheck
	return hex.EncodeToString(hasher.Sum(nil))
}

func normalizeWhitespace(text string) string {
	return strings.Join(strings.Fields(text), " ")
}

func stripCodeFence(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if !strings.HasPrefix(trimmed, "```") {
		return trimmed
	}
	lines := strings.Split(trimmed, "\n")
	if len(lines) > 0 && strings.HasPrefix(strings.TrimSpace(lines[0]), "```") {
		lines = lines[1:]
	}
	if len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "```" {
		lines = lines[:len(lines)-1]
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}
