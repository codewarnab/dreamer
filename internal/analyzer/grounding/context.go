package grounding

import (
	"fmt"
	"strings"
)

// BuildContext returns a string suitable for the `{{codebase_context}}`
// template variable in phase-2 prompts (spec §7.4). It bundles a capped file
// list with an optional symbol index.
func BuildContext(files []string, symbols []Symbol, fileCap, symbolCap int) string {
	var builder strings.Builder
	if fileCap > 0 && len(files) > fileCap {
		files = files[:fileCap]
	}
	builder.WriteString(fmt.Sprintf("Files (%d shown):\n", len(files)))
	for _, file := range files {
		builder.WriteString("  ")
		builder.WriteString(file)
		builder.WriteByte('\n')
	}
	if len(symbols) > 0 {
		if symbolCap > 0 && len(symbols) > symbolCap {
			symbols = symbols[:symbolCap]
		}
		builder.WriteString("\nExported symbols:\n")
		builder.WriteString(FormatSymbolList(symbols, symbolCap))
	}
	return builder.String()
}
