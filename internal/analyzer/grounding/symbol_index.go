package grounding

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"dreamer/internal/analyzer/transport"
)

// Symbol is one exported declaration discovered by BuildSymbolIndex.
type Symbol struct {
	Path string
	Line int
	Kind string // "func", "type", "const", "var"
	Name string
}

var (
	goSymbolRe = regexp.MustCompile(`^\s*(?:func(?:\s*\([^)]*\))?|type|const|var)\s+\(?[*]?[A-Z][A-Za-z0-9_]*\b`)
	goDeclKind = regexp.MustCompile(`^\s*(func|type|const|var)\b`)
	goDeclName = regexp.MustCompile(`(?:func(?:\s*\([^)]*\))?|type|const|var)\s+\(?\*?([A-Z][A-Za-z0-9_]*)`)
)

// BuildSymbolIndex extracts exported Go declarations from files in the
// project. Non-Go projects return an empty index. Cap of zero or negative
// returns all symbols.
func BuildSymbolIndex(projectRoot string, files []string, cap int) []Symbol {
	if strings.TrimSpace(projectRoot) == "" {
		return nil
	}
	symbols := make([]Symbol, 0, len(files))
	for _, rel := range files {
		if !strings.HasSuffix(rel, ".go") {
			continue
		}
		if strings.HasSuffix(rel, "_test.go") {
			continue
		}
		path := filepath.Join(projectRoot, rel)
		fileSymbols := scanGoFile(path, rel)
		symbols = append(symbols, fileSymbols...)
		if cap > 0 && len(symbols) >= cap {
			return symbols[:cap]
		}
	}
	sort.Slice(symbols, func(i, j int) bool {
		if symbols[i].Path == symbols[j].Path {
			return symbols[i].Line < symbols[j].Line
		}
		return symbols[i].Path < symbols[j].Path
	})
	if cap > 0 && len(symbols) > cap {
		return symbols[:cap]
	}
	return symbols
}

func scanGoFile(absPath string, relPath string) []Symbol {
	f, err := os.Open(absPath)
	if err != nil {
		return nil
	}
	defer f.Close()
	scanner := transport.NewFileScanner(f)
	lineNum := 0
	var symbols []Symbol
	for scanner.Scan() {
		lineNum++
		// Avoid allocating a string for every line: match on the raw bytes
		// first and only call Text() when there is an actual symbol match.
		if !goSymbolRe.Match(scanner.Bytes()) {
			continue
		}
		line := scanner.Text()
		kindMatch := goDeclKind.FindStringSubmatch(line)
		nameMatch := goDeclName.FindStringSubmatch(line)
		if len(kindMatch) < 2 || len(nameMatch) < 2 {
			continue
		}
		symbols = append(symbols, Symbol{
			Path: relPath,
			Line: lineNum,
			Kind: kindMatch[1],
			Name: nameMatch[1],
		})
	}
	return symbols
}

// FormatSymbolList renders symbols as one-per-line strings suitable for prompt
// injection: "path:line  kind  Name".
func FormatSymbolList(symbols []Symbol, cap int) string {
	if cap > 0 && len(symbols) > cap {
		symbols = symbols[:cap]
	}
	var builder strings.Builder
	for i, symbol := range symbols {
		if i > 0 {
			builder.WriteByte('\n')
		}
		builder.WriteString(symbol.Path)
		builder.WriteByte(':')
		builder.WriteString(strconv.Itoa(symbol.Line))
		builder.WriteString("  ")
		builder.WriteString(symbol.Kind)
		builder.WriteByte(' ')
		builder.WriteString(symbol.Name)
	}
	return builder.String()
}
