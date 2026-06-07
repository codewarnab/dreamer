package astcheck

import (
	"go/ast"
	"go/types"

	"golang.org/x/tools/go/analysis"
)

var unusedmethodAnalyzer = &analysis.Analyzer{
	Name: "unusedmethod",
	Doc:  "flags unexported funcs/methods with no callers anywhere in the package (including tests); dead helpers accrete review cost",
	Run:  runUnusedmethod,
}

func init() {
	Register(RegistryEntry{
		Analyzer:  unusedmethodAnalyzer,
		Severity:  SevInfo,
		DefaultOn: true,
	})
}

// runUnusedmethod collects unexported funcs declared in non-test files and
// flags those never referenced in pass.TypesInfo.Uses, which spans the whole
// package including test files (PR #60: dead isTestingT/isTestingType helpers).
//
// Intra-package only by design: exported funcs may have external callers, so
// they are out of scope (delegated to whole-program tools like deadcode).
func runUnusedmethod(pass *analysis.Pass) (any, error) {
	if pass.Pkg.Path() == "command-line-arguments" {
		return nil, nil
	}
	used := make(map[types.Object]bool, len(pass.TypesInfo.Uses))
	for _, obj := range pass.TypesInfo.Uses {
		used[obj] = true
	}
	ifaces := packageInterfaces(pass.Pkg)

	for _, file := range pass.Files {
		if isTestFile(pass.Fset.File(file.Pos()).Name()) {
			continue
		}
		for _, decl := range file.Decls {
			fd, ok := decl.(*ast.FuncDecl)
			if !ok {
				continue
			}
			fn, ok := pass.TypesInfo.Defs[fd.Name].(*types.Func)
			if !ok || fn.Exported() || used[fn] {
				continue
			}
			if fn.Name() == "init" || fn.Name() == "main" {
				continue
			}
			if satisfiesPackageInterface(fn, ifaces) {
				continue
			}
			pass.Reportf(fd.Name.Pos(),
				"%s is never used in this package; delete it or wire it up", fn.Name())
		}
	}
	return nil, nil
}

// packageInterfaces returns all interface types declared at package scope.
func packageInterfaces(pkg *types.Package) []*types.Interface {
	var ifaces []*types.Interface
	scope := pkg.Scope()
	for _, name := range scope.Names() {
		tn, ok := scope.Lookup(name).(*types.TypeName)
		if !ok {
			continue
		}
		if iface, ok := tn.Type().Underlying().(*types.Interface); ok {
			ifaces = append(ifaces, iface)
		}
	}
	return ifaces
}

// satisfiesPackageInterface reports whether fn is a method whose receiver type
// implements a package-declared interface that includes a method of the same
// name — such methods are "used" via the interface even with no direct calls.
func satisfiesPackageInterface(fn *types.Func, ifaces []*types.Interface) bool {
	sig, ok := fn.Type().(*types.Signature)
	if !ok || sig.Recv() == nil {
		return false
	}
	recv := sig.Recv().Type()
	for _, iface := range ifaces {
		if !interfaceHasMethod(iface, fn.Name()) {
			continue
		}
		if types.Implements(recv, iface) || types.Implements(types.NewPointer(recv), iface) {
			return true
		}
	}
	return false
}

// interfaceHasMethod reports whether iface declares a method named name.
func interfaceHasMethod(iface *types.Interface, name string) bool {
	for i := range iface.NumMethods() {
		if iface.Method(i).Name() == name {
			return true
		}
	}
	return false
}
