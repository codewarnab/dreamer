package astcheck

import (
	"go/ast"
	"strings"

	"golang.org/x/tools/go/analysis"
)

var goroutinerecoverAnalyzer = &analysis.Analyzer{
	Name: "goroutinerecover",
	Doc:  "flags daemon/web goroutines that run the pipeline without a recover() guard; a panic there crashes the whole daemon",
	Run:  runGoroutinerecover,
}

func init() {
	Register(RegistryEntry{
		Analyzer:  goroutinerecoverAnalyzer,
		Severity:  SevInfo,
		DefaultOn: false, // higher FP risk; enable per-package once validated
	})
}

// goroutineScopePrefixes are the dreamer packages where an unrecovered panic
// in a spawned goroutine can take down the long-lived daemon process.
var goroutineScopePrefixes = []string{
	"dreamer/internal/web",
	"dreamer/cmd",
	"dreamer/internal/backgroundjobs",
}

// goroutineInScope reports whether the package is one whose goroutines this
// analyzer should inspect. Non-dreamer paths (test fixtures, external code) are
// always in scope so analysistest fixtures still exercise the logic.
func goroutineInScope(path string) bool {
	if path == "command-line-arguments" {
		return false
	}
	if !strings.HasPrefix(path, "dreamer/") {
		return true
	}
	for _, p := range goroutineScopePrefixes {
		if path == p || strings.HasPrefix(path, p+"/") {
			return true
		}
	}
	return false
}

// runGoroutinerecover flags `go func(){…}()` whose body runs the pipeline but
// whose first statement is not a deferred recover().
func runGoroutinerecover(pass *analysis.Pass) (interface{}, error) {
	if !goroutineInScope(pass.Pkg.Path()) {
		return nil, nil
	}
	for _, file := range pass.Files {
		if isTestFile(pass.Fset.File(file.Pos()).Name()) {
			continue
		}
		ast.Inspect(file, func(n ast.Node) bool {
			goStmt, ok := n.(*ast.GoStmt)
			if !ok {
				return true
			}
			lit, ok := goStmt.Call.Fun.(*ast.FuncLit)
			if !ok || lit.Body == nil {
				return true
			}
			if !bodyRunsPipeline(lit.Body, pass) {
				return true
			}
			if firstStmtIsRecover(lit.Body) {
				return true
			}
			pass.Reportf(goStmt.Pos(),
				"goroutine runs the pipeline without a recover() guard; a panic here crashes the daemon")
			return true
		})
	}
	return nil, nil
}

// bodyRunsPipeline reports whether a goroutine body calls into the pipeline:
// either a call to something in a package named "pipeline" or any method named
// Run (the executor/runner convention).
func bodyRunsPipeline(body *ast.BlockStmt, pass *analysis.Pass) bool {
	found := false
	ast.Inspect(body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		pkgPath, funcName := extractQualifiedCall(call.Fun, pass.TypesInfo)
		if strings.Contains(pkgPath, "pipeline") {
			found = true
			return false
		}
		// Method/func call named Run on any receiver.
		if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "Run" {
			found = true
			return false
		}
		if funcName == "Run" {
			found = true
			return false
		}
		return true
	})
	return found
}

// firstStmtIsRecover reports whether the first statement of a block is a
// deferred function literal that calls recover().
func firstStmtIsRecover(body *ast.BlockStmt) bool {
	if len(body.List) == 0 {
		return false
	}
	deferStmt, ok := body.List[0].(*ast.DeferStmt)
	if !ok {
		return false
	}
	lit, ok := deferStmt.Call.Fun.(*ast.FuncLit)
	if !ok || lit.Body == nil {
		return false
	}
	found := false
	ast.Inspect(lit.Body, func(n ast.Node) bool {
		if ident, ok := n.(*ast.Ident); ok && ident.Name == "recover" {
			found = true
			return false
		}
		return true
	})
	return found
}
