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
