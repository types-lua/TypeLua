package emitter

import (
	"strconv"

	"github.com/typelua/typelua/internal/ast"
)

// Defer support (Go's defer, Zig's errdefer, Lua-flavored):
//
//   - `defer <call>` registers cleanup run LIFO on every NORMAL function
//     exit (each `return`, plus fallthrough). Calls evaluate lazily inside
//     a closure (unlike Go's immediate arguments; same as Lua closures).
//   - `errdefer <call>` registers cleanup run LIFO only when leaving via
//     an explicit `error(...)` call in the same function.
//   - Deferred entries share one list: normal exits skip err entries,
//     error exits run everything LIFO (Zig order).
//   - NOT covered: unwinding from propagated callee errors or failed
//     asserts (use `<close>` locals: real Lua semantics, all exits).
//
// Emission: one `{fn, err}` list per deferring function, a runner that
// returns its arguments (`return __r(f())` preserves multi-values without
// pack/unpack), and registration closures at each defer site.

// deferFrame tracks one function body under emission.
type deferFrame struct {
	top      *ast.Block // function body (nil until set)
	listVar  string     // __tl_defer_N
	runVar   string     // normal-exit runner
	errVar   string     // error-exit runner (only when hasErr)
	hasDefer bool
	hasErr   bool
}

func (e *emitter) deferTop() *deferFrame {
	if len(e.deferStack) == 0 {
		return nil
	}
	return e.deferStack[len(e.deferStack)-1]
}

// scanDefers reports defer/errdefer statements in the current function
// (descending into plain blocks, never into nested functions, classes,
// or macro definitions).
func scanDefers(b *ast.Block) (hasDefer, hasErr bool) {
	if b == nil {
		return false, false
	}
	var walkBlock func(b *ast.Block)
	var walkStmt func(st ast.Stmt)
	walkStmt = func(st ast.Stmt) {
		switch n := st.(type) {
		case *ast.DeferStmt:
			if n.IsErr {
				hasErr = true
			} else {
				hasDefer = true
			}
		case *ast.DoBlock:
			walkBlock(n.Block)
		case *ast.IfStmt:
			walkBlock(n.Then)
			for _, ei := range n.Elseifs {
				walkBlock(ei.Block)
			}
			walkBlock(n.Else)
		case *ast.WhileStmt:
			walkBlock(n.Block)
		case *ast.RepeatStmt:
			walkBlock(n.Block)
		case *ast.NumericFor:
			walkBlock(n.Block)
		case *ast.GenericFor:
			walkBlock(n.Block)
		case *ast.SpawnStmt:
			// Spawned body is a new task (nested function): not this frame.
		case *ast.SelectStmt:
			for _, sc := range n.Cases {
				walkBlock(sc.Block)
			}
			walkBlock(n.Default)
		}
	}
	walkBlock = func(b *ast.Block) {
		if b == nil {
			return
		}
		for _, st := range b.Stmts {
			walkStmt(st)
		}
	}
	walkBlock(b)
	return hasDefer, hasErr
}

// pushDeferFrame opens a function frame, allocating temporaries when the
// body defers. Call sites: every function body (incl. methods, ctors)
// and the top-level chunk.
func (e *emitter) pushDeferFrame(b *ast.Block) {
	hasDefer, hasErr := scanDefers(b)
	fr := &deferFrame{top: b, hasDefer: hasDefer, hasErr: hasErr}
	if hasDefer || hasErr {
		e.tmpCount++
		fr.listVar = "__tl_defer_" + strconv.Itoa(e.tmpCount)
		fr.runVar = "__tl_rundefer_" + strconv.Itoa(e.tmpCount)
		if hasErr {
			fr.errVar = "__tl_runerr_" + strconv.Itoa(e.tmpCount)
		}
	}
	e.deferStack = append(e.deferStack, fr)
}

func (e *emitter) popDeferFrame() {
	e.deferStack = e.deferStack[:len(e.deferStack)-1]
}

// emitDeferPrelude emits the defer list and runners (after vararg prelude,
// before body statements) at the given indent.
func (e *emitter) emitDeferPrelude(ind int) {
	fr := e.deferTop()
	if fr == nil || (!fr.hasDefer && !fr.hasErr) {
		return
	}
	saved := e.indent
	e.indent = ind
	if fr.hasDefer {
		e.pad()
		e.write("local " + fr.listVar + " = {}")
		e.nl()
		e.pad()
		e.write("local function " + fr.runVar + "(...) for __i = #" + fr.listVar + ", 1, -1 do if not " + fr.listVar + "[__i].err then " + fr.listVar + "[__i].fn() end end return ... end")
		e.nl()
	} else {
		// Error-only frames still need the list (normal runner unused).
		e.pad()
		e.write("local " + fr.listVar + " = {}")
		e.nl()
	}
	if fr.hasErr {
		e.pad()
		e.write("local function " + fr.errVar + "() for __i = #" + fr.listVar + ", 1, -1 do " + fr.listVar + "[__i].fn() end end")
		e.nl()
	}
	e.indent = saved
}

// emitDeferTrail runs pending defers at function-body end (fallthrough
// path only: a present Ret always exits through the rewritten return).
func (e *emitter) emitDeferTrail(b *ast.Block, ind int) {
	fr := e.deferTop()
	if fr == nil || fr.top != b || !fr.hasDefer {
		return
	}
	if b.Ret != nil {
		return
	}
	saved := e.indent
	e.indent = ind
	e.pad()
	e.write(fr.runVar + "()")
	e.nl()
	e.indent = saved
}

// emitReturn emits a return statement with defer/error rewriting for the
// current frame (plain return otherwise).
func (e *emitter) emitReturn(ret *ast.ReturnStmt) {
	e.mark(ret, "")
	e.pad()
	fr := e.deferTop()
	if fr != nil && fr.hasErr && len(ret.Exprs) >= 1 && isErrorCall(ret.Exprs[0]) {
		// `return error(...)`: errdefers (+all defers) run, then throw.
		e.write("do " + fr.errVar + "(); ")
		e.emitExpr(ret.Exprs[0])
		e.write(" end")
		e.nl()
		return
	}
	e.write("return")
	if fr != nil && fr.hasDefer && len(ret.Exprs) > 0 {
		e.write(" " + fr.runVar + "(")
		for i, x := range ret.Exprs {
			if i > 0 {
				e.write(", ")
			}
			e.emitExpr(x)
		}
		e.write(")")
	} else if fr != nil && fr.hasDefer {
		e.write(" " + fr.runVar + "()")
	} else if len(ret.Exprs) > 0 {
		e.write(" ")
		for i, x := range ret.Exprs {
			if i > 0 {
				e.write(", ")
			}
			e.emitExpr(x)
		}
	}
	e.nl()
}

// isErrorCall reports checker-marked builtin `error(...)` calls.
func isErrorCall(x ast.Expr) bool {
	ce, ok := x.(*ast.CallExpr)
	return ok && ce.ErrorCall
}
