package analyzer

import "time"

type RuleCategory string

const (
	RuleCategoryBugs          RuleCategory = "bugs"
	RuleCategoryPerformance   RuleCategory = "performance"
	RuleCategoryDuplication   RuleCategory = "duplication"
	RuleCategoryMissingTests  RuleCategory = "missing_tests"
	RuleCategoryArchitecture  RuleCategory = "architecture"
	RuleCategoryDocumentation RuleCategory = "documentation"
	RuleCategoryLint          RuleCategory = "lint"
	RuleCategorySecurity      RuleCategory = "security"
	RuleCategoryTypes         RuleCategory = "types"
)

type AnalysisRule struct {
	Category       RuleCategory
	PromptTemplate string
	Threshold      float64
	Enabled        bool
	Timeout        time.Duration
}

func DefaultRules() []AnalysisRule {
	return []AnalysisRule{
		{
			Category:       RuleCategoryBugs,
			PromptTemplate: "Find likely bugs in this code or discussion context. Return JSON with a top-level \"findings\" array of objects that include category, description, evidence, and confidence.\n\n%s",
			Threshold:      0.70,
			Enabled:        true,
			Timeout:        45 * time.Second,
		},
		{
			Category:       RuleCategoryPerformance,
			PromptTemplate: "Find performance issues or scalability concerns. Return JSON with a top-level \"findings\" array of objects that include category, description, evidence, and confidence.\n\n%s",
			Threshold:      0.65,
			Enabled:        true,
			Timeout:        45 * time.Second,
		},
		{
			Category:       RuleCategoryDuplication,
			PromptTemplate: "Find duplicated logic or repeated patterns that should be consolidated. Return JSON with a top-level \"findings\" array of objects that include category, description, evidence, and confidence.\n\n%s",
			Threshold:      0.70,
			Enabled:        true,
			Timeout:        45 * time.Second,
		},
		{
			Category:       RuleCategoryMissingTests,
			PromptTemplate: "Identify missing tests for critical paths, edge cases, or regressions. Return JSON with a top-level \"findings\" array of objects that include category, description, evidence, and confidence.\n\n%s",
			Threshold:      0.60,
			Enabled:        true,
			Timeout:        40 * time.Second,
		},
		{
			Category:       RuleCategoryArchitecture,
			PromptTemplate: "Identify architecture concerns, coupling issues, or boundary violations. Return JSON with a top-level \"findings\" array of objects that include category, description, evidence, and confidence.\n\n%s",
			Threshold:      0.65,
			Enabled:        true,
			Timeout:        60 * time.Second,
		},
		{
			Category:       RuleCategoryDocumentation,
			PromptTemplate: "Identify important documentation gaps that would block future contributors. Return JSON with a top-level \"findings\" array of objects that include category, description, evidence, and confidence.\n\n%s",
			Threshold:      0.55,
			Enabled:        true,
			Timeout:        35 * time.Second,
		},
		{
			Category:       RuleCategoryLint,
			PromptTemplate: "Identify lint-like code quality issues that are likely to cause maintenance or correctness problems. Return JSON with a top-level \"findings\" array of objects that include category, description, evidence, and confidence.\n\n%s",
			Threshold:      0.70,
			Enabled:        true,
			Timeout:        35 * time.Second,
		},
		{
			Category:       RuleCategorySecurity,
			PromptTemplate: "Find security vulnerabilities, unsafe data handling, and secrets risks. Return JSON with a top-level \"findings\" array of objects that include category, description, evidence, and confidence.\n\n%s",
			Threshold:      0.80,
			Enabled:        true,
			Timeout:        60 * time.Second,
		},
		{
			Category:       RuleCategoryTypes,
			PromptTemplate: "Find type-safety and contract issues such as invalid assumptions or nil handling risks. Return JSON with a top-level \"findings\" array of objects that include category, description, evidence, and confidence.\n\n%s",
			Threshold:      0.75,
			Enabled:        true,
			Timeout:        40 * time.Second,
		},
	}
}
