package stringconsttest

// --- BAD patterns: repeated string literals that should be constants ---

func badPermMode1() string {
	return "permission-mode" // want `string "permission-mode" duplicates constant permModeConst; use the constant`
}

func badPermMode2() string {
	return "permission-mode" // want `string "permission-mode" duplicates constant permModeConst; use the constant`
}

// badSingleDup: even a single occurrence is flagged when a same-package
// constant already holds the value.
func badSingleDup() string {
	return "single-dup-value" // want `string "single-dup-value" duplicates constant singleDupConst; use the constant`
}

func badBypass1() string {
	return "bypassPermissions" // want `string "bypassPermissions" repeated 3 times across 2 files; extract to a constant`
}

func badBypass2() string {
	return "bypassPermissions" // want `string "bypassPermissions" repeated 3 times across 2 files; extract to a constant`
}
