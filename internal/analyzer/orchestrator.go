package analyzer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
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
	Kind          string `json:"kind"`
	Tool          string `json:"tool"`
	Rule          string `json:"rule"`
	ConfigSnippet string `json:"config_snippet,omitempty"`
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
}

// PhaseRequest groups inputs for a single orchestrator run.
type PhaseRequest struct {
	Transcript        string
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

// Run executes phase 1 (mistake extraction) across all enabled categories
// against a single session. If req.DryRun is false and at least one mistake
// is emitted, phase 2 runs.
func (o *Orchestrator) Run(ctx context.Context, session Session, req PhaseRequest) (AnalysisResult, error) {
	if session == nil {
		return AnalysisResult{}, errors.New("analyzer.Orchestrator.Run: session is required")
	}

	result := AnalysisResult{}
	mistakesByCategory := make(map[RuleCategory][]Mistake)
	warnings, err := runPhase[Mistake](
		ctx,
		session,
		o.Packs,
		"phase-1",
		func(pack RulePack) (string, bool) {
			return o.buildMistakePrompt(pack, req), false
		},
		func(raw string, pack RulePack) ([]Mistake, error) {
			mistakes, err := parseMistakeResponse(raw, pack)
			if err != nil {
				return nil, err
			}
			return filterMistakesByThreshold(mistakes, pack.Threshold), nil
		},
		func(pack RulePack, parsed []Mistake) {
			mistakesByCategory[pack.Category] = append(mistakesByCategory[pack.Category], parsed...)
			result.Mistakes = append(result.Mistakes, parsed...)
		},
	)
	result.Warnings = append(result.Warnings, warnings...)
	if err != nil {
		return result, err
	}

	if len(result.Mistakes) == 0 {
		return result, nil
	}
	if req.DryRun {
		return result, nil
	}

	warnings, err = runPhase[Finding](
		ctx,
		session,
		o.Packs,
		"phase-2",
		func(pack RulePack) (string, bool) {
			categoryMistakes := mistakesByCategory[pack.Category]
			if len(categoryMistakes) == 0 {
				return "", true
			}
			return o.buildGuardrailPrompt(pack, req, categoryMistakes), false
		},
		func(raw string, pack RulePack) ([]Finding, error) {
			findings, err := parseGuardrailResponse(raw, pack)
			if err != nil {
				return nil, err
			}
			validated, validationWarnings := validateFindings(findings, pack, req)
			result.Warnings = append(result.Warnings, validationWarnings...)
			return validated, nil
		},
		func(pack RulePack, parsed []Finding) {
			result.Findings = append(result.Findings, parsed...)
		},
	)
	result.Warnings = append(result.Warnings, warnings...)
	if err != nil {
		return result, err
	}
	return result, nil
}

// runPhase executes the shared provider loop for one orchestrator phase.
//
// The caller supplies phase-specific prompt construction, response parsing, and
// acceptance behavior. Provider rate limits abort the entire phase immediately;
// ordinary provider and parse failures are returned as warnings so other packs
// can continue.
func runPhase[T any](
	ctx context.Context,
	session Session,
	packs []RulePack,
	phaseName string,
	buildPrompt func(RulePack) (prompt string, skip bool),
	parse func(raw string, pack RulePack) ([]T, error),
	accept func(pack RulePack, parsed []T),
) ([]string, error) {
	warnings := []string{}
	for _, pack := range packs {
		if !pack.Enabled {
			continue
		}
		prompt, skip := buildPrompt(pack)
		if skip {
			continue
		}
		raw, err := session.Run(ctx, prompt, pack.Timeout())
		if err != nil {
			if errors.Is(err, ErrRateLimited) {
				return warnings, fmt.Errorf("%s %q hit provider rate limit: %w", phaseName, pack.Category, err)
			}
			warnings = append(warnings,
				fmt.Sprintf("%s %q failed (%v); skipping category", phaseName, pack.Category, err))
			continue
		}
		parsed, parseErr := parse(raw, pack)
		if parseErr != nil {
			warnings = append(warnings,
				fmt.Sprintf("%s %q response parse failed (%v); skipping category", phaseName, pack.Category, parseErr))
			continue
		}
		accept(pack, parsed)
	}
	return warnings, nil
}

func (o *Orchestrator) buildMistakePrompt(pack RulePack, req PhaseRequest) string {
	vars := map[string]string{
		"project_root":      req.ProjectRoot,
		"toolchain_summary": req.ToolchainSummary,
		"primary_linter":    req.PrimaryLinter,
		"test_framework":    req.TestFramework,
		"codebase_context":  req.CodebaseContext,
	}
	body := FormatTemplate(pack.MistakePromptTemplate, vars)
	if strings.TrimSpace(req.Transcript) == "" {
		return body
	}
	return body + "\n\nChat transcript follows:\n" + req.Transcript
}

func (o *Orchestrator) buildGuardrailPrompt(pack RulePack, req PhaseRequest, mistakes []Mistake) string {
	rendered, err := json.MarshalIndent(map[string]any{"mistakes": mistakes}, "", "  ")
	if err != nil {
		// json.Marshal can only fail on unsupported types; we control them.
		rendered = []byte("[]")
	}
	vars := map[string]string{
		"project_root":      req.ProjectRoot,
		"toolchain_summary": req.ToolchainSummary,
		"primary_linter":    req.PrimaryLinter,
		"test_framework":    req.TestFramework,
		"codebase_context":  req.CodebaseContext,
		"mistakes":          string(rendered),
	}
	return FormatTemplate(pack.GuardrailPromptTemplate, vars)
}

func parseMistakeResponse(raw string, pack RulePack) ([]Mistake, error) {
	payload := stripCodeFence(raw)
	if payload == "" {
		return nil, nil
	}
	var wrapped struct {
		Mistakes []Mistake `json:"mistakes"`
	}
	if err := json.Unmarshal([]byte(payload), &wrapped); err == nil && wrapped.Mistakes != nil {
		return normalizeMistakes(wrapped.Mistakes, pack.Category), nil
	}
	var bare []Mistake
	if err := json.Unmarshal([]byte(payload), &bare); err != nil {
		return nil, fmt.Errorf("invalid mistakes JSON: %w", err)
	}
	return normalizeMistakes(bare, pack.Category), nil
}

func parseGuardrailResponse(raw string, pack RulePack) ([]Finding, error) {
	payload := stripCodeFence(raw)
	if payload == "" {
		return nil, nil
	}
	var wrapped struct {
		Findings []rawFinding `json:"findings"`
	}
	if err := json.Unmarshal([]byte(payload), &wrapped); err == nil && wrapped.Findings != nil {
		return materializeFindings(wrapped.Findings, pack.Category), nil
	}
	var bare []rawFinding
	if err := json.Unmarshal([]byte(payload), &bare); err != nil {
		return nil, fmt.Errorf("invalid findings JSON: %w", err)
	}
	return materializeFindings(bare, pack.Category), nil
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
		evidence := make([]CodebaseEvidence, 0, len(raw.CodebaseEvidence))
		for _, item := range raw.CodebaseEvidence {
			path := strings.TrimSpace(item.Path)
			if path == "" {
				continue
			}
			evidence = append(evidence, CodebaseEvidence{
				Path:   path,
				Lines:  strings.TrimSpace(item.Lines),
				Symbol: strings.TrimSpace(item.Symbol),
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
	for _, m := range mistakes {
		summary := strings.TrimSpace(m.Summary)
		if summary == "" {
			continue
		}
		category := RuleCategory(strings.TrimSpace(string(m.Category)))
		if category == "" {
			category = defaultCategory
		}
		out = append(out, Mistake{
			Category:        category,
			Summary:         summary,
			EvidenceExcerpt: strings.TrimSpace(m.EvidenceExcerpt),
			Confidence:      m.Confidence,
		})
	}
	return out
}

func filterMistakesByThreshold(mistakes []Mistake, threshold float64) []Mistake {
	if threshold <= 0 {
		return mistakes
	}
	filtered := make([]Mistake, 0, len(mistakes))
	for _, m := range mistakes {
		if m.Confidence > 0 && m.Confidence < threshold {
			continue
		}
		filtered = append(filtered, m)
	}
	return filtered
}

func validateFindings(findings []Finding, pack RulePack, req PhaseRequest) ([]Finding, []string) {
	out := make([]Finding, 0, len(findings))
	warnings := []string{}
	for _, finding := range findings {
		if pack.Threshold > 0 && finding.Confidence > 0 && finding.Confidence < pack.Threshold {
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
	hasher.Write([]byte(strings.ToLower(strings.TrimSpace(string(f.Category)))))
	hasher.Write([]byte{'|'})
	hasher.Write([]byte(strings.ToLower(normalizeWhitespace(f.Mistake))))
	hasher.Write([]byte{'|'})
	hasher.Write([]byte(strings.ToLower(strings.TrimSpace(f.Guardrail.Tool))))
	hasher.Write([]byte{'|'})
	hasher.Write([]byte(strings.ToLower(strings.TrimSpace(f.Guardrail.Rule))))
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
