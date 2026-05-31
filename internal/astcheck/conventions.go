package astcheck

import (
	"go/ast"
	"go/types"
	"strings"

	"golang.org/x/tools/go/analysis"
)

var nodirectlogAnalyzer = &analysis.Analyzer{
	Name: "nodirectlog",
	Doc:  "flags direct use of stdlib log package; use logging.Logger instead",
	Run:  runNodirectlog,
}

func init() {
	Register(RegistryEntry{
		Analyzer:  nodirectlogAnalyzer,
		Severity:  SevError,
		DefaultOn: true,
	})
}

// runNodirectlog flags calls to stdlib log.Print/Printf/Fatal/etc in non-test code.
func runNodirectlog(pass *analysis.Pass) (interface{}, error) {
	if pass.Pkg.Path() == "command-line-arguments" {
		return nil, nil
	}

	for _, file := range pass.Files {
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			pkgPath, funcName := extractQualifiedCall(call.Fun, pass.TypesInfo)
			if pkgPath != "log" {
				return true
			}
			// Only flag direct log usage: Print, Printf, Println, Fatal, Fatalf,
			// Fatalln, Panic, Panicf, Panicln. Skip log.New, log.Writer, etc.
			switch funcName {
			case "Print", "Printf", "Println",
				"Fatal", "Fatalf", "Fatalln",
				"Panic", "Panicf", "Panicln":
				pass.Reportf(call.Pos(),
					"direct use of log.%s; use logging.Logger instead",
					funcName)
			}
			return true
		})
	}
	return nil, nil
}

// extractQualifiedCall returns the package path and function name for a
// qualified call expression (e.g. log.Printf → ("log", "Printf")).
// Returns ("", "") for unqualified calls.
func extractQualifiedCall(expr ast.Node, info *types.Info) (pkgPath, funcName string) {
	sel, ok := expr.(*ast.SelectorExpr)
	if !ok {
		return "", ""
	}
	// Check if the selector's X refers to a package import.
	if obj := info.Uses[sel.Sel]; obj != nil {
		if _, ok := obj.(*types.Func); ok {
			if pkg := obj.Pkg(); pkg != nil {
				return pkg.Path(), obj.Name()
			}
		}
	}
	return "", ""
}

// isTestFile reports whether a file is a test file based on its name.
func isTestFile(filename string) bool {
	return strings.HasSuffix(filename, "_test.go")
}
