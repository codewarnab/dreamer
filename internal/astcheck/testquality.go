package astcheck

import (
	"go/ast"
	"strings"

	"golang.org/x/tools/go/analysis"
)

// canonicalTestEnvVars is the full list of env vars that a setTestHome-style
// helper must clear/set to isolate tests from the developer's environment.
// Source: CLAUDE.md "Test quality rules", rule #4.
var canonicalTestEnvVars = []string{
	"HOME",
	"USERPROFILE",
	"XDG_CONFIG_HOME",
	"CLAUDE_CONFIG_DIR",
	"GEMINI_HOME",
	"OPENCODE_DB",
	"KIRO_CLI_DB",
	"CODEBUFF_CONFIG_DIR",
	"XDG_DATA_HOME",
}

var settesthomeAnalyzer = &analysis.Analyzer{
	Name: "settesthome",
	Doc:  "flags setTestHome helpers that don't clear all canonical env vars",
	Run:  runSettesthome,
}

func init() {
	Register(RegistryEntry{
		Analyzer:  settesthomeAnalyzer,
		Severity:  SevError,
		DefaultOn: true,
	})
}

// runSettesthome flags functions matching the setTestHome pattern that don't
// clear all canonical env vars (HOME, USERPROFILE, XDG_CONFIG_HOME, etc.).
func runSettesthome(pass *analysis.Pass) (interface{}, error) {
	for _, file := range pass.Files {
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			if !isTestHomeHelper(fn.Name.Name) {
				continue
			}
			// Collect which canonical env vars are touched by os.Unsetenv,
			// os.Setenv, or t.Setenv calls within this function body.
			touched := collectEnvVarTouched(fn.Body)
			var missing []string
			for _, env := range canonicalTestEnvVars {
				if !touched[env] {
					missing = append(missing, env)
				}
			}
			if len(missing) > 0 {
				pass.Reportf(fn.Pos(),
					"setTestHome helper %q missing env vars: %s",
					fn.Name.Name, strings.Join(missing, ", "))
			}
		}
	}
	return nil, nil
}

// isTestHomeHelper reports whether a function name matches the setTestHome pattern.
// Matches: setTestHome, setupTestHome, isolateTestEnv, SetTestEnv, etc.
// Excludes: runSettesthome, isTestHomeHelper, setupTest, etc.
func isTestHomeHelper(name string) bool {
	lower := strings.ToLower(name)
	// Function name must start with a setup verb.
	if strings.HasPrefix(lower, "set") ||
		strings.HasPrefix(lower, "setup") ||
		strings.HasPrefix(lower, "isolate") {
		// And must reference "test home" or "test env".
		return strings.Contains(lower, "testhome") ||
			strings.Contains(lower, "testenv")
	}
	return false
}

// collectEnvVarTouched walks the function body and returns the set of canonical
// env vars that are explicitly unset or set via os.Unsetenv, os.Setenv, or t.Setenv.
func collectEnvVarTouched(body *ast.BlockStmt) map[string]bool {
	touched := make(map[string]bool)
	ast.Inspect(body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		switch {
		case isPkgCall(call, "os", "Unsetenv"):
			if lit, ok := stringLitArg(call, 0); ok {
				touched[lit] = true
			}
		case isPkgCall(call, "os", "Setenv"):
			if lit, ok := stringLitArg(call, 0); ok {
				touched[lit] = true
			}
		default:
			// Check for t.Setenv (method call on a *testing.T receiver).
			if isMethodCall(call, "Setenv") {
				if lit, ok := stringLitArg(call, 0); ok {
					touched[lit] = true
				}
			}
		}
		return true
	})
	return touched
}

// isPkgCall reports whether call is pkg.Func (e.g. os.Unsetenv).
func isPkgCall(call *ast.CallExpr, pkg, fn string) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	ident, ok := sel.X.(*ast.Ident)
	if !ok {
		return false
	}
	return ident.Name == pkg && sel.Sel.Name == fn
}

// isMethodCall reports whether call is a method call (receiver.Method).
func isMethodCall(call *ast.CallExpr, method string) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	return sel.Sel.Name == method
}

// stringLitArg returns the string literal value of the i-th argument, if it is one.
func stringLitArg(call *ast.CallExpr, i int) (string, bool) {
	if i >= len(call.Args) {
		return "", false
	}
	lit, ok := call.Args[i].(*ast.BasicLit)
	if !ok {
		return "", false
	}
	// Strip surrounding quotes.
	s := lit.Value
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		return s[1 : len(s)-1], true
	}
	return "", false
}
