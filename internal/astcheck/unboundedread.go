package astcheck

import (
	"go/ast"
	"go/types"

	"golang.org/x/tools/go/analysis"
)

var unboundedreadAnalyzer = &analysis.Analyzer{
	Name: "unboundedread",
	Doc:  "flags io.ReadAll on untrusted sources (os.Stdin, http.Request.Body) without a size cap",
	Run:  runUnboundedread,
}

func init() {
	Register(RegistryEntry{
		Analyzer:  unboundedreadAnalyzer,
		Severity:  SevWarn,
		DefaultOn: true,
	})
}

// runUnboundedread flags io.ReadAll(x) where x is an unbounded untrusted
// source — os.Stdin or an *http.Request.Body — and the argument is not already
// wrapped in io.LimitReader. Such reads let a hostile or runaway producer
// exhaust memory.
func runUnboundedread(pass *analysis.Pass) (interface{}, error) {
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
			if pkgPath != "io" || funcName != "ReadAll" || len(call.Args) != 1 {
				return true
			}
			arg := call.Args[0]
			if isLimitReaderCall(arg, pass.TypesInfo) {
				return true // already bounded
			}
			if isStdin(arg, pass.TypesInfo) {
				pass.Reportf(call.Pos(),
					"io.ReadAll on os.Stdin without a size cap; wrap in io.LimitReader")
				return true
			}
			if isRequestBody(arg, pass.TypesInfo) {
				pass.Reportf(call.Pos(),
					"io.ReadAll on http.Request.Body without a size cap; use http.MaxBytesReader or io.LimitReader")
			}
			return true
		})
	}
	return nil, nil
}

// isLimitReaderCall reports whether expr is a call to io.LimitReader(...).
func isLimitReaderCall(expr ast.Expr, info *types.Info) bool {
	call, ok := expr.(*ast.CallExpr)
	if !ok {
		return false
	}
	pkgPath, funcName := extractQualifiedCall(call.Fun, info)
	return pkgPath == "io" && funcName == "LimitReader"
}

// isStdin reports whether expr refers to os.Stdin.
func isStdin(expr ast.Expr, info *types.Info) bool {
	sel, ok := expr.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "Stdin" {
		return false
	}
	if obj := info.Uses[sel.Sel]; obj != nil {
		if pkg := obj.Pkg(); pkg != nil {
			return pkg.Path() == "os"
		}
	}
	return false
}

// isRequestBody reports whether expr is the Body field of an *http.Request.
func isRequestBody(expr ast.Expr, info *types.Info) bool {
	sel, ok := expr.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "Body" {
		return false
	}
	// Resolve the type of the selector's receiver; it must be net/http.Request
	// (or a pointer to it).
	t := info.TypeOf(sel.X)
	if t == nil {
		return false
	}
	if ptr, ok := t.(*types.Pointer); ok {
		t = ptr.Elem()
	}
	named, ok := t.(*types.Named)
	if !ok {
		return false
	}
	obj := named.Obj()
	if obj == nil || obj.Pkg() == nil {
		return false
	}
	return obj.Pkg().Path() == "net/http" && obj.Name() == "Request"
}
