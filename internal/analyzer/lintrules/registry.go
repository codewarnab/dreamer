package lintrules

import (
	"regexp"
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
	t := strings.ToLower(strings.TrimSpace(tool))
	r := strings.TrimSpace(rule)
	if t == "" || r == "" {
		return false, false
	}
	switch t {
	case "golangci-lint", "golangci":
		return contains(GolangCILintRules, r), true
	case "eslint", "@eslint/eslint":
		if contains(ESLintRules, r) {
			return true, true
		}
		for _, prefix := range ESLintPluginPrefixes {
			if strings.HasPrefix(r, prefix) {
				return true, true
			}
		}
		return false, true
	case "ruff":
		return ruffMatches(r), true
	}
	return false, false
}

var ruffCodeRe = regexp.MustCompile(`^([A-Z]+)[0-9]+$`)

func ruffMatches(rule string) bool {
	match := ruffCodeRe.FindStringSubmatch(rule)
	if len(match) < 2 {
		return false
	}
	prefix := match[1]
	for _, candidate := range RuffRulePrefixes {
		if candidate == prefix {
			return true
		}
	}
	return false
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
