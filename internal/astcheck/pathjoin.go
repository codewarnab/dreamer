package astcheck

import (
	"go/ast"
	"go/types"
	"regexp"

	"golang.org/x/tools/go/analysis"
)

var pathjoinAnalyzer = &analysis.Analyzer{
	Name: "pathjoin",
	Doc:  "flags path.Join/Dir/Base/Clean/Ext used on filesystem paths; path is for URLs and slash paths, filepath handles OS separators",
	Run:  runPathjoin,
}

func init() {
	Register(RegistryEntry{
		Analyzer:  pathjoinAnalyzer,
		Severity:  SevWarn,
		DefaultOn: true,
	})
}

// pathPkgFuncs is the set of "path" package functions that are wrong to call
// on a filesystem path (they always use forward slashes; on Windows they
// mangle backslash paths — L4, PR #84).
var pathPkgFuncs = map[string]bool{
	"Join":  true,
	"Dir":   true,
	"Base":  true,
	"Clean": true,
	"Ext":   true,
}

// fsEvidencePkgs are package paths whose return values are filesystem
// evidence: a value produced by these packages is a real OS path.
var fsEvidencePkgs = map[string]bool{
	"os":                      true,
	"path/filepath":           true,
	"dreamer/internal/fsutil": true,
}

// urlEvidencePkgs are package paths whose values indicate URL/slash-path use,
// where path.Join is the correct tool.
var urlEvidencePkgs = map[string]bool{
	"net/url":  true,
	"net/http": true,
}

// pathEvidence classifies what an argument's origin suggests about its nature.
type pathEvidence int

const (
	evidenceNone pathEvidence = iota
	evidenceFilesystem
	evidenceURL
)

// runPathjoin flags calls to path.* helpers whose arguments show filesystem
// evidence and no URL evidence.
func runPathjoin(pass *analysis.Pass) (any, error) {
	if pass.Pkg.Path() == "command-line-arguments" {
		return nil, nil
	}
	for _, file := range pass.Files {
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			fnName, matched := pathPkgCallName(call, pass.TypesInfo)
			if !matched {
				return true
			}
			hasFS, hasURL := false, false
			for _, arg := range call.Args {
				switch argEvidence(arg, pass.TypesInfo) {
				case evidenceFilesystem:
					hasFS = true
				case evidenceURL:
					hasURL = true
				}
			}
			if hasFS && !hasURL {
				pass.Reportf(call.Pos(),
					"path.%s used on a filesystem path; use filepath.%s (path is for URLs/slash paths)",
					fnName, fnName)
			}
			return true
		})
	}
	return nil, nil
}

// pathPkgCallName reports whether call is one of the flagged functions from
// the standard library "path" package, and returns the function name.
func pathPkgCallName(call *ast.CallExpr, info *types.Info) (name string, ok bool) {
	sel, isSel := call.Fun.(*ast.SelectorExpr)
	if !isSel {
		return "", false
	}
	fn, isFn := info.Uses[sel.Sel].(*types.Func)
	if !isFn || fn.Pkg() == nil {
		return "", false
	}
	if fn.Pkg().Path() != "path" || !pathPkgFuncs[fn.Name()] {
		return "", false
	}
	return fn.Name(), true
}

// argEvidence walks an argument expression and aggregates evidence from
// identifiers (by name) and calls (by callee package). URL evidence anywhere
// in the expression wins over filesystem evidence, since mixed expressions
// like path.Join(baseURL, fileName) are URL construction.
func argEvidence(arg ast.Expr, info *types.Info) pathEvidence {
	result := evidenceNone
	ast.Inspect(arg, func(n ast.Node) bool {
		var ev pathEvidence
		switch node := n.(type) {
		case *ast.Ident:
			ev = classifyName(node.Name)
		case *ast.SelectorExpr:
			ev = selectorEvidence(node, info)
		case *ast.CallExpr:
			ev = callEvidence(node, info)
		default:
			return true
		}
		if ev == evidenceURL {
			result = evidenceURL
			return false
		}
		if ev == evidenceFilesystem && result == evidenceNone {
			result = evidenceFilesystem
		}
		return true
	})
	return result
}

// selectorEvidence classifies a selector by the package of its base type:
// fields/methods of net/url and net/http types (url.URL.Path, req.URL) are
// URL evidence; the selected name itself is classified like any identifier.
func selectorEvidence(sel *ast.SelectorExpr, info *types.Info) pathEvidence {
	if t := info.TypeOf(sel.X); t != nil && typeFromURLPkg(t) {
		return evidenceURL
	}
	return classifyName(sel.Sel.Name)
}

// callEvidence classifies a call by its callee's package path.
func callEvidence(call *ast.CallExpr, info *types.Info) pathEvidence {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return evidenceNone
	}
	fn, ok := info.Uses[sel.Sel].(*types.Func)
	if !ok || fn.Pkg() == nil {
		return evidenceNone
	}
	pkg := fn.Pkg().Path()
	switch {
	case urlEvidencePkgs[pkg]:
		return evidenceURL
	case fsEvidencePkgs[pkg]:
		return evidenceFilesystem
	}
	return evidenceNone
}

// typeFromURLPkg reports whether t (or its pointee) is a named type declared
// in a URL-evidence package, e.g. *url.URL or *http.Request.
func typeFromURLPkg(t types.Type) bool {
	if ptr, ok := t.(*types.Pointer); ok {
		t = ptr.Elem()
	}
	named, ok := t.(*types.Named)
	if !ok || named.Obj().Pkg() == nil {
		return false
	}
	return urlEvidencePkgs[named.Obj().Pkg().Path()]
}

var (
	fsNamePattern  = regexp.MustCompile(`(?i)(path|dir|file|root|home)`)
	urlNamePattern = regexp.MustCompile(`(?i)(url|route|endpoint)`)
)

// classifyName classifies a bare identifier name as filesystem evidence, URL
// evidence, or neither, using fsNamePattern and urlNamePattern.
func classifyName(name string) pathEvidence {
	// URL match wins: a name like "urlPath" matches both patterns, but the
	// developer who wrote it means a URL component, where path.Join is correct.
	if urlNamePattern.MatchString(name) {
		return evidenceURL
	}
	if fsNamePattern.MatchString(name) {
		return evidenceFilesystem
	}
	return evidenceNone
}
