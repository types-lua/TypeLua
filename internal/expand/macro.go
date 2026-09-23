package expand

import (
	"github.com/typelua/typelua/internal/ast"
	"github.com/typelua/typelua/internal/diag"
	"github.com/typelua/typelua/internal/token"
)

// Macro expansion core: arity, recursion guard, call drivers.

// ---------- macro expansion core ----------

// checkArity validates argument count. A variadic macro (`...`) requires
// at least len(params); otherwise the count must match exactly.
// Mismatches report (best-effort runnable output) via a nil return.
func (e *expander) checkArity(md *macroDef, call token.Span, nargs int) []int {
	if md.hasVarArg {
		if nargs < len(md.params) {
			e.errorf(diag.MacroArityMismatch, call, "macro '%s' expects at least %d argument(s), got %d", md.name, len(md.params), nargs)
			return nil
		}
	} else if nargs != len(md.params) {
		e.errorf(diag.MacroArityMismatch, call, "macro '%s' expects %d argument(s), got %d", md.name, len(md.params), nargs)
		return nil
	}
	idx := make([]int, nargs)
	for i := range idx {
		idx[i] = i
	}
	return idx
}

// enter guards recursion: same macro re-entered, or depth exceeded.
func (e *expander) enter(name string, sp token.Span) bool {
	for _, n := range e.stack {
		if n == name {
			e.errorf(diag.RecursiveMacro, sp, "recursive macro expansion of '%s'", name)
			return false
		}
	}
	if len(e.stack) >= maxExpandDepth {
		e.errorf(diag.RecursiveMacro, sp, "macro expansion too deep (possible recursion via '%s')", name)
		return false
	}
	e.stack = append(e.stack, name)
	return true
}

func (e *expander) leave() {
	e.stack = e.stack[:len(e.stack)-1]
}

// renameScope prepares a rename context: avoid-set from caller arguments,
// labels pre-scanned over the copied body.
func (e *expander) renameScope(args []ast.Expr) *renameCtx {
	r := &renameCtx{e: e, vars: map[string]string{}, labels: map[string]string{}, avoid: map[string]bool{}}
	for _, a := range args {
		collectIdents(r.avoid, a)
	}
	return r
}

// reportStrayDots reports variadic `...` used outside call arguments.
func (e *expander) reportStrayDots(md *macroDef, sub *substCtx) {
	for _, sp := range sub.strayDots {
		e.errorf(diag.MacroVarargOutsideCallArgs, sp, "variadic '...' of macro '%s' must appear in call arguments", md.name)
	}
}

// expandExprMacro expands an expression-macro call.
func (e *expander) expandExprMacro(md *macroDef, call *ast.CallExpr, env *scope) ast.Expr {
	// Expand arguments first (caller context).
	args := make([]ast.Expr, len(call.Args))
	for i, a := range call.Args {
		args[i] = e.expandExpr(a, env)
	}
	if e.checkArity(md, call.Span(), len(args)) == nil {
		return &ast.NilLit{S: call.Span()}
	}
	if !e.enter(md.name, call.Span()) {
		return &ast.NilLit{S: call.Span()}
	}
	defer e.leave()
	body := copyExpr(md.body)
	r := e.renameScope(args)
	r.prescanLabelsExpr(body)
	r.renameExpr(body)
	sub := &substCtx{args: map[string]ast.Expr{}, shadow: map[string]int{}}
	for i, p := range md.params {
		sub.args[p] = args[i]
	}
	if md.hasVarArg {
		sub.varArgs = append([]ast.Expr{}, args[len(md.params):]...)
	}
	body = substExpr(body, sub)
	e.reportStrayDots(md, sub)
	return e.expandExpr(body, env)
}

// expandStmtMacro expands a statement-macro call into statements.
func (e *expander) expandStmtMacro(md *macroDef, call *ast.CallExpr, env *scope) []ast.Stmt {
	args := make([]ast.Expr, len(call.Args))
	for i, a := range call.Args {
		args[i] = e.expandExpr(a, env)
	}
	if e.checkArity(md, call.Span(), len(args)) == nil {
		return nil
	}
	if !e.enter(md.name, call.Span()) {
		return nil
	}
	defer e.leave()
	blk := copyBlock(md.block)
	r := e.renameScope(args)
	r.prescanLabelsBlock(blk)
	r.renameBlock(blk)
	sub := &substCtx{args: map[string]ast.Expr{}, shadow: map[string]int{}}
	for i, p := range md.params {
		sub.args[p] = args[i]
	}
	if md.hasVarArg {
		sub.varArgs = append([]ast.Expr{}, args[len(md.params):]...)
	}
	substBlock(blk, sub)
	e.reportStrayDots(md, sub)
	// Spliced placeholders must yield real statements (calls); anything
	// else would emit invalid Lua.
	var valid []ast.Stmt
	for _, st := range blk.Stmts {
		if es, ok := st.(*ast.ExprStmt); ok {
			inner := es.E
			for {
				aw, ok := inner.(*ast.AwaitExpr)
				if !ok {
					break
				}
				inner = aw.X
			}
			switch inner.(type) {
			case *ast.CallExpr, *ast.MethodCallExpr:
			default:
				e.errorf(diag.MacroExpandsToInvalidStmt, es.Span(), "macro '%s' expanded to an invalid statement", md.name)
				continue
			}
		}
		valid = append(valid, st)
	}
	blk.Stmts = valid
	// Expand nested calls (and nested definitions) in call-site scope.
	child := &scope{parent: env}
	tmp := &ast.Block{Stmts: blk.Stmts, S: call.Span()}
	e.expandBlock(tmp, child)
	return tmp.Stmts
}
