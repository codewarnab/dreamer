package grounding

import (
	"fmt"
	"strings"
)

// BuildContext returns a string suitable for the `{{codebase_context}}`
// template variable in phase-2 prompts (spec §7.4). It bundles a capped file
// list with an optional symbol index.
func BuildContext(files []string, symbols []Symbol, fileCap, symbolCap int) string {
	var b strings.Builder
	if fileCap > 0 && len(files) > fileCap {
		files = files[:fileCap]
	}
	b.WriteString(fmt.Sprintf("Files (%d shown):\n", len(files)))
	for _, file := range files {
		b.WriteString("  ")
		b.WriteString(file)
		b.WriteByte('\n')
	}
	if len(symbols) > 0 {
		if symbolCap > 0 && len(symbols) > symbolCap {
			symbols = symbols[:symbolCap]
		}
		b.WriteString("\nExported symbols:\n")
		b.WriteString(FormatSymbolList(symbols, symbolCap))
	}
	return b.String()
}
