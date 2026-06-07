package astcheck

import (
	"go/ast"
	"go/types"

	"golang.org/x/tools/go/analysis"
)

var nilcleanupAnalyzer = &analysis.Analyzer{
	Name: "nilcleanup",
	Doc:  "flags `return nil, err` from functions returning (func(), error); a nil cleanup func crashes callers that defer it unconditionally",
	Run:  runNilcleanup,
}

func init() {
	Register(RegistryEntry{
		Analyzer:  nilcleanupAnalyzer,
		Severity:  SevWarn,
		DefaultOn: true,
	})
}

// runNilcleanup inspects every function (declared or literal) whose results
// include both a niladic func() and an error, and flags returns where the
// cleanup slot is the literal nil while the error slot is non-nil (M2, PR #84).
func runNilcleanup(pass *analysis.Pass) (any, error) {
	if pass.Pkg.Path() == "command-line-arguments" {
		return nil, nil
	}
	for _, file := range pass.Files {
		ast.Inspect(file, func(n ast.Node) bool {
			var body *ast.BlockStmt
			var ftype *ast.FuncType
			switch fn := n.(type) {
			case *ast.FuncDecl:
				body, ftype = fn.Body, fn.Type
			case *ast.FuncLit:
				body, ftype = fn.Body, fn.Type
			default:
				return true
			}
			if body == nil {
				return true
			}
			checkCleanupReturns(pass, ftype, body)
			return true
		})
	}
	return nil, nil
}

// checkCleanupReturns flags offending return statements in one function body.
func checkCleanupReturns(pass *analysis.Pass, ftype *ast.FuncType, body *ast.BlockStmt) {
	funcIdx, errIdx, funcName := cleanupSlots(pass.TypesInfo, ftype)
	if funcIdx < 0 || errIdx < 0 {
		return
	}
	assigned := funcName != "" && identAssigned(body, funcName)
	for _, ret := range ownReturns(body) {
		if len(ret.Results) == 0 {
			// Naked return: best-effort — flag only when the named func()
			// result is never assigned anywhere in the body.
			if funcName != "" && !assigned {
				pass.Reportf(ret.Pos(),
					"nil cleanup func returned with error; return func(){} so callers can defer unconditionally")
			}
			continue
		}
		if len(ret.Results) <= funcIdx || len(ret.Results) <= errIdx {
			continue // single-expression `return f()` form; can't inspect slots
		}
		if isNilIdent(ret.Results[funcIdx]) && !isNilIdent(ret.Results[errIdx]) {
			pass.Reportf(ret.Pos(),
				"nil cleanup func returned with error; return func(){} so callers can defer unconditionally")
		}
	}
}

// cleanupSlots returns the result indexes of the first niladic func() result
// and the first error result (-1 when absent), plus the func() result's name
// when the results are named.
func cleanupSlots(info *types.Info, ftype *ast.FuncType) (funcIdx, errIdx int, funcName string) {
	funcIdx, errIdx = -1, -1
	if ftype.Results == nil {
		return funcIdx, errIdx, ""
	}
	idx := 0
	for _, field := range ftype.Results.List {
		names := max(len(field.Names), 1)
		t := info.TypeOf(field.Type)
		for i := range names {
			switch {
			case funcIdx < 0 && isNiladicFunc(t):
				funcIdx = idx
				if i < len(field.Names) {
					funcName = field.Names[i].Name
				}
			case errIdx < 0 && isErrorType(t):
				errIdx = idx
			}
			idx++
		}
	}
	return funcIdx, errIdx, funcName
}

// isNiladicFunc reports whether t is a func() with no params and no results.
func isNiladicFunc(t types.Type) bool {
	sig, ok := t.(*types.Signature)
	return ok && sig.Params().Len() == 0 && sig.Results().Len() == 0
}

// isNilIdent reports whether e is the literal identifier nil.
func isNilIdent(e ast.Expr) bool {
	ident, ok := e.(*ast.Ident)
	return ok && ident.Name == "nil"
}

// ownReturns collects the return statements that belong to body itself,
// not to any nested function literal.
func ownReturns(body *ast.BlockStmt) []*ast.ReturnStmt {
	var returns []*ast.ReturnStmt
	ast.Inspect(body, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.FuncLit:
			return false // nested function: its returns are its own
		case *ast.ReturnStmt:
			returns = append(returns, node)
		}
		return true
	})
	return returns
}

// identAssigned reports whether name appears as an assignment target anywhere
// in body (outside nested function literals).
func identAssigned(body *ast.BlockStmt, name string) bool {
	found := false
	ast.Inspect(body, func(n ast.Node) bool {
		if found {
			return false
		}
		if _, ok := n.(*ast.FuncLit); ok {
			return false
		}
		assign, ok := n.(*ast.AssignStmt)
		if !ok {
			return true
		}
		for _, lhs := range assign.Lhs {
			if ident, ok := lhs.(*ast.Ident); ok && ident.Name == name {
				found = true
			}
		}
		return true
	})
	return found
}
