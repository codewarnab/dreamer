package astcheck

import (
	"go/ast"
	"go/types"
	"strings"

	"golang.org/x/tools/go/analysis"
)

var flagpropagationAnalyzer = &analysis.Analyzer{
	Name: "flagpropagation",
	Doc:  "flags bool parameters in cmd/ functions that are never read; an accepted-but-ignored flag silently disables the behavior it promises",
	Run:  runFlagpropagation,
}

func init() {
	Register(RegistryEntry{
		Analyzer: flagpropagationAnalyzer,
		Severity: SevInfo,
		// Default OFF for one release; promote after a baseline run confirms
		// acceptable noise (see docs/plans/QUALITY_CHECKER_IMPROVEMENTS.md §7).
		DefaultOn: false,
	})
}

// runFlagpropagation flags bool parameters of functions in cmd packages that
// are never read in the function body (L5, PR #84: dryRun accepted but never
// forwarded, so --json always printed false). Restricted to cmd/ to bound
// noise — that's where CLI flags enter and must propagate (TESTING.md rule #2).
func runFlagpropagation(pass *analysis.Pass) (any, error) {
	if !isCmdPackage(pass.Pkg.Path()) {
		return nil, nil
	}
	used := make(map[types.Object]bool, len(pass.TypesInfo.Uses))
	for _, obj := range pass.TypesInfo.Uses {
		used[obj] = true
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
			checkBoolParams(pass, fd, used)
		}
	}
	return nil, nil
}

// checkBoolParams reports each named bool parameter of fd that is never read.
func checkBoolParams(pass *analysis.Pass, fd *ast.FuncDecl, used map[types.Object]bool) {
	for _, field := range fd.Type.Params.List {
		for _, name := range field.Names {
			if name.Name == "_" {
				continue
			}
			obj := pass.TypesInfo.Defs[name]
			if obj == nil || !isBoolType(obj.Type()) || used[obj] {
				continue
			}
			pass.Reportf(name.Pos(),
				"bool parameter %s is never read in %s; the flag it carries is silently dropped",
				name.Name, fd.Name.Name)
		}
	}
}

// isBoolType reports whether t is the predeclared bool (not named bool-like types).
func isBoolType(t types.Type) bool {
	basic, ok := t.(*types.Basic)
	return ok && basic.Kind() == types.Bool
}

// isCmdPackage reports whether pkgPath is a cmd package ("dreamer/cmd" in
// production; "flagprop/cmd" in the analysistest fixture).
func isCmdPackage(pkgPath string) bool {
	return pkgPath == "dreamer/cmd" || strings.HasSuffix(pkgPath, "/cmd")
}
