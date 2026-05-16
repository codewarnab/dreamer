package lintrules

// ESLintRules is the curated allow-list of common ESLint rule ids and
// well-known plugin prefixes. The matcher in registry.go treats anything in
// this set as allowed; unknown ids fall to the `[unverified]` tag in
// permissive mode.
var ESLintRules = stringSet(
	// Core rules (subset of the most common ones; not exhaustive).
	"no-unused-vars", "no-undef", "no-console", "no-debugger",
	"no-empty", "no-redeclare", "no-prototype-builtins", "no-fallthrough",
	"no-var", "prefer-const", "prefer-arrow-callback", "prefer-template",
	"eqeqeq", "no-else-return", "no-implicit-globals",
	"no-unused-expressions", "no-shadow", "no-multi-assign",
	"no-throw-literal", "no-mixed-operators", "no-misleading-character-class",
	"no-async-promise-executor", "no-await-in-loop", "no-return-await",
	"require-await", "no-floating-promises",
	"complexity", "consistent-return", "curly", "default-case",
	"max-classes-per-file", "max-depth", "max-lines", "max-lines-per-function",
	"max-nested-callbacks", "max-params", "max-statements",
	"no-array-constructor", "no-bitwise", "no-empty-function",
	"no-eval", "no-implied-eval", "no-loop-func",
	"no-magic-numbers", "no-new-func", "no-new-wrappers",
	"no-octal", "no-octal-escape", "no-param-reassign",
	"no-plusplus", "no-restricted-syntax", "no-script-url",
	"no-self-assign", "no-self-compare", "no-sequences",
	"no-unmodified-loop-condition", "no-unreachable", "no-useless-call",
	"no-useless-concat", "no-useless-return", "no-with",
	"prefer-named-capture-group", "prefer-promise-reject-errors",
	"radix", "require-unicode-regexp", "vars-on-top", "wrap-iife", "yoda",
)

// ESLintPluginPrefixes recognised by the matcher. Rule ids prefixed by any
// of these strings are considered "known" even if not enumerated in
// ESLintRules.
var ESLintPluginPrefixes = []string{
	"@typescript-eslint/",
	"react/",
	"react-hooks/",
	"jsx-a11y/",
	"import/",
	"unicorn/",
	"promise/",
	"node/",
	"security/",
	"sonarjs/",
}
