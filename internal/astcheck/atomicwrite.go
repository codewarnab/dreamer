package astcheck

import (
	"go/ast"
	"strings"

	"golang.org/x/tools/go/analysis"
)

var atomicwriteAnalyzer = &analysis.Analyzer{
	Name: "atomicwrite",
	Doc:  "flags os.WriteFile in non-test code; use fsutil.WriteFileAtomic for durable, crash-safe writes",
	Run:  runAtomicwrite,
}

func init() {
	Register(RegistryEntry{
		Analyzer:  atomicwriteAnalyzer,
		Severity:  SevWarn,
		DefaultOn: true,
	})
}

// atomicwriteExemptPkgs are packages that legitimately cannot route through
// fsutil.WriteFileAtomic: fsutil itself (it IS the implementation) and logging,
// which fsutil imports — routing logging through fsutil would create an import
// cycle.
var atomicwriteExemptPkgs = []string{
	"dreamer/internal/fsutil",
	"dreamer/internal/logging",
}

// runAtomicwrite flags calls to os.WriteFile in non-test code, steering writers
// toward fsutil.WriteFileAtomic. Suppress an intentional scratch write with
// //astcheck:ignore[atomicwrite].
func runAtomicwrite(pass *analysis.Pass) (interface{}, error) {
	if pass.Pkg.Path() == "command-line-arguments" {
		return nil, nil
	}
	for _, exempt := range atomicwriteExemptPkgs {
		// Match exact path and the analysistest package alias (e.g. the test
		// fixture uses a bare package name).
		if pass.Pkg.Path() == exempt || strings.HasSuffix(pass.Pkg.Path(), exempt) {
			return nil, nil
		}
	}

	for _, file := range pass.Files {
		if isTestFile(pass.Fset.File(file.Pos()).Name()) {
			continue
		}
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			pkgPath, funcName := extractQualifiedCall(call.Fun, pass.TypesInfo)
			if pkgPath == "os" && funcName == "WriteFile" {
				pass.Reportf(call.Pos(),
					"os.WriteFile is not atomic; use fsutil.WriteFileAtomic for durable, crash-safe writes")
			}
			return true
		})
	}
	return nil, nil
}
