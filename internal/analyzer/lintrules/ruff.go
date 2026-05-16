package lintrules

// RuffRulePrefixes is the curated set of Ruff rule code prefixes. Any code
// that starts with one of these prefixes followed by digits is considered
// "known" by the validator. Prefixes come from Ruff's rule taxonomy
// (https://docs.astral.sh/ruff/rules/).
var RuffRulePrefixes = []string{
	"F", "E", "W", "C90", "I", "N", "D", "UP", "YTT", "ANN",
	"S", "BLE", "FBT", "B", "A", "COM", "C4", "DTZ", "T10",
	"DJ", "EM", "EXE", "FA", "ISC", "ICN", "G", "INP", "PIE",
	"T20", "PYI", "PT", "Q", "RSE", "RET", "SLF", "SLOT", "SIM",
	"TID", "TCH", "INT", "ARG", "PTH", "ERA", "PD", "PGH", "PL",
	"PLC", "PLE", "PLR", "PLW", "TRY", "FLY", "NPY", "AIR",
	"PERF", "FURB", "LOG", "RUF",
}
