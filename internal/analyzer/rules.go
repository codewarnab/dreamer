package analyzer

import (
	"embed"
	"fmt"
	"path"
	"sort"
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

// AllRuleCategories returns the canonical list of v1 categories in fixed order.
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

// RulePack is the parsed YAML rule pack for one category.
type RulePack struct {
	Category                RuleCategory   `yaml:"category"`
	Enabled                 bool           `yaml:"enabled"`
	Threshold               float64        `yaml:"threshold"`
	TimeoutSeconds          int            `yaml:"timeout_seconds"`
	MistakePromptTemplate   string         `yaml:"mistake_prompt_template"`
	GuardrailPromptTemplate string         `yaml:"guardrail_prompt_template"`
	ResponseSchema          map[string]any `yaml:"response_schema"`
}

// Timeout returns the duration form of TimeoutSeconds, falling back to a
// 45s default when unset or non-positive.
func (r RulePack) Timeout() time.Duration {
	if r.TimeoutSeconds <= 0 {
		return 45 * time.Second
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

// MergeRulePack overlays override fields onto base. Empty / zero override
// fields are ignored so partial project YAMLs work cleanly.
func MergeRulePack(base, override RulePack) RulePack {
	out := base
	if strings.TrimSpace(string(override.Category)) != "" {
		out.Category = override.Category
	}
	out.Enabled = override.Enabled || (base.Enabled && !packExplicitlyDisabled(override))
	if override.Threshold > 0 {
		out.Threshold = override.Threshold
	}
	if override.TimeoutSeconds > 0 {
		out.TimeoutSeconds = override.TimeoutSeconds
	}
	if strings.TrimSpace(override.MistakePromptTemplate) != "" {
		out.MistakePromptTemplate = override.MistakePromptTemplate
	}
	if strings.TrimSpace(override.GuardrailPromptTemplate) != "" {
		out.GuardrailPromptTemplate = override.GuardrailPromptTemplate
	}
	if override.ResponseSchema != nil {
		out.ResponseSchema = override.ResponseSchema
	}
	return out
}

func packExplicitlyDisabled(p RulePack) bool {
	// A pack that comes in with all-zero values shouldn't flip Enabled off.
	// Treat "explicitly disabled" as: any field on the override is set AND
	// Enabled is false.
	if p.Threshold > 0 || p.TimeoutSeconds > 0 ||
		strings.TrimSpace(p.MistakePromptTemplate) != "" ||
		strings.TrimSpace(p.GuardrailPromptTemplate) != "" ||
		p.ResponseSchema != nil {
		return !p.Enabled
	}
	return false
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

// SortRulePacks orders rule packs by category for deterministic iteration.
func SortRulePacks(packs []RulePack) []RulePack {
	sort.SliceStable(packs, func(i, j int) bool {
		return string(packs[i].Category) < string(packs[j].Category)
	})
	return packs
}
