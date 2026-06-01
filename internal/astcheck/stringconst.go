package astcheck

import (
	"go/ast"
	"go/token"
	"strings"
	"unicode"

	"golang.org/x/tools/go/analysis"
)

var stringconstAnalyzer = &analysis.Analyzer{
	Name: "stringconst",
	Doc:  "flags string literals repeated >= 3 times across >= 2 files that should be extracted to constants",
	Run:  runStringconst,
}

func init() {
	Register(RegistryEntry{
		Analyzer:  stringconstAnalyzer,
		Severity:  SevWarn,
		DefaultOn: true,
	})
}

// minOccurrences is the minimum number of times a string literal must appear
// before it is flagged.
const minOccurrences = 3

// minDistinctFiles is the minimum number of distinct files a string literal
// must appear in before it is flagged.
const minDistinctFiles = 2

// stringOccurrence tracks a single string literal occurrence.
type stringOccurrence struct {
	pos    token.Pos
	file   string
	symbol string
}

// runStringconst collects string literals across all non-test files in a
// package and flags those that appear >= minOccurrences times across >=
// minDistinctFiles distinct files.
func runStringconst(pass *analysis.Pass) (any, error) {
	if pass.Pkg.Path() == "command-line-arguments" {
		return nil, nil
	}

	literals := make(map[string][]stringOccurrence)
	for _, file := range pass.Files {
		filename := pass.Fset.File(file.Pos()).Name()
		if isTestFile(filename) {
			continue
		}
		collectStringLiterals(file, pass, filename, literals)
	}

	for lit, occs := range literals {
		if len(occs) < minOccurrences {
			continue
		}
		files := make(map[string]bool)
		for _, oc := range occs {
			files[oc.file] = true
		}
		if len(files) < minDistinctFiles {
			continue
		}
		for _, oc := range occs {
			pass.Reportf(oc.pos,
				"string %q repeated %d times across %d files; extract to a constant",
				lit, len(occs), len(files))
		}
	}

	return nil, nil
}

// collectStringLiterals walks a file and records string literals that are
// candidates for constant extraction. Skips const declarations, struct tags,
// import paths, format strings, and very short strings.
func collectStringLiterals(file *ast.File, pass *analysis.Pass, filename string, literals map[string][]stringOccurrence) {
	constLiterals := collectConstLiterals(file)

	ast.Inspect(file, func(n ast.Node) bool {
		bl, ok := n.(*ast.BasicLit)
		if !ok || bl.Kind != token.STRING {
			return true
		}
		if constLiterals[bl.Pos()] {
			return true
		}
		if isStructTag(file, bl) {
			return true
		}
		inner := stripQuotes(bl.Value)
		if inner == "" {
			return true
		}
		if shouldSkipString(inner) {
			return true
		}

		symbol := EnclosingSymbol(pass.Fset, file, bl.Pos())
		literals[inner] = append(literals[inner], stringOccurrence{
			pos:    bl.Pos(),
			file:   filename,
			symbol: symbol,
		})
		return true
	})
}

// collectConstLiterals returns a set of positions of string literals that are
// already declared as constants in the file.
func collectConstLiterals(file *ast.File) map[token.Pos]bool {
	positions := make(map[token.Pos]bool)
	for _, decl := range file.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok || gd.Tok != token.CONST {
			continue
		}
		for _, spec := range gd.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			for _, val := range vs.Values {
				if bl, ok := val.(*ast.BasicLit); ok && bl.Kind == token.STRING {
					positions[bl.Pos()] = true
				}
			}
		}
	}
	return positions
}

// shouldSkipString reports whether s should be excluded from duplicate
// detection (format strings, import paths, file paths, natural language).
func shouldSkipString(s string) bool {
	if len(s) <= 2 {
		return true
	}
	if strings.TrimSpace(s) == "" {
		return true
	}
	if strings.Contains(s, "%") {
		return true
	}
	if isImportPath(s) {
		return true
	}
	if isFilePath(s) {
		return true
	}
	if isNaturalLanguage(s) {
		return true
	}
	return false
}

// stripQuotes removes surrounding double quotes from a Go string literal.
func stripQuotes(s string) string {
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		return s[1 : len(s)-1]
	}
	return s
}

// isStructTag reports whether bl is a struct tag (parent is an ast.Field).
func isStructTag(file *ast.File, bl *ast.BasicLit) bool {
	var found bool
	ast.Inspect(file, func(n ast.Node) bool {
		if found {
			return false
		}
		field, ok := n.(*ast.Field)
		if !ok {
			return true
		}
		if field.Tag != nil && field.Tag.Pos() == bl.Pos() {
			found = true
			return false
		}
		return true
	})
	return found
}

// isImportPath reports whether s looks like a Go import path.
func isImportPath(s string) bool {
	return strings.Contains(s, "/") && !strings.Contains(s, " ")
}

// isFilePath reports whether s looks like a file or directory path.
func isFilePath(s string) bool {
	if strings.Contains(s, "/") || strings.Contains(s, `\`) {
		return !strings.Contains(s, " ")
	}
	return false
}

// isNaturalLanguage reports whether s looks like a natural-language sentence
// (contains spaces and starts with a lowercase letter).
func isNaturalLanguage(s string) bool {
	if !strings.Contains(s, " ") {
		return false
	}
	for _, r := range s {
		return unicode.IsLower(r)
	}
	return false
}
