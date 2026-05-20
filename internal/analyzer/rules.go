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

// DefaultRuleTimeoutSeconds is the fallback per-rule prompt timeout
// when a rule pack's timeout_seconds is unset or non-positive.
const DefaultRuleTimeoutSeconds = 45

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
		return DefaultRuleTimeoutSeconds * time.Second
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
