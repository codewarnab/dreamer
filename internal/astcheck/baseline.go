// Package astcheck — baseline.go manages position-stable finding keys
// and baseline read/write/subtract for incremental adoption.
//
// Baseline keys use {check, enclosing-symbol, normalized-message} — NOT
// line numbers — so edits elsewhere in a file don't invalidate the baseline.
package astcheck

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/token"
	"os"
	"strings"
)

// BaselineFile is the on-disk format of .quality-baseline.json.
type BaselineFile struct {
	Version  int                       `json:"version"`
	Findings map[string]BaselineEntry  `json:"findings"`
}

// BaselineEntry stores the components of a position-stable finding key.
type BaselineEntry struct {
	Check   string `json:"check"`
	Symbol  string `json:"symbol"`
	Message string `json:"message"`
}

// FindingKey computes a position-stable key for a finding.
// The key format is "check:symbol:message" where symbol is the enclosing
// function/method/type name (or "init" for package-level init, "" if unknown).
func FindingKey(check, symbol, message string) string {
	return check + ":" + symbol + ":" + message
}

// Key returns the position-stable key for this finding.
func (f Finding) Key() string {
	return FindingKey(f.Check, f.Symbol, f.Message)
}

// ReadBaseline reads a baseline file and returns the set of finding keys.
// Returns an empty set if the file doesn't exist.
func ReadBaseline(path string) (map[string]bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return make(map[string]bool), nil
		}
		return nil, fmt.Errorf("reading baseline %q: %w", path, err)
	}

	var bf BaselineFile
	if err := json.Unmarshal(data, &bf); err != nil {
		return nil, fmt.Errorf("parsing baseline %q: %w", path, err)
	}
	if bf.Version != 1 {
		return nil, fmt.Errorf("baseline %q: unsupported version %d", path, bf.Version)
	}

	keys := make(map[string]bool, len(bf.Findings))
	for key := range bf.Findings {
		keys[key] = true
	}
	return keys, nil
}

// WriteBaseline writes findings as a baseline file with position-stable keys.
func WriteBaseline(path string, findings []Finding) error {
	bf := BaselineFile{
		Version:  1,
		Findings: make(map[string]BaselineEntry, len(findings)),
	}
	for _, f := range findings {
		key := f.Key()
		bf.Findings[key] = BaselineEntry{
			Check:   f.Check,
			Symbol:  f.Symbol,
			Message: f.Message,
		}
	}

	data, err := json.MarshalIndent(bf, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling baseline: %w", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("writing baseline %q: %w", path, err)
	}
	return nil
}

// SubtractBaseline removes findings whose keys are in the baseline set.
func SubtractBaseline(findings []Finding, baseline map[string]bool) []Finding {
	if len(baseline) == 0 {
		return findings
	}
	var out []Finding
	for _, f := range findings {
		if !baseline[f.Key()] {
			out = append(out, f)
		}
	}
	return out
}

// EnclosingSymbol walks the AST to find the enclosing function, method,
// or type declaration name for the given position. Returns "" if at
// package level (no enclosing symbol).
func EnclosingSymbol(fset *token.FileSet, file *ast.File, pos token.Pos) string {
	line := fset.Position(pos).Line
	var best string
	var bestLine int

	ast.Inspect(file, func(n ast.Node) bool {
		if n == nil {
			return false
		}
		start := fset.Position(n.Pos())
		end := fset.Position(n.End())
		if line < start.Line || line > end.Line {
			return true
		}

		switch decl := n.(type) {
		case *ast.FuncDecl:
			// Prefer the innermost function.
			if start.Line >= bestLine {
				if decl.Recv != nil && len(decl.Recv.List) > 0 {
					recv := decl.Recv.List[0].Type
					if star, ok := recv.(*ast.StarExpr); ok {
						if id, ok := star.X.(*ast.Ident); ok {
							best = id.Name + "." + decl.Name.Name
						}
					}
					if best == "" {
						best = decl.Name.Name
					}
				} else {
					best = decl.Name.Name
				}
				bestLine = start.Line
			}
		case *ast.GenDecl:
			// For type/const/var declarations at package level.
			if decl.Tok.IsKeyword() && start.Line >= bestLine {
				for _, spec := range decl.Specs {
					if ts, ok := spec.(*ast.TypeSpec); ok {
						best = ts.Name.Name
						bestLine = start.Line
					}
				}
			}
		}
		return true
	})

	return best
}

// computeSuppressedLines scans file comments for //astcheck:ignore directives
// and returns the set of 1-based line numbers that should be suppressed.
// If the directive includes a check name (e.g. //astcheck:ignore[nodirectlog]),
// only that check is suppressed on that line.
func computeSuppressedLines(fset *token.FileSet, file *ast.File) map[int][]string {
	suppressed := make(map[int][]string)
	for _, cg := range file.Comments {
		for _, c := range cg.List {
			if !strings.Contains(c.Text, "astcheck:ignore") {
				continue
			}
			text := c.Text
			// Strip comment delimiters.
			text = strings.TrimPrefix(text, "//")
			text = strings.TrimPrefix(text, "/*")
			text = strings.TrimSuffix(text, "*/")
			text = strings.TrimSpace(text)

			// Parse: astcheck:ignore[ checkname][ // reason]
			directive := text
			if idx := strings.Index(directive, "//"); idx >= 0 {
				directive = strings.TrimSpace(directive[:idx])
			}

			// Extract check name if present.
			var checkName string
			if bracketStart := strings.Index(directive, "["); bracketStart >= 0 {
				if bracketEnd := strings.Index(directive[bracketStart:], "]"); bracketEnd >= 0 {
					checkName = directive[bracketStart+1 : bracketStart+bracketEnd]
					checkName = strings.TrimSpace(checkName)
				}
			}

			commentLine := fset.Position(c.Pos()).Line

			// The directive applies to the same line or the next line.
			// If the comment is on its own line (no code before it),
			// apply to the next line. If inline, apply to the same line.
			// We can't easily tell from AST alone, so apply to both
			// the comment line and the line after.
			lines := []int{commentLine, commentLine + 1}
			for _, l := range lines {
				suppressed[l] = append(suppressed[l], checkName)
			}
		}
	}
	return suppressed
}

// isSuppressed reports whether a finding at the given line should be
// suppressed based on the suppression map.
func isSuppressed(line int, check string, suppressed map[int][]string) bool {
	names, ok := suppressed[line]
	if !ok {
		return false
	}
	for _, name := range names {
		if name == "" || name == check {
			return true
		}
	}
	return false
}
