package astcheck

import (
	"go/ast"
	"go/types"

	"golang.org/x/tools/go/analysis"
)

var stringerrAnalyzer = &analysis.Analyzer{
	Name: "stringerr",
	Doc:  "flags strings.Contains(err.Error(), literal) for error type detection; use errors.Is/errors.As instead",
	Run:  runStringerr,
}

func init() {
	Register(RegistryEntry{
		Analyzer:  stringerrAnalyzer,
		Severity:  SevWarn,
		DefaultOn: true,
	})
}

// runStringerr flags calls to strings.Contains where the first argument is
// err.Error() (a method call on an error-typed value) and the second argument
// is a string literal. This pattern is fragile — it breaks if the error text
// changes and doesn't support wrapped errors. Use errors.Is/errors.As instead.
func runStringerr(pass *analysis.Pass) (any, error) {
	if pass.Pkg.Path() == "command-line-arguments" {
		return nil, nil
	}
	for _, file := range pass.Files {
		if isTestFile(pass.Fset.File(file.Pos()).Name()) {
			continue
		}
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			// Must be strings.Contains with exactly 2 args.
			pkgPath, funcName := extractQualifiedCall(call.Fun, pass.TypesInfo)
			if pkgPath != "strings" || funcName != "Contains" || len(call.Args) != 2 {
				return true
			}
			// First arg must be .Error() on an error-typed value.
			if !isErrorMethodCall(call.Args[0], pass.TypesInfo) {
				return true
			}
			// Second arg must be a string literal.
			if _, ok := call.Args[1].(*ast.BasicLit); !ok {
				return true
			}
			pass.Reportf(call.Pos(),
				"error type detected via strings.Contains(err.Error(), ...); use errors.Is or errors.As instead")
			return true
		})
	}
	return nil, nil
}

// isErrorMethodCall reports whether expr is a .Error() method call on a value
// whose type implements the error interface. expr is typically a *ast.CallExpr
// wrapping a *ast.SelectorExpr (i.e. err.Error()).
func isErrorMethodCall(expr ast.Expr, info *types.Info) bool {
	call, ok := expr.(*ast.CallExpr)
	if !ok {
		return false
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "Error" {
		return false
	}
	// Check that the receiver's type implements the error interface.
	recvType := info.TypeOf(sel.X)
	if recvType == nil {
		return false
	}
	return isErrorType(recvType)
}
