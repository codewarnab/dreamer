package lintrules

import (
	"strings"
)

// Validator returns whether a `(tool, rule)` pair is in dreamer's curated
// allow-list. It implements analyzer.LintRuleValidator.
type Validator struct{}

// NewValidator constructs a Validator backed by the curated allow-lists in
// this package.
func NewValidator() *Validator { return &Validator{} }

// IsAllowed reports whether the rule is in the allow-list for the matching
// tool. The second return value indicates whether dreamer has an allow-list
// table for the tool at all. Tools without a table are treated as permissive
// upstream (the orchestrator keeps the finding without the `[unverified]`
// flag).
func (v *Validator) IsAllowed(tool, rule string) (allowed, known bool) {
	normalizedTool := strings.ToLower(strings.TrimSpace(tool))
	trimmedRule := strings.TrimSpace(rule)
	if normalizedTool == "" || trimmedRule == "" {
		return false, false
	}
	switch normalizedTool {
	case "golangci-lint", "golangci":
		return contains(GolangCILintRules, trimmedRule), true
	case "eslint", "@eslint/eslint":
		if contains(ESLintRules, trimmedRule) {
			return true, true
		}
		for _, prefix := range ESLintPluginPrefixes {
			if strings.HasPrefix(trimmedRule, prefix) {
				return true, true
			}
		}
		return false, true
	case "ruff":
		return ruffMatches(trimmedRule), true
	}
	return false, false
}

func ruffMatches(rule string) bool {
	for _, prefix := range RuffRulePrefixes {
		if strings.HasPrefix(rule, prefix) {
			suffix := rule[len(prefix):]
			if len(suffix) > 0 && allDigits(suffix) {
				return true
			}
		}
	}
	return false
}

func allDigits(s string) bool {
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

func contains(set map[string]struct{}, value string) bool {
	_, ok := set[value]
	return ok
}

func stringSet(values ...string) map[string]struct{} {
	out := make(map[string]struct{}, len(values))
	for _, v := range values {
		out[v] = struct{}{}
	}
	return out
}
