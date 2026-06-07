package astcheck

import (
	"go/ast"
	"go/types"

	"golang.org/x/tools/go/analysis"
)

var discardederrAnalyzer = &analysis.Analyzer{
	Name: "discardederr",
	Doc:  "flags _-discard of an error return from a curated set of callees where ignoring the error reliably hid a bug",
	Run:  runDiscardederr,
}

func init() {
	Register(RegistryEntry{
		Analyzer:  discardederrAnalyzer,
		Severity:  SevWarn,
		DefaultOn: true,
	})
}

// discardedErrCallees is the curated allowlist of callees whose error return,
// when dropped into `_`, has hidden a real bug in past PRs. errcheck does NOT
// flag explicit `_` discards, which is exactly why these slipped through — but
// a blanket "all dropped errors" rule is far too noisy (deferred Close,
// best-effort Reportf, etc. are legitimately ignored everywhere). So we keep a
// small, high-signal list grounded in observed bugs.
//
// Keys are normalized callee identities:
//   - package functions:  "<pkgpath>.<Func>"   e.g. "time.ParseDuration", "encoding/json.Marshal"
//   - methods:            "(*<pkgpath>.<Type>).<Method>"  e.g. "(*os.File).Sync"
//
// Membership rule: include only callees where dropping the error is ALWAYS a
// latent bug, not merely "often." Note os.WriteFile is intentionally absent —
// the atomicwrite analyzer already steers writers away from it, so listing it
// here would double-report.
var discardedErrCallees = map[string]bool{
	// Parse functions: a dropped error means silently proceeding with the
	// zero value (0, 0s) as if the input were valid (PR #18, #21, #34).
	"time.ParseDuration": true,
	"strconv.Atoi":       true,
	"strconv.ParseInt":   true,
	// Environment/path lookups: an ignored error yields "" and the caller
	// builds paths off an empty root (PR #19, #57).
	"os.Executable":  true,
	"os.UserHomeDir": true,
	// Marshal: a dropped error returns nil bytes, persisting empty/corrupt
	// state without any signal (PR #44, #52).
	"encoding/json.Marshal": true,
	// Sync: a silent failure defeats the durability guarantee the call exists
	// to provide (PR #60, mirrors the WriteFileAtomic fsync path).
	"(*os.File).Sync": true,
	// exec.Cmd: a dropped error means treating a failed/missing/killed child
	// process as success — runner.go discarded cmd.Run() and reported a clean
	// exit for a crashed provider (PR #60).
	"(*os/exec.Cmd).Run":            true,
	"(*os/exec.Cmd).Output":         true,
	"(*os/exec.Cmd).CombinedOutput": true,
	"(*os/exec.Cmd).Wait":           true,
	// Activity log read: a blank-discard returns a nil slice, rendering an
	// empty activity feed with no signal that the read failed (L8, PR #84).
	"dreamer/internal/backgroundjobs.ReadAllActivity": true,
}

// runDiscardederr flags assignments that drop a curated callee's error return
// into the blank identifier.
func runDiscardederr(pass *analysis.Pass) (interface{}, error) {
	if pass.Pkg.Path() == "command-line-arguments" {
		return nil, nil
	}
	for _, file := range pass.Files {
		ast.Inspect(file, func(n ast.Node) bool {
			assign, ok := n.(*ast.AssignStmt)
			if !ok || len(assign.Rhs) != 1 {
				return true
			}
			call, ok := assign.Rhs[0].(*ast.CallExpr)
			if !ok {
				return true
			}
			key := calleeKey(call, pass.TypesInfo)
			if key == "" || !discardedErrCallees[key] {
				return true
			}
			if errorDiscardedAt(assign.Lhs, call, pass.TypesInfo) {
				pass.Reportf(call.Pos(),
					"error from %s discarded; ignoring it has hidden bugs — handle or check it", key)
			}
			return true
		})
	}
	return nil, nil
}

// errorDiscardedAt reports whether any LHS slot that receives an error-typed
// result of the call is the blank identifier `_`.
func errorDiscardedAt(lhs []ast.Expr, call *ast.CallExpr, info *types.Info) bool {
	callType := info.TypeOf(call)
	if callType == nil {
		return false
	}
	for i, target := range lhs {
		ident, ok := target.(*ast.Ident)
		if !ok || ident.Name != "_" {
			continue
		}
		if isErrorType(resultTypeAt(callType, i, len(lhs))) {
			return true
		}
	}
	return false
}

// resultTypeAt returns the i-th result type of a call whose overall type is
// callType, given that n values are being assigned. For a multi-value return
// callType is a *types.Tuple; for a single value it is the value type itself.
func resultTypeAt(callType types.Type, i, n int) types.Type {
	if tuple, ok := callType.(*types.Tuple); ok {
		if i < tuple.Len() {
			return tuple.At(i).Type()
		}
		return nil
	}
	if n == 1 && i == 0 {
		return callType
	}
	return nil
}

// isErrorType reports whether t is the predeclared error interface.
func isErrorType(t types.Type) bool {
	if t == nil {
		return false
	}
	named, ok := t.(*types.Named)
	if !ok {
		return false
	}
	// The builtin error type is a Named type declared in the universe scope
	// (its object has a nil package).
	return named.Obj().Name() == "error" && named.Obj().Pkg() == nil
}

// calleeKey returns the normalized identity of a call's callee, matching the
// key format of discardedErrCallees. Returns "" for calls it cannot resolve
// (non-selector calls, builtins, etc.).
func calleeKey(call *ast.CallExpr, info *types.Info) string {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return ""
	}
	fn, ok := info.Uses[sel.Sel].(*types.Func)
	if !ok {
		return ""
	}
	sig, ok := fn.Type().(*types.Signature)
	if !ok {
		return ""
	}
	if recv := sig.Recv(); recv != nil {
		return methodKey(recv.Type(), fn.Name())
	}
	if fn.Pkg() != nil {
		return fn.Pkg().Path() + "." + fn.Name()
	}
	return ""
}

// methodKey builds a "(*pkgpath.Type).Method" / "(pkgpath.Type).Method" key
// from a receiver type. Returns "" if the receiver is not a named type.
func methodKey(recvType types.Type, method string) string {
	star := ""
	if ptr, ok := recvType.(*types.Pointer); ok {
		star = "*"
		recvType = ptr.Elem()
	}
	named, ok := recvType.(*types.Named)
	if !ok || named.Obj().Pkg() == nil {
		return ""
	}
	return "(" + star + named.Obj().Pkg().Path() + "." + named.Obj().Name() + ")." + method
}
