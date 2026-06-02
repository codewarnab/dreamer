package webcheck

import (
	"bufio"
	"os"
	"regexp"
	"strings"
)

// hexColor matches CSS hex color values (#RGB, #RGBA, #RRGGBB, #RRGGBBAA).
// Excludes values preceded by letters or ( which indicate url() or identifiers.
var hexColor = regexp.MustCompile(`(?:^|[^a-zA-Z(])#(?:[0-9a-fA-F]{3}){1,2}(?:[0-9a-fA-F]{1,2})?\b`)

// cssVarRef matches var(--name) references in CSS.
var cssVarRef = regexp.MustCompile(`var\(\s*(--[a-zA-Z0-9-]+)`)

// cssVarDef matches --name: value definitions inside :root or other blocks.
var cssVarDef = regexp.MustCompile(`(--[a-zA-Z0-9-]+)\s*:`)

// CheckCSSTokens scans CSS files for:
//  1. Hardcoded hex color values that should be CSS custom properties.
//  2. var(--name) references where --name is not defined in the same file's :root.
func CheckCSSTokens(files []string) []Finding {
	var findings []Finding
	for _, path := range files {
		if !strings.HasSuffix(path, ".css") {
			continue
		}
		findings = append(findings, checkHardcodedColors(path)...)
		findings = append(findings, checkUndefinedTokens(path)...)
	}
	return findings
}

// checkHardcodedColors flags hex color values in CSS that should use custom
// properties from the design token system. Excludes colors inside comments,
// url(), var(), and common shorthand #fff/#000.
func checkHardcodedColors(path string) []Finding {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()

	var findings []Finding
	inComment := false
	inRoot := false
	braceDepth := 0
	scanner := bufio.NewScanner(f)
	lineNum := 0
	for scanner.Scan() {
		lineNum++
		line := scanner.Text()

		// Track :root block — token definitions are not violations.
		if strings.Contains(line, ":root") && strings.Contains(line, "{") {
			inRoot = true
		}
		if inRoot {
			braceDepth += strings.Count(line, "{") - strings.Count(line, "}")
			if braceDepth <= 0 {
				inRoot = false
				braceDepth = 0
			}
			continue // skip colors inside :root (they're the token definitions)
		}

		// Track multi-line comments.
		if inComment {
			if idx := strings.Index(line, "*/"); idx >= 0 {
				inComment = false
				line = line[idx+2:]
			} else {
				continue
			}
		}
		// Strip single-line comments.
		if idx := strings.Index(line, "/*"); idx >= 0 {
			if endIdx := strings.Index(line[idx:], "*/"); endIdx >= 0 {
				line = line[:idx] + line[idx+endIdx+2:]
			} else {
				line = line[:idx]
				inComment = true
			}
		}
		// Strip // comments (non-standard but used in some preprocessors).
		if idx := strings.Index(line, "//"); idx >= 0 {
			line = line[:idx]
		}

		// Strip var() references so hex colors inside them are not flagged,
		// but keep the rest of the line for standalone color detection.
		line = cssVarRef.ReplaceAllString(line, "")

		matches := hexColor.FindAllStringIndex(line, -1)
		for _, loc := range matches {
			color := strings.TrimSpace(line[loc[0]:loc[1]])
			// Exclude common defaults.
			lower := strings.ToLower(color)
			if lower == "#fff" || lower == "#000" || lower == "#ffffff" || lower == "#000000" {
				continue
			}
			findings = append(findings, Finding{
				File:     path,
				Line:     lineNum,
				Col:      loc[0] + 1,
				Check:    "css-hardcoded-color",
				Severity: SevWarn,
				Message:  "hardcoded color " + color + " should use a CSS custom property from the design token system",
			})
		}
	}
	return findings
}

// checkUndefinedTokens flags var(--name) references where --name is not defined
// in any :root block in the same CSS file.
func checkUndefinedTokens(path string) []Finding {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()

	var findings []Finding
	definedVars := make(map[string]bool)
	varRefs := make([]varRef, 0)

	scanner := bufio.NewScanner(f)
	lineNum := 0
	inRoot := false
	braceDepth := 0

	for scanner.Scan() {
		lineNum++
		line := scanner.Text()

		// Track :root block entry.
		if strings.Contains(line, ":root") && strings.Contains(line, "{") {
			inRoot = true
		}
		if inRoot {
			braceDepth += strings.Count(line, "{") - strings.Count(line, "}")
			if braceDepth <= 0 {
				inRoot = false
				braceDepth = 0
			}
			// Collect variable definitions inside :root.
			for _, m := range cssVarDef.FindAllStringSubmatch(line, -1) {
				definedVars[m[1]] = true
			}
		}

		// Collect all var(--name) references.
		for _, m := range cssVarRef.FindAllStringSubmatchIndex(line, -1) {
			if len(m) >= 4 {
				varName := line[m[2]:m[3]]
				varRefs = append(varRefs, varRef{name: varName, line: lineNum, col: m[0] + 1})
			}
		}
	}

	// Check references against definitions.
	for _, ref := range varRefs {
		if !definedVars[ref.name] {
			findings = append(findings, Finding{
				File:     path,
				Line:     ref.line,
				Col:      ref.col,
				Check:    "css-undefined-token",
				Severity: SevWarn,
				Message:  "CSS custom property " + ref.name + " is referenced but not defined in :root",
			})
		}
	}
	return findings
}

type varRef struct {
	name string
	line int
	col  int
}
