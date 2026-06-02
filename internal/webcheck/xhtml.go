package webcheck

import (
	"bufio"
	"os"
	"regexp"
	"strings"
)

// xhtmlPattern matches Alpine.js x-html directives in HTML templates.
// x-html injects unescaped HTML — a maintenance XSS risk even when the current
// code manually escapes content, because any future edit that skips escaping
// silently introduces a vulnerability.
var xhtmlPattern = regexp.MustCompile(`x-html\s*=`)

// CheckXHTML scans HTML template files for x-html usage. Each match produces a
// warn-level finding.
func CheckXHTML(files []string) []Finding {
	var findings []Finding
	for _, path := range files {
		if !strings.HasSuffix(path, ".html") {
			continue
		}
		findings = append(findings, checkXHTMLFile(path)...)
	}
	return findings
}

// checkXHTMLFile scans a single HTML file for x-html directives. Extracted so
// the deferred Close runs per-file, not at the end of the outer loop.
func checkXHTMLFile(path string) []Finding {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()

	var findings []Finding
	scanner := bufio.NewScanner(f)
	lineNum := 0
	for scanner.Scan() {
		lineNum++
		line := scanner.Text()
		// Skip Go template comments.
		if strings.Contains(line, "{{/*") {
			continue
		}
		// Strip HTML comments (full-line or inline) before checking.
		line = stripHTMLComments(line)
		if strings.TrimSpace(line) == "" {
			continue
		}
		loc := xhtmlPattern.FindStringIndex(line)
		if loc != nil {
			findings = append(findings, Finding{
				File:     path,
				Line:     lineNum,
				Col:      loc[0] + 1,
				Check:    "x-html",
				Severity: SevWarn,
				Message:  "x-html injects unescaped HTML; use x-text + CSS-based rendering to prevent XSS",
			})
		}
	}
	return findings
}

// stripHTMLComments removes <!-- ... --> comment segments from a line.
func stripHTMLComments(line string) string {
	for {
		start := strings.Index(line, "<!--")
		if start < 0 {
			return line
		}
		end := strings.Index(line[start:], "-->")
		if end < 0 {
			// Multi-line comment — strip to end of line.
			return line[:start]
		}
		line = line[:start] + line[start+end+3:]
	}
}
