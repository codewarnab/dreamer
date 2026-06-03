package astcheck

import (
	"go/ast"
	"go/types"

	"golang.org/x/tools/go/analysis"
)

var symlinkresolveAnalyzer = &analysis.Analyzer{
	Name: "symlinkresolve",
	Doc:  "flags filepath.Clean used in containment checks without fsutil.ResolveSymlinks; symlink traversal bypasses the check",
	Run:  runSymlinkresolve,
}

func init() {
	Register(RegistryEntry{
		Analyzer:  symlinkresolveAnalyzer,
		Severity:  SevWarn,
		DefaultOn: true,
	})
}

// runSymlinkresolve flags strings.HasPrefix/Suffix calls where the first
// argument is (or was assigned from) a filepath.Clean call whose input was not
// first resolved via fsutil.ResolveSymlinks, filepath.EvalSymlinks, or
// os.Readlink.
func runSymlinkresolve(pass *analysis.Pass) (any, error) {
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
			checkSymlinkFunction(fn, pass)
		}
	}
	return nil, nil
}

// checkSymlinkFunction checks a single function for filepath.Clean in
// containment checks without prior symlink resolution. Maps are scoped per
// function to prevent cross-function variable name collisions.
func checkSymlinkFunction(fn *ast.FuncDecl, pass *analysis.Pass) {
	cleanVars := make(map[string]*ast.CallExpr) // var → filepath.Clean call
	resolvedVars := make(map[string]bool)       // var assigned from symlink resolution

	// Collect assignments from this function's body only.
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		assign, ok := n.(*ast.AssignStmt)
		if !ok || len(assign.Rhs) != 1 {
			return true
		}
		call, ok := assign.Rhs[0].(*ast.CallExpr)
		if !ok {
			return true
		}
		for _, lhs := range assign.Lhs {
			ident, ok := lhs.(*ast.Ident)
			if !ok {
				continue
			}
			if isFilepathClean(pass.TypesInfo, call) {
				cleanVars[ident.Name] = call
			}
			if isSymlinkResolutionCall(call) {
				resolvedVars[ident.Name] = true
			}
		}
		return true
	})

	// Find strings.HasPrefix/Suffix calls and check their first argument.
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if !isHasPrefixOrSuffix(call, pass.TypesInfo) {
			return true
		}
		arg := call.Args[0]
		var cleanCall *ast.CallExpr

		// Pattern 1: inline — HasPrefix(filepath.Clean(path), root)
		if inner, ok := arg.(*ast.CallExpr); ok && isFilepathClean(pass.TypesInfo, inner) {
			cleanCall = inner
		}

		// Pattern 2: split — cleaned := filepath.Clean(path); HasPrefix(cleaned, root)
		if ident, ok := arg.(*ast.Ident); ok {
			if cc, exists := cleanVars[ident.Name]; exists {
				cleanCall = cc
			}
		}

		if cleanCall == nil {
			return true
		}

		// Check if the filepath.Clean input was already symlink-resolved.
		if len(cleanCall.Args) >= 1 && isSymlinkResolved(cleanCall.Args[0], resolvedVars) {
			return true
		}

		pass.Reportf(call.Pos(),
			"filepath.Clean used in containment check without symlink resolution; call fsutil.ResolveSymlinks or filepath.EvalSymlinks first")
		return true
	})
}

// isFilepathClean reports whether call is to path/filepath.Clean.
func isFilepathClean(info *types.Info, call *ast.CallExpr) bool {
	pkgPath, funcName := extractQualifiedCall(call.Fun, info)
	return pkgPath == "path/filepath" && funcName == "Clean"
}

// isSymlinkResolved reports whether expr is either a direct call to a symlink
// resolution function, or a variable that was assigned from one.
func isSymlinkResolved(expr ast.Expr, resolvedVars map[string]bool) bool {
	if call, ok := expr.(*ast.CallExpr); ok && isSymlinkResolutionCall(call) {
		return true
	}
	if ident, ok := expr.(*ast.Ident); ok && resolvedVars[ident.Name] {
		return true
	}
	return false
}

// isHasPrefixOrSuffix reports whether call is to strings.HasPrefix or
// strings.HasSuffix with at least 2 arguments.
func isHasPrefixOrSuffix(call *ast.CallExpr, info *types.Info) bool {
	pkgPath, funcName := extractQualifiedCall(call.Fun, info)
	if pkgPath != "strings" {
		return false
	}
	if funcName != "HasPrefix" && funcName != "HasSuffix" {
		return false
	}
	return len(call.Args) >= 2
}

// isSymlinkResolutionCall reports whether call is to fsutil.ResolveSymlinks,
// filepath.EvalSymlinks, or os.Readlink. Uses AST matching so it works with
// both test stubs and production packages.
func isSymlinkResolutionCall(call *ast.CallExpr) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	ident, ok := sel.X.(*ast.Ident)
	if !ok {
		return false
	}
	switch {
	case ident.Name == "fsutil" && sel.Sel.Name == "ResolveSymlinks":
		return true
	case ident.Name == "filepath" && sel.Sel.Name == "EvalSymlinks":
		return true
	case ident.Name == "os" && sel.Sel.Name == "Readlink":
		return true
	}
	return false
}
