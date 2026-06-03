package astcheck

import (
	"go/ast"
	"go/types"

	"golang.org/x/tools/go/analysis"
)

var implicitstatusAnalyzer = &analysis.Analyzer{
	Name: "implicitstatus",
	Doc:  "flags HTTP handlers that call w.Write without a preceding w.WriteHeader; relies on implicit 200 status which is fragile",
	Run:  runImplicitstatus,
}

func init() {
	Register(RegistryEntry{
		Analyzer:  implicitstatusAnalyzer,
		Severity:  SevInfo,
		DefaultOn: true,
	})
}

// runImplicitstatus flags HTTP handler functions that call w.Write() or
// io.WriteString(w, ...) without a preceding w.WriteHeader() call. The implicit
// 200 default works but is fragile — any reordering or early return can produce
// wrong status codes.
func runImplicitstatus(pass *analysis.Pass) (any, error) {
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
			writerName := responseWriterParam(fn, pass.TypesInfo)
			if writerName == "" {
				continue
			}
			checkHandlerBody(fn.Body, writerName, pass)
		}
	}
	return nil, nil
}

// responseWriterParam returns the name of the http.ResponseWriter parameter in
// fn, or "" if fn doesn't have one.
func responseWriterParam(fn *ast.FuncDecl, info *types.Info) string {
	if fn.Recv != nil {
		for _, field := range fn.Recv.List {
			if isResponseWriterType(field.Type, info) {
				if len(field.Names) > 0 {
					return field.Names[0].Name
				}
				return "this" // unnamed receiver
			}
		}
	}
	if fn.Type.Params != nil {
		for _, field := range fn.Type.Params.List {
			if isResponseWriterType(field.Type, info) {
				if len(field.Names) > 0 {
					return field.Names[0].Name
				}
				return "w" // unnamed param
			}
		}
	}
	return ""
}

// checkHandlerBody walks the function body and flags Write calls on the writer
// variable that are not preceded by a WriteHeader call.
func checkHandlerBody(body *ast.BlockStmt, writerName string, pass *analysis.Pass) {
	hasWriteHeader := false
	ast.Inspect(body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		// Check if this is w.WriteHeader(...)
		if sel.Sel.Name == "WriteHeader" && isWriterRef(sel.X, writerName) {
			hasWriteHeader = true
			return true
		}
		// Check if this is w.Write(...)
		if sel.Sel.Name == "Write" && isWriterRef(sel.X, writerName) && !hasWriteHeader {
			pass.Reportf(call.Pos(),
				"w.Write called without preceding w.WriteHeader; add explicit status code")
		}
		// Check if this is io.WriteString(w, ...)
		if isIOWriteString(call, writerName, pass.TypesInfo) && !hasWriteHeader {
			pass.Reportf(call.Pos(),
				"io.WriteString(w, ...) called without preceding w.WriteHeader; add explicit status code")
		}
		return true
	})
}

// isWriterRef reports whether expr refers to the named writer variable.
func isWriterRef(expr ast.Expr, writerName string) bool {
	ident, ok := expr.(*ast.Ident)
	return ok && ident.Name == writerName
}

// isIOWriteString reports whether call is io.WriteString(w, ...) where w
// matches the named writer variable.
func isIOWriteString(call *ast.CallExpr, writerName string, info *types.Info) bool {
	pkgPath, funcName := extractQualifiedCall(call.Fun, info)
	if pkgPath != "io" || funcName != "WriteString" || len(call.Args) < 2 {
		return false
	}
	return isWriterRef(call.Args[0], writerName)
}
