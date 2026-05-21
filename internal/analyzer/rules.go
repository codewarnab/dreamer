package analyzer

import (
	"embed"
	"fmt"
	"path"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

//go:embed rules/*.yaml
var embeddedRulesFS embed.FS

// RuleCategory is one of the six v1 spec categories.
type RuleCategory string

const (
	RuleCategoryLintRule         RuleCategory = "lint-rule"
	RuleCategoryTest             RuleCategory = "test"
	RuleCategoryCICheck          RuleCategory = "ci-check"
	RuleCategoryDoc              RuleCategory = "doc"
	RuleCategoryConfig           RuleCategory = "config"
	RuleCategoryRefactorBoundary RuleCategory = "refactor-boundary"
)

// defaultRuleTimeoutSeconds is the fallback per-rule prompt timeout
// when neither config nor the rule pack supplies one.
const defaultRuleTimeoutSeconds = 45

// AllRuleCategories returns the canonical list of v1 categories in fixed order.
// Keep in sync with the RuleCategory constants above when adding new categories.
func AllRuleCategories() []RuleCategory {
	return []RuleCategory{
		RuleCategoryLintRule,
		RuleCategoryTest,
		RuleCategoryCICheck,
		RuleCategoryDoc,
		RuleCategoryConfig,
		RuleCategoryRefactorBoundary,
	}
}

// defaultPhase1Preamble is the fallback when a rule pack omits phase1_preamble.
const defaultPhase1Preamble = "You are auditing chat transcripts of a developer working with an AI coding assistant."

// defaultPhase1ResponseSchema is the fallback JSON shape for phase-1 responses.
const defaultPhase1ResponseSchema = `{"summary": "<= 2000 chars summarizing themes, in-progress threads, and the mistakes you flagged>",
 "mistakes": {"<category-id>": [{"category": "<id>", "summary": "<one sentence>",
  "evidence_excerpt": "<short quote>", "confidence": 0.0-1.0}]}}`

// defaultPhase2Preamble is the fallback when a rule pack omits phase2_preamble.
const defaultPhase2Preamble = "You are synthesizing guardrails from mistakes found across multiple transcript chunks."

// defaultPhase2ResponseSchema is the fallback JSON shape for phase-2 responses.
const defaultPhase2ResponseSchema = `{"findings": {"<category-id>": [{"category": "<id>", "mistake": "<one sentence>",
 "guardrail": {"kind": "<category>", "tool": "...", "rule": "...", "config_snippet": "...",
  "apply": {"target_file": "...", "strategy": "...", "anchor": "...", "snippet": "..."}},
 "codebase_evidence": [{"path": "...", "lines": "1-10", "symbol": "..."}],
 "confidence": 0.0-1.0}]}}`

// defaultToolUseInstructions is the fallback tool-use guidance for phase 2.
const defaultToolUseInstructions = `You have access to these tools to verify findings against the actual codebase:
- Grep(pattern, path): search for patterns in files
- Read(file_path, offset, limit): read specific file contents
- Glob(pattern): find files by pattern

For each mistake:
1. Use Grep to find the relevant code patterns in the codebase
2. Read the specific files to understand the actual implementation
3. Only propose a guardrail if you can cite real code evidence (path, lines, symbol)
4. If the mistake doesn't match the actual code, skip it`

// RulePack is the parsed YAML rule pack for one category.
type RulePack struct {
	Category                RuleCategory   `yaml:"category"`
	Enabled                 bool           `yaml:"enabled"`
	Threshold               float64        `yaml:"threshold"`
	TimeoutSeconds          int            `yaml:"timeout_seconds"`
	MistakePromptTemplate   string         `yaml:"mistake_prompt_template"`
	GuardrailPromptTemplate string         `yaml:"guardrail_prompt_template"`
	ResponseSchema          map[string]any `yaml:"response_schema"`

	// Prompt assembly pieces (new in tool-enabled phase 2).
	Phase1Preamble            string `yaml:"phase1_preamble"`
	Phase1CategoryDescription string `yaml:"phase1_category_description"`
	Phase1ResponseSchema      string `yaml:"phase1_response_schema"`
	Phase2Preamble            string `yaml:"phase2_preamble"`
	Phase2ResponseSchema      string `yaml:"phase2_response_schema"`
}

// EffectivePhase1Preamble returns the pack's preamble or the default.
func (r RulePack) EffectivePhase1Preamble() string {
	if s := strings.TrimSpace(r.Phase1Preamble); s != "" {
		return s
	}
	return defaultPhase1Preamble
}

// EffectivePhase1CategoryDescription returns the pack's category description
// or the category string itself as fallback.
func (r RulePack) EffectivePhase1CategoryDescription() string {
	if s := strings.TrimSpace(r.Phase1CategoryDescription); s != "" {
		return s
	}
	return string(r.Category)
}

// EffectivePhase1ResponseSchema returns the pack's response schema hint or the default.
func (r RulePack) EffectivePhase1ResponseSchema() string {
	if s := strings.TrimSpace(r.Phase1ResponseSchema); s != "" {
		return s
	}
	return defaultPhase1ResponseSchema
}

// EffectivePhase2Preamble returns the pack's preamble or the default.
func (r RulePack) EffectivePhase2Preamble() string {
	if s := strings.TrimSpace(r.Phase2Preamble); s != "" {
		return s
	}
	return defaultPhase2Preamble
}

// EffectivePhase2ResponseSchema returns the pack's response schema hint or the default.
func (r RulePack) EffectivePhase2ResponseSchema() string {
	if s := strings.TrimSpace(r.Phase2ResponseSchema); s != "" {
		return s
	}
	return defaultPhase2ResponseSchema
}

// Timeout returns the duration form of TimeoutSeconds, falling back to
// the computed default when unset or non-positive.
func (r RulePack) Timeout() time.Duration {
	if r.TimeoutSeconds <= 0 {
		return time.Duration(defaultRuleTimeoutSeconds) * time.Second
	}
	return time.Duration(r.TimeoutSeconds) * time.Second
}

// LoadDefaultRulePacks parses the embedded built-in rule pack YAMLs in the
// fixed category order returned by AllRuleCategories.
func LoadDefaultRulePacks() ([]RulePack, error) {
	categories := AllRuleCategories()
	packs := make([]RulePack, 0, len(categories))
	for _, category := range categories {
		pack, err := loadEmbeddedRulePack(category)
		if err != nil {
			return nil, err
		}
		packs = append(packs, pack)
	}
	return packs, nil
}

func loadEmbeddedRulePack(category RuleCategory) (RulePack, error) {
	name := path.Join("rules", string(category)+".yaml")
	data, err := embeddedRulesFS.ReadFile(name)
	if err != nil {
		return RulePack{}, fmt.Errorf("read embedded rule pack %q: %w", name, err)
	}
	return parseRulePack(data, string(category))
}

func parseRulePack(data []byte, label string) (RulePack, error) {
	var pack RulePack
	if err := yaml.Unmarshal(data, &pack); err != nil {
		return RulePack{}, fmt.Errorf("parse rule pack %q: %w", label, err)
	}
	pack.Category = RuleCategory(strings.TrimSpace(string(pack.Category)))
	if pack.Category == "" {
		return RulePack{}, fmt.Errorf("rule pack %q missing category", label)
	}
	return pack, nil
}

// FormatTemplate fills `{{key}}` placeholders in template with values from
// vars. Unknown placeholders are left as-is. Whitespace is preserved.
func FormatTemplate(template string, vars map[string]string) string {
	if template == "" {
		return template
	}
	result := template
	for k, v := range vars {
		result = strings.ReplaceAll(result, "{{"+k+"}}", v)
	}
	return result
}
