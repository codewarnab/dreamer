package stringconsttest

const (
	permModeConst  = "permission-mode"
	singleDupConst = "single-dup-value"
	tinyConst      = "on!"
)

// --- GOOD patterns: not flagged ---

// goodConst: string already in a const declaration.
func goodConst() string {
	return permModeConst
}

// goodOnce: string used only once (below threshold).
func goodOnce() string {
	return "unique-value"
}

// goodShort: very short string (filtered out).
func goodShort() string {
	return "x"
}

// goodFormat: format string (filtered out).
func goodFormat() string {
	return "hello %s"
}

// goodPath: file path (filtered out).
func goodPath() string {
	return "/tmp/test.json"
}

// goodSentence: natural language (filtered out).
func goodSentence() string {
	return "this is a sentence"
}

// goodTiny: matches tinyConst but is below minConstMatchLen — not flagged.
func goodTiny() string {
	return "on!"
}

// Third occurrence of each bad string (in a different file).
func badPermMode3() string {
	return "permission-mode" // want `string "permission-mode" duplicates constant permModeConst; use the constant`
}

func badBypass3() string {
	return "bypassPermissions" // want `string "bypassPermissions" repeated 3 times across 2 files; extract to a constant`
}
