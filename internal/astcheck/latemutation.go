package astcheck

import (
	"go/ast"
	"go/token"
	"go/types"

	"golang.org/x/tools/go/analysis"
)

var latemutationAnalyzer = &analysis.Analyzer{
	Name: "latemutation",
	Doc:  `flags a field write to a struct value variable after it was already passed by value to a call, with no later use of the variable — the write mutates a local copy nobody reads`,
	Run:  runLatemutation,
}

func init() {
	Register(RegistryEntry{
		Analyzer:  latemutationAnalyzer,
		Severity:  SevWarn,
		DefaultOn: true,
	})
}

// latemutationEvent is one occurrence of a candidate variable inside a
// function body, in source order.
type latemutationEvent struct {
	pos  token.Pos
	kind latemutationKind
	// node is the ident occurrence; reported positions point at the write.
	node ast.Node
	// inLoop is true when the occurrence is lexically inside a for/range
	// statement, where source order does not imply execution order.
	inLoop bool
	// inFuncLit is true when the occurrence is inside a closure, which may
	// execute at any time relative to surrounding statements.
	inFuncLit bool
}

type latemutationKind int

const (
	// kindPassedByValue: the variable appears as a direct by-value argument
	// to a call (the callee received a snapshot copy).
	kindPassedByValue latemutationKind = iota
	// kindFieldWrite: an assignment writes a field of the variable (x.F = v).
	kindFieldWrite
	// kindOtherUse: any other read/use of the variable (return, RHS read,
	// composite literal, later call argument, range, etc.).
	kindOtherUse
)

// runLatemutation inspects each function body for local struct-typed value
// variables, records every occurrence in source order, and reports field
// writes that lateWriteIsLost classifies as lost mutations.
func runLatemutation(pass *analysis.Pass) (any, error) {
	for _, file := range pass.Files {
		ast.Inspect(file, func(n ast.Node) bool {
			fd, ok := n.(*ast.FuncDecl)
			if !ok || fd.Body == nil {
				return true
			}
			checkLatemutationFunc(pass, fd.Body)
			return true
		})
	}
	return nil, nil
}

// checkLatemutationFunc analyzes one function body.
func checkLatemutationFunc(pass *analysis.Pass, body *ast.BlockStmt) {
	loops, funcLits := latemutationRegions(body)

	// Pre-classify special ident occurrences so the generic ident walk can
	// treat everything else as kindOtherUse.
	fieldWriteBases := map[*ast.Ident]bool{} // x in `x.F = v` LHS
	callArgs := map[*ast.Ident]bool{}        // x in `f(x)`
	addressTaken := map[types.Object]bool{}  // any `&x` disqualifies the var

	ast.Inspect(body, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.AssignStmt:
			for _, lhs := range node.Lhs {
				if sel, ok := lhs.(*ast.SelectorExpr); ok {
					if ident, ok := sel.X.(*ast.Ident); ok {
						fieldWriteBases[ident] = true
					}
				}
			}
		case *ast.CallExpr:
			for _, arg := range node.Args {
				if ident, ok := arg.(*ast.Ident); ok {
					callArgs[ident] = true
				}
			}
		case *ast.UnaryExpr:
			if node.Op == token.AND {
				if ident, ok := node.X.(*ast.Ident); ok {
					if obj := pass.TypesInfo.Uses[ident]; obj != nil {
						addressTaken[obj] = true
					}
				}
			}
		}
		return true
	})

	// Collect per-variable event streams in source order (ast.Inspect visits
	// in position order within a file).
	events := map[types.Object][]latemutationEvent{}
	ast.Inspect(body, func(n ast.Node) bool {
		ident, ok := n.(*ast.Ident)
		if !ok {
			return true
		}
		obj := pass.TypesInfo.Uses[ident]
		if obj == nil {
			return true
		}
		if !latemutationCandidate(obj, body) {
			return true
		}
		kind := kindOtherUse
		switch {
		case fieldWriteBases[ident]:
			kind = kindFieldWrite
		case callArgs[ident]:
			kind = kindPassedByValue
		}
		events[obj] = append(events[obj], latemutationEvent{
			pos:       ident.Pos(),
			kind:      kind,
			node:      ident,
			inLoop:    latemutationWithin(loops, ident.Pos()),
			inFuncLit: latemutationWithin(funcLits, ident.Pos()),
		})
		return true
	})

	for obj, evs := range events {
		if addressTaken[obj] {
			continue // aliased: writes may be observable through the pointer
		}
		for i, ev := range evs {
			if ev.kind != kindFieldWrite {
				continue
			}
			hasEarlierPass := false
			for _, prev := range evs[:i] {
				if prev.kind == kindPassedByValue {
					hasEarlierPass = true
					break
				}
			}
			hasLaterUse := false
			for _, next := range evs[i+1:] {
				if next.kind != kindFieldWrite {
					hasLaterUse = true
					break
				}
			}
			if lateWriteIsLost(hasEarlierPass, hasLaterUse, ev.inLoop, ev.inFuncLit) {
				pass.Reportf(ev.node.Pos(),
					"field write to %q after it was passed by value; the callee saw a copy and nothing reads this write — set the field before the call",
					obj.Name())
			}
		}
	}
}

// lateWriteIsLost decides whether a field write to a struct value variable
// should be reported as a lost mutation.
//
// Inputs, all describing the write under inspection:
//   - hasEarlierPass: the variable was passed by value to some call BEFORE
//     this write (a callee holds a snapshot that this write cannot affect).
//   - hasLaterUse: AFTER this write, the variable is read, returned, or
//     passed somewhere (so the written value is still observable by someone).
//   - inLoop: the write sits lexically inside a for/range body, where a
//     "later" pass may actually execute after the write on the next iteration.
//   - inFuncLit: the write sits inside a closure, which may run at any time.
func lateWriteIsLost(hasEarlierPass, hasLaterUse, inLoop, inFuncLit bool) bool {
	return hasEarlierPass && !hasLaterUse && !inLoop && !inFuncLit
}

// latemutationCandidate reports whether obj is a local variable of struct
// value type declared within body — the only shape this analyzer reasons about.
func latemutationCandidate(obj types.Object, body *ast.BlockStmt) bool {
	v, ok := obj.(*types.Var)
	if !ok || v.IsField() {
		return false
	}
	if v.Pos() < body.Pos() || v.Pos() > body.End() {
		return false // parameter, receiver, or package-level variable
	}
	_, isStruct := v.Type().Underlying().(*types.Struct)
	return isStruct
}

// latemutationRegions returns the position intervals of loop bodies and
// function literals inside body, used to tag events whose lexical order does
// not imply execution order.
func latemutationRegions(body *ast.BlockStmt) (loops, funcLits [][2]token.Pos) {
	ast.Inspect(body, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.ForStmt:
			loops = append(loops, [2]token.Pos{node.Body.Pos(), node.Body.End()})
		case *ast.RangeStmt:
			loops = append(loops, [2]token.Pos{node.Body.Pos(), node.Body.End()})
		case *ast.FuncLit:
			funcLits = append(funcLits, [2]token.Pos{node.Pos(), node.End()})
		}
		return true
	})
	return loops, funcLits
}

// latemutationWithin reports whether pos falls inside any of the intervals.
func latemutationWithin(regions [][2]token.Pos, pos token.Pos) bool {
	for _, r := range regions {
		if pos >= r[0] && pos <= r[1] {
			return true
		}
	}
	return false
}
