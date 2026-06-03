package astcheck

import (
	"go/ast"
	"go/types"

	"golang.org/x/tools/go/analysis"
)

var errverbatimAnalyzer = &analysis.Analyzer{
	Name: "errverbatim",
	Doc:  "flags err.Error() used as HTTP response body; leaks filesystem paths and internal details to clients",
	Run:  runErrverbatim,
}

func init() {
	Register(RegistryEntry{
		Analyzer:  errverbatimAnalyzer,
		Severity:  SevWarn,
		DefaultOn: true,
	})
}

// verbatimCallees are functions that write to an io.Writer (typically the
// http.ResponseWriter). When err.Error() is passed as an argument to any of
// these, it ends up in the HTTP response body verbatim.
// fmt.Sprintf is intentionally excluded — it returns a string that may go to
// logging, metrics, or a safe message, not directly to the response.
var verbatimCallees = map[string]bool{
	"fmt.Fprintf":    true,
	"io.WriteString": true,
}

// runErrverbatim flags err.Error() used as an argument to write-like functions
// inside HTTP handler functions (those with an http.ResponseWriter parameter).
func runErrverbatim(pass *analysis.Pass) (any, error) {
	if pass.Pkg.Path() == "command-line-arguments" {
		return nil, nil
	}
	for _, file := range pass.Files {
		if isTestFile(pass.Fset.File(file.Pos()).Name()) {
			continue
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			if !hasResponseWriterParam(fn, pass.TypesInfo) {
				continue
			}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				if !isVerbatimCallee(call, pass.TypesInfo) {
					return true
				}
				for _, arg := range call.Args {
					if isErrorMethodCall(arg, pass.TypesInfo) {
						pass.Reportf(arg.Pos(),
							"err.Error() used as HTTP response body; leaks filesystem paths — use a safe error message")
						break
					}
				}
				return true
			})
		}
	}
	return nil, nil
}

// hasResponseWriterParam reports whether fn has an http.ResponseWriter parameter
// (in any position — receiver, first param, etc.).
func hasResponseWriterParam(fn *ast.FuncDecl, info *types.Info) bool {
	if fn.Recv != nil {
		for _, field := range fn.Recv.List {
			if isResponseWriterType(field.Type, info) {
				return true
			}
		}
	}
	if fn.Type.Params != nil {
		for _, field := range fn.Type.Params.List {
			if isResponseWriterType(field.Type, info) {
				return true
			}
		}
	}
	return false
}

// isResponseWriterType reports whether expr is http.ResponseWriter.
// Uses AST matching rather than type info since parameter type expressions
// are not always populated in info.Types.
func isResponseWriterType(expr ast.Expr, info *types.Info) bool {
	// Check via Uses lookup first (handles aliases, renamed imports, etc.).
	if sel, ok := expr.(*ast.SelectorExpr); ok {
		if obj := info.Uses[sel.Sel]; obj != nil {
			if tn, ok := obj.(*types.TypeName); ok {
				if iface, ok := tn.Type().Underlying().(*types.Interface); ok {
					for m := range iface.Methods() {
						if m.Name() == "Header" {
							return true
						}
					}
				}
			}
		}
	}
	return false
}

// isVerbatimCallee reports whether call is to a function that writes to an
// io.Writer, where passing err.Error() would leak it to the response.
func isVerbatimCallee(call *ast.CallExpr, info *types.Info) bool {
	pkgPath, funcName := extractQualifiedCall(call.Fun, info)
	key := pkgPath + "." + funcName
	if verbatimCallees[key] {
		return true
	}
	// Also check for method calls like w.Write([]byte(err.Error())) — but
	// that's harder to detect statically without tracking the writer variable.
	// We only flag the clear-cut cases above.
	return false
}
