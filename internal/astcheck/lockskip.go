package astcheck

import (
	"go/ast"
	"go/token"
	"go/types"
	"strings"

	"golang.org/x/tools/go/analysis"
)

var lockskipAnalyzer = &analysis.Analyzer{
	Name: "lockskip",
	Doc:  "flags exported methods on mutex-protected structs that call unexported helpers without holding the lock, when other methods on the same struct do hold the lock for the same helper",
	Run:  runLockskip,
}

func init() {
	Register(RegistryEntry{
		Analyzer:  lockskipAnalyzer,
		Severity:  SevError,
		DefaultOn: true,
	})
}

// methodBody holds the AST for a method and its metadata.
type methodBody struct {
	decl     *ast.FuncDecl
	name     string
	exported bool
}

// runLockskip flags exported methods on mutex-protected structs that call
// unexported helpers without holding the lock, when other methods on the same
// struct DO hold the lock for the same helper.
//
// The canonical bug pattern:
//
//	func (s *Store) Load() (*State, error) { return s.load() }        // no lock!
//	func (s *Store) Update(...) error { s.mu.Lock(); defer s.mu.Unlock(); ... s.load() }
//
// If a struct has a sync.Mutex/sync.RWMutex field AND an exported method calls
// an unexported helper without acquiring the lock, but another method on the
// same struct acquires the lock before calling that same helper, the exported
// method has a missing lock.
func runLockskip(pass *analysis.Pass) (any, error) {
	if pass.Pkg.Path() == "command-line-arguments" {
		return nil, nil
	}
	for _, file := range pass.Files {
		if isTestFile(pass.Fset.File(file.Pos()).Name()) {
			continue
		}
		muTypes := mutexProtectedTypes(file, pass)
		if len(muTypes) == 0 {
			continue
		}
		methods := collectMethods(file, pass)
		for typeName := range muTypes {
			analyzeTypeForLockSkip(typeName, methods, pass)
		}
	}
	return nil, nil
}

// mutexProtectedTypes returns the set of struct type names declared in file
// that have a direct sync.Mutex or sync.RWMutex field.
func mutexProtectedTypes(file *ast.File, pass *analysis.Pass) map[string]bool {
	types := make(map[string]bool)
	for _, decl := range file.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok || gd.Tok != token.TYPE {
			continue
		}
		for _, spec := range gd.Specs {
			ts, ok := spec.(*ast.TypeSpec)
			if !ok {
				continue
			}
			st, ok := ts.Type.(*ast.StructType)
			if !ok || st.Fields == nil {
				continue
			}
			if hasMutexField(st, pass) {
				types[ts.Name.Name] = true
			}
		}
	}
	return types
}

// hasMutexField reports whether struct st has a direct sync.Mutex or
// sync.RWMutex field (pointer or value).
func hasMutexField(st *ast.StructType, pass *analysis.Pass) bool {
	for _, field := range st.Fields.List {
		ts := typeString(field.Type, pass)
		if ts == "sync.Mutex" || ts == "sync.RWMutex" ||
			ts == "*sync.Mutex" || ts == "*sync.RWMutex" {
			return true
		}
	}
	return false
}

// typeString returns the string representation of an AST type expression using
// the type checker's info map, falling back to the Ident name.
func typeString(expr ast.Expr, pass *analysis.Pass) string {
	if tv, ok := pass.TypesInfo.Types[expr]; ok {
		return tv.Type.String()
	}
	if id, ok := expr.(*ast.Ident); ok {
		return id.Name
	}
	return ""
}

// collectMethods builds a map of all methods declared in file, keyed by
// (typeName, methodName).
func collectMethods(file *ast.File, pass *analysis.Pass) map[string]map[string]*methodBody {
	methods := make(map[string]map[string]*methodBody)
	for _, decl := range file.Decls {
		fd, ok := decl.(*ast.FuncDecl)
		if !ok || fd.Recv == nil || len(fd.Recv.List) == 0 {
			continue
		}
		typeName := receiverBaseType(fd, pass)
		if typeName == "" {
			continue
		}
		if methods[typeName] == nil {
			methods[typeName] = make(map[string]*methodBody)
		}
		methods[typeName][fd.Name.Name] = &methodBody{
			decl:     fd,
			name:     fd.Name.Name,
			exported: fd.Name.IsExported(),
		}
	}
	return methods
}

// receiverBaseType extracts the base type name from a method receiver,
// unwrapping pointer types and stripping the package qualifier.
func receiverBaseType(fd *ast.FuncDecl, pass *analysis.Pass) string {
	if fd.Recv == nil || len(fd.Recv.List) == 0 {
		return ""
	}
	recvType := fd.Recv.List[0].Type
	if star, ok := recvType.(*ast.StarExpr); ok {
		recvType = star.X
	}
	if tv, ok := pass.TypesInfo.Types[recvType]; ok {
		name := tv.Type.String()
		if i := strings.LastIndex(name, "."); i >= 0 {
			return name[i+1:]
		}
		return name
	}
	if id, ok := recvType.(*ast.Ident); ok {
		return id.Name
	}
	return ""
}

// lockedHelpers holds the set of unexported helper method names called while
// holding the lock.
type lockedHelpers = map[string]bool

// analyzeTypeForLockSkip checks a mutex-protected type for exported methods
// that call locked unexported helpers without holding the lock.
func analyzeTypeForLockSkip(typeName string, methods map[string]map[string]*methodBody, pass *analysis.Pass) {
	typeMethods := methods[typeName]
	if len(typeMethods) == 0 {
		return
	}

	// Phase 1: Find unexported helpers called while holding the lock.
	helpers := findLockedHelpers(typeMethods)

	// Phase 2: Flag exported methods that call locked helpers without the lock.
	for _, m := range typeMethods {
		if !m.exported || m.decl.Body == nil {
			continue
		}
		if methodHoldsLock(m.decl.Body, pass) {
			continue
		}
		for _, call := range findReceiverCalls(m.decl.Body, pass) {
			if helpers[call] {
				pass.Reportf(m.decl.Pos(),
					"exported method %s calls unexported %s without holding the lock; other methods lock before calling %s",
					m.name, call, call)
			}
		}
	}
}

// findLockedHelpers returns the set of unexported helper method names that are
// called by any method that holds the lock.
func findLockedHelpers(typeMethods map[string]*methodBody) lockedHelpers {
	helpers := make(lockedHelpers)
	for _, m := range typeMethods {
		if m.decl.Body == nil {
			continue
		}
		if !hasLockCall(m.decl.Body) {
			continue
		}
		for _, call := range findUnexportedCalls(m.decl.Body) {
			helpers[call] = true
		}
	}
	return helpers
}

// findUnexportedCalls returns the names of unexported methods called on any
// receiver in body.
func findUnexportedCalls(body *ast.BlockStmt) []string {
	var names []string
	ast.Inspect(body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		if !sel.Sel.IsExported() {
			names = append(names, sel.Sel.Name)
		}
		return true
	})
	return names
}

// findReceiverCalls returns the names of methods called on the function's own
// receiver in body. Only returns unexported method names (the pattern we're
// looking for).
func findReceiverCalls(body *ast.BlockStmt, pass *analysis.Pass) []string {
	var names []string
	ast.Inspect(body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if name := callOnSameType(call, pass); name != "" {
			names = append(names, name)
		}
		return true
	})
	return names
}

// --- Lock detection helpers ---

// methodHoldsLock reports whether body contains a lock acquisition followed by
// an unexported helper call (i.e., the method locks before calling helpers).
func methodHoldsLock(body *ast.BlockStmt, pass *analysis.Pass) bool {
	if body == nil {
		return false
	}
	seenLock := false
	for _, stmt := range body.List {
		if isLockCallStmt(stmt, pass) {
			seenLock = true
			continue
		}
		if seenLock {
			if hasUnexportedCall(stmt) {
				return true
			}
		}
	}
	return false
}

// hasLockCall reports whether body contains a mutex .Lock() call.
func hasLockCall(body *ast.BlockStmt) bool {
	found := false
	ast.Inspect(body, func(n ast.Node) bool {
		if found {
			return false
		}
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if isMutexLockCall(call) {
			found = true
			return false
		}
		return true
	})
	return found
}

// hasUnexportedCall reports whether stmt contains a call to an unexported
// method on any receiver.
func hasUnexportedCall(stmt ast.Stmt) bool {
	found := false
	ast.Inspect(stmt, func(n ast.Node) bool {
		if found {
			return false
		}
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		if !sel.Sel.IsExported() {
			found = true
			return false
		}
		return true
	})
	return found
}

// isLockCallStmt reports whether stmt is a mutex .Lock() call at the statement
// level (direct call, assignment RHS, or defer).
func isLockCallStmt(stmt ast.Stmt, pass *analysis.Pass) bool {
	switch s := stmt.(type) {
	case *ast.ExprStmt:
		if call, ok := s.X.(*ast.CallExpr); ok {
			return isMutexLockCall(call)
		}
	case *ast.DeferStmt:
		// defer mu.Unlock() — not a lock acquisition.
		return false
	case *ast.AssignStmt:
		for _, rhs := range s.Rhs {
			if call, ok := rhs.(*ast.CallExpr); ok && isMutexLockCall(call) {
				return true
			}
		}
	case *ast.DeclStmt:
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
				if call, ok := val.(*ast.CallExpr); ok && isMutexLockCall(call) {
					return true
				}
			}
		}
	case *ast.IfStmt:
		if isLockCallStmt(s.Init, pass) {
			return true
		}
		for _, b := range s.Body.List {
			if isLockCallStmt(b, pass) {
				return true
			}
		}
	case *ast.BlockStmt:
		for _, b := range s.List {
			if isLockCallStmt(b, pass) {
				return true
			}
		}
	}
	return false
}

// isMutexLockCall reports whether call is a .Lock() method call (any receiver).
func isMutexLockCall(call *ast.CallExpr) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	return ok && sel.Sel.Name == "Lock"
}

// --- Receiver call analysis ---

// callOnSameType checks if call is an unexported method call on the function's
// own receiver. Returns the called method name, or "" if not applicable.
func callOnSameType(call *ast.CallExpr, pass *analysis.Pass) string {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.IsExported() {
		return ""
	}

	recvIdent := extractBaseIdent(sel.X)
	if recvIdent == nil {
		return ""
	}

	// Verify the receiver is a variable (function parameter / method receiver).
	recvObj := pass.TypesInfo.ObjectOf(recvIdent)
	if recvObj == nil {
		return ""
	}
	if _, ok := recvObj.(*types.Var); !ok {
		return ""
	}

	// Verify the called method is on a type with a receiver (i.e., a method).
	selObj := pass.TypesInfo.Uses[sel.Sel]
	if selObj == nil {
		return ""
	}
	fn, ok := selObj.(*types.Func)
	if !ok {
		return ""
	}
	sig := fn.Type().(*types.Signature)
	if sig.Recv() == nil {
		return ""
	}

	return fn.Name()
}

// extractBaseIdent returns the base identifier from expr, unwrapping pointer
// dereferences.
func extractBaseIdent(expr ast.Expr) *ast.Ident {
	switch e := expr.(type) {
	case *ast.Ident:
		return e
	case *ast.StarExpr:
		return extractBaseIdent(e.X)
	}
	return nil
}
