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


// PromptDefaults holds the global prompt text loaded from defaults.yaml.
// It is intentionally a separate type from RulePack because defaults.yaml
// has no category field and parseRulePack validates category presence.
//
// When adding a new field here, also update applyDefaults and the sync
// guard test TestApplyDefaultsCopiesAllFields.
type PromptDefaults struct {
	Phase1Preamble              string `yaml:"phase1_preamble"`
	Phase1ResponseSchema        string `yaml:"phase1_response_schema"`
	Phase2Preamble              string `yaml:"phase2_preamble"`
	Phase2ResponseSchema        string `yaml:"phase2_response_schema"`
	ToolUseInstructions         string `yaml:"tool_use_instructions"`
	Phase2RecordingInstructions string `yaml:"phase2_recording_instructions"`
}

// RulePack is the parsed YAML rule pack for one category.
type RulePack struct {
	Category                RuleCategory   `yaml:"category"`
	Enabled                 bool           `yaml:"enabled"`
	Threshold               float64        `yaml:"threshold"`
	TimeoutSeconds          int            `yaml:"timeout_seconds"`
	MistakePromptTemplate   string         `yaml:"mistake_prompt_template"`
	GuardrailPromptTemplate string         `yaml:"guardrail_prompt_template"`
	ResponseSchema          map[string]any `yaml:"response_schema"`

	// Prompt assembly pieces.
	Phase1Preamble            string `yaml:"phase1_preamble"`
	Phase1CategoryDescription string `yaml:"phase1_category_description"`
	Phase1ResponseSchema      string `yaml:"phase1_response_schema"`
	Phase2Preamble            string `yaml:"phase2_preamble"`
	Phase2ResponseSchema      string `yaml:"phase2_response_schema"`

	// Tool-use and recording hook (loaded from defaults.yaml, overridable per-category).
	ToolUseInstructions         string `yaml:"tool_use_instructions"`
	Phase2RecordingInstructions string `yaml:"phase2_recording_instructions"`
}

// EffectivePhase1Preamble returns the pack's preamble or empty string.
// The defaults.yaml value is applied by applyDefaults in LoadDefaultRulePacks
// before callers reach this method.
func (r RulePack) EffectivePhase1Preamble() string {
	return strings.TrimSpace(r.Phase1Preamble)
}

// EffectivePhase1CategoryDescription returns the pack's category description
// or the category string itself as fallback.
func (r RulePack) EffectivePhase1CategoryDescription() string {
	if s := strings.TrimSpace(r.Phase1CategoryDescription); s != "" {
		return s
	}
	return string(r.Category)
}

// EffectivePhase1ResponseSchema returns the pack's response schema hint or empty.
func (r RulePack) EffectivePhase1ResponseSchema() string {
	return strings.TrimSpace(r.Phase1ResponseSchema)
}

// EffectivePhase2Preamble returns the pack's preamble or empty.
func (r RulePack) EffectivePhase2Preamble() string {
	return strings.TrimSpace(r.Phase2Preamble)
}

// EffectivePhase2ResponseSchema returns the pack's response schema hint or empty.
func (r RulePack) EffectivePhase2ResponseSchema() string {
	return strings.TrimSpace(r.Phase2ResponseSchema)
}

// EffectiveToolUseInstructions returns the pack's tool-use instructions or empty.
func (r RulePack) EffectiveToolUseInstructions() string {
	return strings.TrimSpace(r.ToolUseInstructions)
}

// EffectivePhase2RecordingInstructions returns the pack's recording instructions or empty.
func (r RulePack) EffectivePhase2RecordingInstructions() string {
	return strings.TrimSpace(r.Phase2RecordingInstructions)
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
// fixed category order returned by AllRuleCategories. Global prompt defaults
// from defaults.yaml are applied as the base layer; per-category YAML and
// config overrides take precedence.
func LoadDefaultRulePacks() ([]RulePack, error) {
	defaults, err := loadDefaultsYAML()
	if err != nil {
		return nil, err
	}

	categories := AllRuleCategories()
	packs := make([]RulePack, 0, len(categories))
	for _, category := range categories {
		pack, err := loadEmbeddedRulePack(category)
		if err != nil {
			return nil, err
		}
		applyDefaults(&pack, defaults)
		packs = append(packs, pack)
	}
	return packs, nil
}

// loadDefaultsYAML reads the embedded defaults.yaml into a PromptDefaults struct.
func loadDefaultsYAML() (PromptDefaults, error) {
	data, err := embeddedRulesFS.ReadFile("rules/defaults.yaml")
	if err != nil {
		panic(fmt.Sprintf("dreamer: embedded defaults.yaml missing: %v", err))
	}
	var d PromptDefaults
	if err := yaml.Unmarshal(data, &d); err != nil {
		return PromptDefaults{}, fmt.Errorf("parse defaults.yaml: %w", err)
	}
	return d, nil
}

// applyDefaults fills empty RulePack fields from PromptDefaults.
// Per-category YAML and config overrides still take precedence because
// they're applied later by applyRuleToggles.
func applyDefaults(pack *RulePack, d PromptDefaults) {
	if pack.Phase1Preamble == "" {
		pack.Phase1Preamble = d.Phase1Preamble
	}
	if pack.Phase1ResponseSchema == "" {
		pack.Phase1ResponseSchema = d.Phase1ResponseSchema
	}
	if pack.Phase2Preamble == "" {
		pack.Phase2Preamble = d.Phase2Preamble
	}
	if pack.Phase2ResponseSchema == "" {
		pack.Phase2ResponseSchema = d.Phase2ResponseSchema
	}
	if pack.ToolUseInstructions == "" {
		pack.ToolUseInstructions = d.ToolUseInstructions
	}
	if pack.Phase2RecordingInstructions == "" {
		pack.Phase2RecordingInstructions = d.Phase2RecordingInstructions
	}
}

func loadEmbeddedRulePack(category RuleCategory) (RulePack, error) {
	name := path.Join("rules", string(category)+".yaml")
	ruleFileBytes, err := embeddedRulesFS.ReadFile(name)
	if err != nil {
		return RulePack{}, fmt.Errorf("read embedded rule pack %q: %w", name, err)
	}
	return parseRulePack(ruleFileBytes, string(category))
}

func parseRulePack(ruleFileBytes []byte, label string) (RulePack, error) {
	var pack RulePack
	if err := yaml.Unmarshal(ruleFileBytes, &pack); err != nil {
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
	resolvedTemplate := template
	for k, v := range vars {
		resolvedTemplate = strings.ReplaceAll(resolvedTemplate, "{{"+k+"}}", v)
	}
	return resolvedTemplate
}
