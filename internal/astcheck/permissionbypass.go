package astcheck

import (
	"go/ast"
	"go/token"
	"strings"

	"golang.org/x/tools/go/analysis"
)

var permissionbypassAnalyzer = &analysis.Analyzer{
	Name: "permissionbypass",
	Doc:  "flags SandboxProjectWrite or SandboxWritableDirs set without a sandbox.Available() guard",
	Run:  runPermissionbypass,
}

func init() {
	Register(RegistryEntry{
		Analyzer:  permissionbypassAnalyzer,
		Severity:  SevError,
		DefaultOn: true,
	})
}

// permissionbypassInScope limits the analyzer to backgroundjobs and cliharness.
// Non-dreamer paths (test fixtures) are always in scope.
func permissionbypassInScope(path string) bool {
	if path == "command-line-arguments" {
		return false
	}
	if !strings.HasPrefix(path, "dreamer/") {
		return true
	}
	return path == "dreamer/internal/backgroundjobs" ||
		strings.HasPrefix(path, "dreamer/internal/backgroundjobs/") ||
		path == "dreamer/internal/analyzer/providers/cliharness" ||
		strings.HasPrefix(path, "dreamer/internal/analyzer/providers/cliharness/")
}

// runPermissionbypass flags functions that set sandbox write posture without
// a structural sandbox.Available() guard. Two guard patterns are recognized:
//
//  1. Positive if-guard: `if sandbox.Available() { cfg.Write = true }`
//  2. Early-return guard: `if !sandbox.Available() { return } cfg.Write = true`
//
// A negated check without return (!sandbox.Available() + log + continue) does
// NOT guard — the write posture still executes when the sandbox is absent.
func runPermissionbypass(pass *analysis.Pass) (any, error) {
	if !permissionbypassInScope(pass.Pkg.Path()) {
		return nil, nil
	}
	for _, file := range pass.Files {
		if isTestFile(pass.Fset.File(file.Pos()).Name()) {
			continue
		}
		for _, decl := range file.Decls {
			fd, ok := decl.(*ast.FuncDecl)
			if !ok || fd.Body == nil {
				continue
			}
			checkFuncBody(fd.Body, pass)
		}
	}
	return nil, nil
}

// checkFuncBody walks function statements tracking a "guarded" flag.
func checkFuncBody(body *ast.BlockStmt, pass *analysis.Pass) {
	if body == nil {
		return
	}
	walkStatements(body.List, pass, false)
}

// walkStatements walks statements sequentially, updating guard state when
// encountering an early-return pattern.
func walkStatements(stmts []ast.Stmt, pass *analysis.Pass, guarded bool) {
	for _, stmt := range stmts {
		guarded = walkStmt(stmt, pass, guarded)
	}
}

// walkStmt walks a single statement, returning the updated guard state.
func walkStmt(stmt ast.Stmt, pass *analysis.Pass, guarded bool) bool {
	if stmt == nil {
		return guarded
	}
	switch s := stmt.(type) {
	case *ast.IfStmt:
		if isPositiveAvailableCheck(s.Cond) {
			walkStatements(s.Body.List, pass, true)
			walkElse(s.Else, pass, guarded)
		} else if isNegatedAvailableCheck(s.Cond) && containsReturn(s.Body) {
			walkStatements(s.Body.List, pass, guarded)
			return true
		} else {
			walkStatements(s.Body.List, pass, guarded)
			walkElse(s.Else, pass, guarded)
		}

	case *ast.AssignStmt:
		if !guarded {
			for _, lhs := range s.Lhs {
				if isSandboxWritePostureLHS(lhs) {
					pass.Reportf(lhs.Pos(), "%s set without sandbox.Available() check",
						selectorName(lhs))
				}
			}
		}

	case *ast.BlockStmt:
		walkStatements(s.List, pass, guarded)
	case *ast.ForStmt:
		if s.Body != nil {
			walkStatements(s.Body.List, pass, guarded)
		}
	case *ast.RangeStmt:
		if s.Body != nil {
			walkStatements(s.Body.List, pass, guarded)
		}
	case *ast.SwitchStmt:
		if s.Body != nil {
			walkStatements(s.Body.List, pass, guarded)
		}
	case *ast.CaseClause:
		walkStatements(s.Body, pass, guarded)
	}
	return guarded
}

// walkElse walks an else branch (block or chained if).
func walkElse(elseNode ast.Stmt, pass *analysis.Pass, guarded bool) {
	if elseNode == nil {
		return
	}
	switch e := elseNode.(type) {
	case *ast.BlockStmt:
		walkStatements(e.List, pass, guarded)
	case *ast.IfStmt:
		walkStmt(e, pass, guarded)
	}
}

// isPositiveAvailableCheck reports whether expr is sandbox.Available().
func isPositiveAvailableCheck(expr ast.Expr) bool {
	call, ok := expr.(*ast.CallExpr)
	return ok && isSandboxAvailableCall(call)
}

// isNegatedAvailableCheck reports whether expr is !sandbox.Available().
func isNegatedAvailableCheck(expr ast.Expr) bool {
	unary, ok := expr.(*ast.UnaryExpr)
	if !ok || unary.Op != token.NOT {
		return false
	}
	call, ok := unary.X.(*ast.CallExpr)
	return ok && isSandboxAvailableCall(call)
}

// containsReturn reports whether body contains a top-level return statement.
func containsReturn(body *ast.BlockStmt) bool {
	if body == nil {
		return false
	}
	for _, stmt := range body.List {
		if _, ok := stmt.(*ast.ReturnStmt); ok {
			return true
		}
	}
	return false
}

// isSandboxAvailableCall reports whether call is sandbox.Available().
func isSandboxAvailableCall(call *ast.CallExpr) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	if sel.Sel.Name != "Available" {
		return false
	}
	ident, ok := sel.X.(*ast.Ident)
	if !ok {
		return false
	}
	return ident.Name == "sandbox"
}

// isSandboxWritePostureLHS reports whether lhs is a SandboxProjectWrite or
// SandboxWritableDirs selector.
func isSandboxWritePostureLHS(lhs ast.Expr) bool {
	sel, ok := lhs.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	name := sel.Sel.Name
	return name == "SandboxProjectWrite" || name == "SandboxWritableDirs"
}

// selectorName returns the selector name from an expression like cfg.Foo → "Foo".
func selectorName(expr ast.Expr) string {
	if sel, ok := expr.(*ast.SelectorExpr); ok {
		return sel.Sel.Name
	}
	if ident, ok := expr.(*ast.Ident); ok {
		return ident.Name
	}
	return "?"
}
