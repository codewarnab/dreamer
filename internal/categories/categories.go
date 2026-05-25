// Package categories defines the canonical rule category type and constants
// used by the analyzer, MCP server, and web apply subsystem. This leaf
// package exists to avoid import cycles between those packages.
package categories

// Category is one of the six v1 spec rule categories.
type Category string

const (
	CategoryLintRule         Category = "lint-rule"
	CategoryTest             Category = "test"
	CategoryCICheck          Category = "ci-check"
	CategoryDoc              Category = "doc"
	CategoryConfig           Category = "config"
	CategoryRefactorBoundary Category = "refactor-boundary"
)

// All returns the canonical list of v1 categories.
func All() []Category {
	return []Category{
		CategoryLintRule,
		CategoryTest,
		CategoryCICheck,
		CategoryDoc,
		CategoryConfig,
		CategoryRefactorBoundary,
	}
}
