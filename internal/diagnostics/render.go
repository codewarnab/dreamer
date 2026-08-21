package diagnostics

import (
	"fmt"
	"strings"
)

// RenderText renders the report as plain text suitable for pasting into a
// GitHub issue or attaching as report.txt inside a bundle. No ANSI colors.
func RenderText(r Report) string {
	var b strings.Builder

	b.WriteString("dreamer diagnostics report\n")
	b.WriteString(fmt.Sprintf("generated: %s\n\n", r.GeneratedAt.Format(timeLayout)))

	b.WriteString("system\n")
	b.WriteString(fmt.Sprintf("  version:    %s\n", r.System.Version))
	b.WriteString(fmt.Sprintf("  commit:     %s\n", r.System.Commit))
	b.WriteString(fmt.Sprintf("  built:      %s\n", r.System.BuildDate))
	b.WriteString(fmt.Sprintf("  go:         %s\n", r.System.GoVersion))
	b.WriteString(fmt.Sprintf("  os/arch:    %s/%s\n\n", r.System.GOOS, r.System.GOARCH))

	b.WriteString("checks\n")
	for _, c := range r.Checks {
		b.WriteString(fmt.Sprintf("  [%-7s] %s", c.Status, c.Name))
		if c.Detail != "" {
			b.WriteString(": " + c.Detail)
		}
		b.WriteString("\n")
	}

	return b.String()
}

// timeLayout is the timestamp format used in rendered reports.
const timeLayout = "2006-01-02 15:04:05 MST"

// TruncateBody clamps text to maxRunes, appending a visible truncation marker
// so readers know content was cut. Used to respect GitHub's issue body limit.
func TruncateBody(text string, maxRunes int) string {
	runes := []rune(text)
	if len(runes) <= maxRunes {
		return text
	}
	const marker = "\n\n---\n[...truncated by dreamer bug — full details are in the attached diagnostic bundle...]"
	keep := maxRunes - len([]rune(marker))
	if keep < 0 {
		keep = 0
	}
	return string(runes[:keep]) + marker
}
