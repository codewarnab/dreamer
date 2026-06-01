package astcheck

import (
	"go/ast"
	"go/token"
	"go/types"
	"strings"

	"golang.org/x/tools/go/analysis"
)

var lockorderAnalyzer = &analysis.Analyzer{
	Name: "lockorder",
	Doc:  "flags read operations (readAll, ReadFile, os.Open, etc.) that precede fsutil.AcquireLock in the same function body — a TOCTOU race where another process can modify the file between read and lock",
	Run:  runLockorder,
}

func init() {
	Register(RegistryEntry{
		Analyzer:  lockorderAnalyzer,
		Severity:  SevError,
		DefaultOn: true,
	})
}

// runLockorder inspects each function body for read-before-lock ordering.
// When a function acquires a file lock via fsutil.AcquireLock, any file read
// (readAll, ReadFile, os.Open, etc.) that precedes the lock acquisition in
// statement order is a TOCTOU race — another process can modify the file
// between the read and the lock.
func runLockorder(pass *analysis.Pass) (interface{}, error) {
	if pass.Pkg.Path() == "command-line-arguments" {
		return nil, nil
	}
	for _, file := range pass.Files {
		if isTestFile(pass.Fset.File(file.Pos()).Name()) {
			continue
		}
		ast.Inspect(file, func(n ast.Node) bool {
			body := funcBodyFromNode(n)
			if body == nil {
				return true
			}
			analyzeBodyForLockOrder(body, pass)
			return true
		})
	}
	return nil, nil
}

// funcBodyFromNode extracts the function body from a FuncDecl or FuncLit.
func funcBodyFromNode(n ast.Node) *ast.BlockStmt {
	switch fn := n.(type) {
	case *ast.FuncDecl:
		return fn.Body
	case *ast.FuncLit:
		return fn.Body
	}
	return nil
}

// analyzeBodyForLockOrder walks statements in order, tracking whether a read
// has occurred before an AcquireLock call. Only flags if the function actually
// acquires a lock — no lock means no lock-related TOCTOU risk.
func analyzeBodyForLockOrder(body *ast.BlockStmt, pass *analysis.Pass) {
	if body == nil {
		return
	}
	// First pass: check if this function acquires a lock at all.
	hasLock := false
	for _, stmt := range body.List {
		if isAcquireLockStmt(stmt) {
			hasLock = true
			break
		}
	}
	if !hasLock {
		return
	}
	// Second pass: flag reads that precede the lock.
	seenLock := false
	for _, stmt := range body.List {
		if isAcquireLockStmt(stmt) {
			seenLock = true
			continue
		}
		if !seenLock {
			if call := findReadCallInStmt(stmt, pass); call != nil {
				pass.Reportf(call.Pos(),
					"%s before fsutil.AcquireLock; move lock acquisition before the read to prevent TOCTOU races",
					callNameInfo(call, pass.TypesInfo))
			}
		}
	}
}

// isAcquireLockStmt reports whether stmt contains an fsutil.AcquireLock call
// at the top level (direct call or RHS of a short-var-decl/assignment).
func isAcquireLockStmt(stmt ast.Stmt) bool {
	switch s := stmt.(type) {
	case *ast.ExprStmt:
		return isFsutilAcquireLock(s.X)
	case *ast.DeferStmt:
		// defer release() — not a lock acquisition itself
		return false
	case *ast.AssignStmt:
		for _, rhs := range s.Rhs {
			if isFsutilAcquireLock(rhs) {
				return true
			}
		}
	case *ast.DeclStmt:
		// var release, err = fsutil.AcquireLock(...)
		gd, ok := s.Decl.(*ast.GenDecl)
		if !ok || gd.Tok != token.VAR {
			return false
		}
		for _, spec := range gd.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			for _, val := range vs.Values {
				if isFsutilAcquireLock(val) {
					return true
				}
			}
		}
	}
	return false
}

// isFsutilAcquireLock reports whether expr is a call to fsutil.AcquireLock.
func isFsutilAcquireLock(expr ast.Expr) bool {
	call, ok := expr.(*ast.CallExpr)
	if !ok {
		return false
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	if sel.Sel.Name != "AcquireLock" {
		return false
	}
	ident, ok := sel.X.(*ast.Ident)
	return ok && ident.Name == "fsutil"
}

// findReadCallInStmt searches stmt for a file-read call at the top level
// (direct call or RHS of assignment). Returns the call if found.
func findReadCallInStmt(stmt ast.Stmt, pass *analysis.Pass) *ast.CallExpr {
	switch s := stmt.(type) {
	case *ast.ExprStmt:
		if call, ok := s.X.(*ast.CallExpr); ok && isReadCall(call, pass) {
			return call
		}
	case *ast.AssignStmt:
		for _, rhs := range s.Rhs {
			if call, ok := rhs.(*ast.CallExpr); ok && isReadCall(call, pass) {
				return call
			}
		}
	case *ast.DeclStmt:
		gd, ok := s.Decl.(*ast.GenDecl)
		if !ok || gd.Tok != token.VAR {
			return nil
		}
		for _, spec := range gd.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			for _, val := range vs.Values {
				if call, ok := val.(*ast.CallExpr); ok && isReadCall(call, pass) {
					return call
				}
			}
		}
	case *ast.IfStmt:
		if call := findReadCallInStmt(s.Init, pass); call != nil {
			return call
		}
		for _, b := range s.Body.List {
			if call := findReadCallInStmt(b, pass); call != nil {
				return call
			}
		}
		if s.Else != nil {
			if call := findReadCallInStmtAsBlock(s.Else, pass); call != nil {
				return call
			}
		}
	case *ast.SwitchStmt:
		if call := findReadCallInStmt(s.Init, pass); call != nil {
			return call
		}
		for _, b := range s.Body.List {
			if cc, ok := b.(*ast.CaseClause); ok {
				for _, c := range cc.Body {
					if call := findReadCallInStmt(c, pass); call != nil {
						return call
					}
				}
			}
		}
	case *ast.BlockStmt:
		for _, b := range s.List {
			if call := findReadCallInStmt(b, pass); call != nil {
				return call
			}
		}
	case *ast.RangeStmt:
		if call := findReadCallInStmt(s.Body, pass); call != nil {
			return call
		}
	case *ast.ForStmt:
		if call := findReadCallInStmt(s.Body, pass); call != nil {
			return call
		}
	}
	return nil
}

// findReadCallInStmtAsBlock wraps a non-block else branch into a block for
// uniform handling by findReadCallInStmt.
func findReadCallInStmtAsBlock(stmt ast.Stmt, pass *analysis.Pass) *ast.CallExpr {
	if block, ok := stmt.(*ast.BlockStmt); ok {
		return findReadCallInStmt(block, pass)
	}
	return findReadCallInStmt(stmt, pass)
}

// isReadCall reports whether a call expression is a file-read operation.
func isReadCall(call *ast.CallExpr, pass *analysis.Pass) bool {
	// Unqualified call: readAll() — common pattern for receiver-style
	// helper methods invoked within the same package.
	if ident, ok := call.Fun.(*ast.Ident); ok {
		if ident.Name == "readAll" || ident.Name == "ReadAll" || ident.Name == "ReadFile" {
			return true
		}
	}
	// Method call: x.readAll(), x.ReadAll(), x.ReadFile(...)
	if sel, ok := call.Fun.(*ast.SelectorExpr); ok {
		name := sel.Sel.Name
		if name == "readAll" || name == "ReadAll" || name == "ReadFile" {
			return true
		}
	}
	// Package-qualified call: os.ReadFile, os.Open, ioutil.ReadFile,
	// io.ReadAll.
	pkgPath, funcName := extractQualifiedCall(call.Fun, pass.TypesInfo)
	if pkgPath == "" {
		return false
	}
	switch funcName {
	case "ReadFile":
		return true
	case "ReadAll":
		return true
	case "Open":
		// os.Open or os.OpenFile — both can read.
		return strings.HasPrefix(pkgPath, "os") || pkgPath == "io"
	}
	return false
}

// callNameInfo returns a human-readable name for a call expression.
func callNameInfo(call *ast.CallExpr, _ *types.Info) string {
	if sel, ok := call.Fun.(*ast.SelectorExpr); ok {
		if x, ok := sel.X.(*ast.Ident); ok {
			return x.Name + "." + sel.Sel.Name
		}
		return sel.Sel.Name
	}
	if ident, ok := call.Fun.(*ast.Ident); ok {
		return ident.Name
	}
	return "read call"
}
