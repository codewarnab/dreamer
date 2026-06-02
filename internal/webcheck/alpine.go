package webcheck

import (
	"bufio"
	"os"
	"regexp"
	"strings"
)

// alpineInitPattern matches document.addEventListener("alpine:init", ...) usage.
// With <script defer>, Alpine v3 fires alpine:init before subsequent deferred
// scripts load, so any listener registered this way never executes. Stores must
// be registered directly at the top level instead.
var alpineInitPattern = regexp.MustCompile(`document\.addEventListener\s*\(\s*["']alpine:init["']`)

// CheckAlpineInit scans JS files for document.addEventListener("alpine:init")
// usage that silently fails when scripts are loaded with defer.
func CheckAlpineInit(files []string) []Finding {
	var findings []Finding
	for _, path := range files {
		if !strings.HasSuffix(path, ".js") {
			continue
		}
		findings = append(findings, checkAlpineInitFile(path)...)
	}
	return findings
}

func checkAlpineInitFile(path string) []Finding {
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
		if strings.HasPrefix(strings.TrimSpace(line), "//") || strings.HasPrefix(strings.TrimSpace(line), "*") {
			continue
		}
		loc := alpineInitPattern.FindStringIndex(line)
		if loc != nil {
			findings = append(findings, Finding{
				File:     path,
				Line:     lineNum,
				Col:      loc[0] + 1,
				Check:    "alpine-init-listener",
				Severity: SevWarn,
				Message:  "document.addEventListener(\"alpine:init\") fires before deferred scripts load; register Alpine.store() directly instead",
			})
		}
	}
	return findings
}
