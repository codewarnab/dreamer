package astcheck

import (
	"go/ast"
	"go/token"
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
// net/http.Error is the single most common leak vector: http.Error(w,
// err.Error(), code) sends the raw error straight to the client.
var verbatimCallees = map[string]bool{
	"fmt.Fprintf":    true,
	"io.WriteString": true,
	"net/http.Error": true,
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
				if pos, ok := errLeakArgPos(call, pass.TypesInfo); ok {
					pass.Reportf(pos,
						"err.Error() used as HTTP response body; leaks filesystem paths — use a safe error message")
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

// errLeakArgPos reports whether call leaks err.Error() into an HTTP response,
// returning the position of the offending err.Error() expression. It covers
// two shapes:
//
//	verbatim callee:  fmt.Fprintf(w, ..., err.Error()) / http.Error(w, err.Error(), code)
//	response write:   w.Write([]byte(err.Error()))
func errLeakArgPos(call *ast.CallExpr, info *types.Info) (token.Pos, bool) {
	if isVerbatimCallee(call, info) {
		for _, arg := range call.Args {
			if isErrorMethodCall(arg, info) {
				return arg.Pos(), true
			}
		}
	}
	return responseWriteErrPos(call, info)
}

// isVerbatimCallee reports whether call is to a function in verbatimCallees
// (fmt.Fprintf, io.WriteString, http.Error) that writes its args to the client.
func isVerbatimCallee(call *ast.CallExpr, info *types.Info) bool {
	pkgPath, funcName := extractQualifiedCall(call.Fun, info)
	return verbatimCallees[pkgPath+"."+funcName]
}

// responseWriteErrPos detects w.Write([]byte(err.Error())) where w is an
// http.ResponseWriter, returning the position of the err.Error() call. This is
// the other common leak vector: writing the raw error bytes to the response.
func responseWriteErrPos(call *ast.CallExpr, info *types.Info) (token.Pos, bool) {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "Write" || len(call.Args) != 1 {
		return 0, false
	}
	if !isResponseWriterValue(sel.X, info) {
		return 0, false
	}
	inner, ok := byteSliceConversionArg(call.Args[0])
	if !ok || !isErrorMethodCall(inner, info) {
		return 0, false
	}
	return inner.Pos(), true
}

// byteSliceConversionArg returns the inner expression of a []byte(x) conversion
// and true, or (nil, false) when arg is not such a conversion.
func byteSliceConversionArg(arg ast.Expr) (ast.Expr, bool) {
	conv, ok := arg.(*ast.CallExpr)
	if !ok || len(conv.Args) != 1 {
		return nil, false
	}
	arrType, ok := conv.Fun.(*ast.ArrayType)
	if !ok || arrType.Len != nil {
		return nil, false
	}
	if elt, ok := arrType.Elt.(*ast.Ident); !ok || elt.Name != "byte" {
		return nil, false
	}
	return conv.Args[0], true
}

// isResponseWriterValue reports whether the value expr has an http.ResponseWriter
// method set, identified (like isResponseWriterType) by carrying both Header and
// Write methods. This excludes plain io.Writer sinks such as bytes.Buffer used
// for logging, keeping the check focused on the response.
func isResponseWriterValue(expr ast.Expr, info *types.Info) bool {
	t := info.TypeOf(expr)
	if t == nil {
		return false
	}
	ms := types.NewMethodSet(t)
	hasHeader, hasWrite := false, false
	for i := 0; i < ms.Len(); i++ {
		switch ms.At(i).Obj().Name() {
		case "Header":
			hasHeader = true
		case "Write":
			hasWrite = true
		}
	}
	return hasHeader && hasWrite
}
