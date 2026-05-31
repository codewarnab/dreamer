package astcheck

import (
	"go/ast"
	"reflect"
	"strconv"
	"strings"

	"golang.org/x/tools/go/analysis"
)

var overlayboolAnalyzer = &analysis.Analyzer{
	Name: "overlaybool",
	Doc:  `flags overlay-merged struct fields typed "bool" instead of "*bool"; a zero-value false silently overrides a true default`,
	Run:  runOverlaybool,
}

func init() {
	Register(RegistryEntry{
		Analyzer:  overlayboolAnalyzer,
		Severity:  SevWarn,
		DefaultOn: true,
	})
}

// overlayboolInScope limits the analyzer to internal/config (where overlay
// merging lives). Non-dreamer paths (test fixtures) are always in scope so the
// analysistest fixture exercises the logic.
func overlayboolInScope(path string) bool {
	if path == "command-line-arguments" {
		return false
	}
	if !strings.HasPrefix(path, "dreamer/") {
		return true
	}
	return path == "dreamer/internal/config" || strings.HasPrefix(path, "dreamer/internal/config/")
}

// runOverlaybool flags struct fields tagged `overlay:"merge"` whose declared
// type is the value type bool. Overlay merge cannot distinguish an explicit
// false from an unset field unless the field is a *bool pointer.
func runOverlaybool(pass *analysis.Pass) (interface{}, error) {
	if !overlayboolInScope(pass.Pkg.Path()) {
		return nil, nil
	}
	for _, file := range pass.Files {
		ast.Inspect(file, func(n ast.Node) bool {
			st, ok := n.(*ast.StructType)
			if !ok || st.Fields == nil {
				return true
			}
			for _, field := range st.Fields.List {
				if !fieldMergesOverlay(field) {
					continue
				}
				if ident, ok := field.Type.(*ast.Ident); ok && ident.Name == "bool" {
					pass.Reportf(field.Pos(),
						`overlay-merged field is bool; use *bool so an explicit false is distinguishable from unset`)
				}
			}
			return true
		})
	}
	return nil, nil
}

// fieldMergesOverlay reports whether a struct field carries the
// `overlay:"merge"` tag.
func fieldMergesOverlay(field *ast.Field) bool {
	if field.Tag == nil {
		return false
	}
	raw, err := strconv.Unquote(field.Tag.Value)
	if err != nil {
		return false
	}
	return reflect.StructTag(raw).Get("overlay") == "merge"
}
