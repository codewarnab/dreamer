package stringconsttest

// --- BAD patterns: repeated string literals that should be constants ---

func badPermMode1() string {
	return "permission-mode" // want `string "permission-mode" repeated 3 times across 2 files; extract to a constant`
}

func badPermMode2() string {
	return "permission-mode" // want `string "permission-mode" repeated 3 times across 2 files; extract to a constant`
}

func badBypass1() string {
	return "bypassPermissions" // want `string "bypassPermissions" repeated 3 times across 2 files; extract to a constant`
}

func badBypass2() string {
	return "bypassPermissions" // want `string "bypassPermissions" repeated 3 times across 2 files; extract to a constant`
}
