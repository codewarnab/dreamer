// Package categories defines the canonical rule category type and constants
// used by the analyzer, MCP server, and web apply subsystem. This leaf
// package exists to avoid import cycles between those packages.
package categories

// Category is one of the six v1 spec rule categories.
type Category string

const (
	LintRule         Category = "lint-rule"
	Test             Category = "test"
	CICheck          Category = "ci-check"
	Doc              Category = "doc"
	Config           Category = "config"
	RefactorBoundary Category = "refactor-boundary"
)

// allCategories is a package-level slice to avoid allocating a new slice on every All() call.
// This is a cold-path optimization since category lists are rarely accessed during runtime.
var allCategories = []Category{
	LintRule,
	Test,
	CICheck,
	Doc,
	Config,
	RefactorBoundary,
}

// All returns the canonical list of v1 categories.
func All() []Category {
	return append([]Category(nil), allCategories...)
}

// applyEligible is the single source of truth for which categories the web UI
// is allowed to apply automatically.
//
// Design rationale: previously this set was maintained as a parallel
// map[string]bool literal in internal/web/apply/apply.go (EligibleCategories).
// That meant adding a new category required edits in at least three places
// (categories.go, apply.go, and All()), with no compile-time or test-time
// signal if apply.go was forgotten. By moving the property here — adjacent to
// the category definitions — the decision about auto-apply eligibility is
// co-located with the categories themselves, and apply.go derives its set via
// AllApplyEligible() rather than maintaining its own copy.
//
// Eligibility reasoning per category:
//   - Doc:              safe to auto-apply — documentation changes are additive
//     and easy to review / revert.
//   - LintRule:         safe to auto-apply — config-file snippets for linters;
//     deterministic and reviewable.
//   - CICheck:          safe to auto-apply — CI config additions; scoped to
//     workflow files, not production logic.
//   - Config:           safe to auto-apply — tool/project config snippets with
//     well-defined target files and apply strategies.
//   - Test:             NOT eligible — test changes require human judgement
//     about coverage intent and assertion correctness.
//   - RefactorBoundary: NOT eligible — structural code changes carry risk and
//     must be reviewed before being written to disk.
var applyEligible = map[Category]bool{
	Doc:      true,
	LintRule: true,
	CICheck:  true,
	Config:   true,
}

// ApplyEligible reports whether the web UI is allowed to auto-apply findings
// in this category. The authoritative set is defined by applyEligible above.
func ApplyEligible(c Category) bool {
	return applyEligible[c]
}

// AllApplyEligible returns the subset of All() that are apply-eligible,
// in the same iteration order as All(). Use this to derive UI filters,
// generate documentation, or build test fixtures — do not maintain a
// parallel list elsewhere.
func AllApplyEligible() []Category {
	all := All()
	out := make([]Category, 0, len(applyEligible))
	for _, c := range all {
		if applyEligible[c] {
			out = append(out, c)
		}
	}
	return out
}
