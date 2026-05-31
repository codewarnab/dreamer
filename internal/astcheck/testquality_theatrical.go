package astcheck

import (
	"go/ast"
	"go/token"
	"go/types"
	"strings"

	"golang.org/x/tools/go/analysis"
)

var theatricaltestAnalyzer = &analysis.Analyzer{
	Name: "theatricaltest",
	Doc:  "detects theatrical tests that pass even when the code under test is broken",
	Run:  runTheatricaltest,
}

func init() {
	Register(RegistryEntry{
		Analyzer:  theatricaltestAnalyzer,
		Severity:  SevWarn,
		DefaultOn: true,
	})
}

// assertionCall tracks an assertion call with its context.
type assertionCall struct {
	onT        bool // called on *testing.T/B (t.Error, t.Fatal, etc.)
	structural bool // only checks structural properties (len, nil, field existence)
	isMock     bool // called on a mock/stub receiver (m.AssertExpectations, etc.)
}

// runTheatricaltest detects three patterns of theatrical tests:
//  1. No-assertion tests — test functions with zero assertion calls (excluding helpers/benchmarks)
//  2. Assertion-only-on-non-target — assertions only check mock call counts, not return values
//  3. Structural-only tests — only check len, nil, field existence without verifying correctness
func runTheatricaltest(pass *analysis.Pass) (interface{}, error) {
	for _, file := range pass.Files {
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			name := fn.Name.Name
			if !isTestFunc(name) {
				continue
			}
			if strings.HasPrefix(name, "Benchmark") || strings.HasPrefix(name, "Fuzz") {
				continue
			}

			assertions := collectAssertions(fn.Body)
			hasHelpers := hasNestedTestHelpers(fn.Body)

			checkNoAssertions(pass, name, fn.Pos(), assertions, hasHelpers)
			checkNonTargetOnly(pass, name, fn.Pos(), assertions)
			checkStructuralOnly(pass, name, fn.Pos(), assertions)
		}
	}
	return nil, nil
}

// isTestFunc reports whether a function is a test function (Test*, Benchmark*, Fuzz*).
func isTestFunc(name string) bool {
	return strings.HasPrefix(name, "Test") ||
		strings.HasPrefix(name, "Benchmark") ||
		strings.HasPrefix(name, "Fuzz")
}

// isTestingT reports whether expr is a *testing.T or *testing.B variable reference.
func isTestingT(expr ast.Expr, info *types.Info) bool {
	if info == nil {
		return false
	}
	if ident, ok := expr.(*ast.Ident); ok {
		if obj := info.ObjectOf(ident); obj != nil {
			if v, ok := obj.(*types.Var); ok {
				return isTestingType(v.Type())
			}
		}
	}
	return false
}

// isTestingType reports whether t is *testing.T or *testing.B.
func isTestingType(t types.Type) bool {
	ptr, ok := t.(*types.Pointer)
	if !ok {
		return false
	}
	named, ok := ptr.Elem().(*types.Named)
	if !ok {
		return false
	}
	obj := named.Obj()
	return obj.Pkg() != nil &&
		obj.Pkg().Path() == "testing" &&
		(obj.Name() == "T" || obj.Name() == "B")
}

// assertionMethods is the set of standard assertion methods on *testing.T/B.
var assertionMethods = map[string]bool{
	"Error": true, "Errorf": true,
	"Fail": true, "FailNow": true,
	"Fatal": true, "Fatalf": true, "Fatalln": true,
	"Skip": true, "Skipf": true, "SkipNow": true,
}

// isAssertionMethod reports whether name is a standard assertion method on *testing.T/B.
func isAssertionMethod(name string) bool {
	return assertionMethods[name]
}

// isAssertRequireCall reports whether a call is from the assert or require package
// (e.g. assert.Equal, require.NoError).
func isAssertRequireCall(call *ast.CallExpr) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	ident, ok := sel.X.(*ast.Ident)
	if !ok {
		return false
	}
	return ident.Name == "assert" || ident.Name == "require"
}

// isMockMethod reports whether a method name is typical of mock/stub objects.
func isMockMethod(name string) bool {
	switch name {
	case "AssertExpectations", "AssertCalled", "AssertNotCalled",
		"AssertNumberOfCalls", "Called", "Return", "On", "Once":
		return true
	default:
		return false
	}
}

// isMockAssertion reports whether a call is a mock/stub method call on a non-T receiver.
// Examples: m.AssertExpectations(t), mock.Called(args...), m.On("method").Return(val)
func isMockAssertion(call *ast.CallExpr) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	return isMockMethod(sel.Sel.Name)
}

// isAssertRequireStructural reports whether an assert/require call only checks
// structural properties (len, nil, field existence).
// Assert/require functions always have testing.TB as first arg — skip it.
func isAssertRequireStructural(call *ast.CallExpr) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	method := sel.Sel.Name
	switch method {
	case "NotNil", "Nil", "Empty", "NotEmpty", "True", "False", "Zero", "NotZero":
		return true
	case "Len":
		// assert.Len(t, obj, length) — structural (checks size, not value)
		return true
	}
	// For methods with expected value (Equal, etc.) — check if expected is structural.
	// Skip arg[0] (testing.TB), check arg[1] (expected value).
	if len(call.Args) >= 2 {
		return isStructuralRHS(call.Args[1])
	}
	return false
}

// isStructuralRHS reports whether an expression is a structural-only pattern:
// len(), cap(). nil is excluded because nil comparisons are common in real tests.
func isStructuralRHS(arg ast.Expr) bool {
	switch a := arg.(type) {
	case *ast.CallExpr:
		if sel, ok := a.Fun.(*ast.SelectorExpr); ok {
			if ident, ok := sel.X.(*ast.Ident); ok {
				if ident.Name == "reflect" && sel.Sel.Name == "DeepEqual" {
					return true
				}
			}
		}
		if id, ok := a.Fun.(*ast.Ident); ok {
			switch id.Name {
			case "len", "cap", "make", "new":
				return true
			}
		}
	}
	return false
}

// collectAssertions walks the function body and collects assertion calls.
// Excludes calls inside nested test helper closures.
func collectAssertions(body *ast.BlockStmt) []assertionCall {
	var result []assertionCall
	ast.Inspect(body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		switch {
		case isMockAssertion(call):
			result = append(result, assertionCall{isMock: true})
		case isTAssertion(call):
			result = append(result, assertionCall{onT: true})
		case isAssertRequireCall(call):
			result = append(result, assertionCall{structural: isAssertRequireStructural(call)})
		}
		return true
	})
	return result
}

// isTAssertion reports whether call is an assertion on *testing.T (e.g. t.Error, t.Fatal).
func isTAssertion(call *ast.CallExpr) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	return isAssertionMethod(sel.Sel.Name)
}

// hasNestedTestHelpers reports whether the function body contains nested
// function literals that look like test helpers (take *testing.T or *testing.B).
func hasNestedTestHelpers(body *ast.BlockStmt) bool {
	found := false
	ast.Inspect(body, func(n ast.Node) bool {
		if found {
			return false
		}
		fn, ok := n.(*ast.FuncLit)
		if !ok {
			return true
		}
		for _, param := range fn.Type.Params.List {
			if isTestingParam(param) {
				found = true
				return false
			}
		}
		return true
	})
	return found
}

// isTestingParam reports whether a function parameter is *testing.T or *testing.B.
func isTestingParam(param *ast.Field) bool {
	star, ok := param.Type.(*ast.StarExpr)
	if !ok {
		return false
	}
	sel, ok := star.X.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	if x, ok := sel.X.(*ast.Ident); ok && x.Name == "testing" {
		return sel.Sel.Name == "T" || sel.Sel.Name == "B"
	}
	return false
}

// checkNoAssertions reports test functions that have zero assertion calls.
func checkNoAssertions(pass *analysis.Pass, name string, pos token.Pos, assertions []assertionCall, hasHelpers bool) {
	if len(assertions) > 0 {
		return
	}
	if hasHelpers {
		pass.Reportf(pos, "test %s calls helpers but has no direct assertions; add assertions to verify helper results", name)
	} else {
		pass.Reportf(pos, "test %s has no assertions (t.Error/Fatal/require.*); test always passes", name)
	}
}

// checkNonTargetOnly reports test functions where all assertions are mock expectations
// (on non-T receivers), indicating the test doesn't verify the function under test.
func checkNonTargetOnly(pass *analysis.Pass, name string, pos token.Pos, assertions []assertionCall) {
	if len(assertions) == 0 {
		return
	}
	for _, a := range assertions {
		if !a.isMock {
			return // has at least one non-mock assertion
		}
	}
	pass.Reportf(pos, "test %s only asserts on non-T receivers (mock expectations); verify function return values", name)
}

// checkStructuralOnly reports test functions where all non-mock assertions are
// structural assert/require calls (len, nil, field existence) without value checks.
// Tests using t.Error/Fatal are excluded — their conditions can't be reliably analyzed.
func checkStructuralOnly(pass *analysis.Pass, name string, pos token.Pos, assertions []assertionCall) {
	if len(assertions) == 0 {
		return
	}
	hasStructuralAssert := false
	for _, a := range assertions {
		if a.isMock {
			continue
		}
		if a.onT {
			return // t.Error/Fatal — can't analyze conditions, assume non-structural
		}
		if !a.structural {
			return // has at least one non-structural assert/require call
		}
		hasStructuralAssert = true
	}
	if !hasStructuralAssert {
		return
	}
	pass.Reportf(pos, "test %s only checks structural properties (len/nil/fields); verify correctness with value assertions", name)
}
