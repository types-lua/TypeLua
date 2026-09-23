package checker

import (
	"github.com/typelua/typelua/internal/ast"
	"github.com/typelua/typelua/internal/token"
	"github.com/typelua/typelua/internal/types"
)

// Cooperative CSP concurrency (Go-style, single-threaded):
//   async function f() ... end     may contain `await`
//   await <expr>                    suspension marker (erases to expr)
//   spawn <call>                    spawn concurrent task
//   select { case ... default ... } multiplex over channels
//   chan<T> + makeChan<T>(cap?)     typed channels
//   sleep(sec) / timeout(sec)       timers (await them)
//   WaitGroup / Mutex               coordination primitives
//
// `await`/`select` are valid inside `async` functions and the top-level
// chunk (the main task). Anywhere else they report TL38xx.

// inAsyncTask reports whether await/select may appear here: inside an
// async function, or directly in the chunk (asyncStack empty = main task).
func (c *Checker) inAsyncTask() bool {
	if len(c.asyncStack) == 0 {
		return true
	}
	return c.asyncStack[len(c.asyncStack)-1]
}

// checkAwait validates context and checks the operand. The type is the
// operand's type (await erases; blocking runtime calls yield by
// themselves, await is the explicit marker).
func (c *Checker) checkAwait(n *ast.AwaitExpr) types.Type {
	if !c.inAsyncTask() {
		c.errorf("TL3801", n.Span(), "'await' is only allowed inside 'async' functions (or the top-level chunk)")
	}
	return c.checkExpr(n.X)
}

func (c *Checker) checkAwaitMulti(n *ast.AwaitExpr) []types.Type {
	if !c.inAsyncTask() {
		c.errorf("TL3801", n.Span(), "'await' is only allowed inside 'async' functions (or the top-level chunk)")
	}
	return c.checkExprMulti(n.X)
}

// checkSpawn checks a spawned call like a normal call. Spawning is allowed
// anywhere (it never suspends the spawner); the callee runs as a new task.
func (c *Checker) checkSpawn(n *ast.SpawnStmt) {
	c.checkExpr(n.Call)
	if hasBareVarArg(n.Call) {
		c.errorf("TL3235", n.Call.Span(), "cannot use '...' in a 'spawn' call (copy it into a local first)")
	}
}

// checkSelect validates guards, binds receive variables, and checks bodies.
func (c *Checker) checkSelect(n *ast.SelectStmt) {
	if !c.inAsyncTask() {
		c.errorf("TL3802", n.Span(), "'select' is only allowed inside 'async' functions (or the top-level chunk)")
	}
	if len(n.Cases) == 0 && n.Default == nil {
		c.errorf("TL3804", n.Span(), "select must have at least one 'case' or 'default'")
		return
	}
	for _, sc := range n.Cases {
		c.checkSelectCase(sc)
	}
	if n.Default != nil {
		c.push()
		c.checkBlock(n.Default)
		c.pop()
	}
}

func (c *Checker) checkSelectCase(sc *ast.SelectCase) {
	nb := 0
	if sc.Recv != nil {
		nb++
	}
	if sc.Send != nil {
		nb++
	}
	if sc.Timeout != nil {
		nb++
	}
	if nb != 1 {
		c.errorf("TL3804", sc.Span(), "select case must have exactly one guard ('ch:recv()', 'ch:send(v)' or 'timeout(s)')")
		return
	}
	switch {
	case sc.Recv != nil:
		if len(sc.Recv.Args) != 0 {
			c.errorf("TL3804", sc.Recv.Span(), "'ch:recv' expects no arguments, got %d", len(sc.Recv.Args))
		}
		objT := c.checkExpr(sc.Recv.Obj)
		elem := c.chanElem(objT, sc.Recv.Span())
		c.recordMemberUse(sc.Recv.Method.Span(), objT, "recv")
		if len(sc.RecvVars) > 2 {
			c.errorf("TL3804", sc.Span(), "select receive binds at most 2 variables (v, ok)")
			return
		}
		if sc.RecvLocal {
			c.push()
			if len(sc.RecvVars) >= 1 {
				c.recordDef(sc.RecvVars[0].Span(), c.bindVar(sc.RecvVars[0].Name, &Symbol{Name: sc.RecvVars[0].Name, Kind: symVar, Type: elem, Span: sc.RecvVars[0].Span()}))
			}
			if len(sc.RecvVars) == 2 {
				c.recordDef(sc.RecvVars[1].Span(), c.bindVar(sc.RecvVars[1].Name, &Symbol{Name: sc.RecvVars[1].Name, Kind: symVar, Type: types.Boolean, Span: sc.RecvVars[1].Span()}))
			}
			c.checkBlock(sc.Block)
			c.pop()
		} else if len(sc.RecvVars) > 0 {
			// Assign form: `case v = ch:recv()` / `case v, ok = ch:recv()`.
			want := []types.Type{elem, types.Boolean}
			for i, v := range sc.RecvVars {
				sym := c.cur().lookupVar(v.Name)
				if sym == nil {
					c.errorf("TL3014", v.Span(), "undefined variable '%s' (use 'case local %s = ch:recv()' to declare it)", v.Name, v.Name)
					continue
				}
				c.recordUse(v.Span(), sym)
				if sym.IsConst {
					c.errorf("TL3015", v.Span(), "cannot assign to const variable '%s'", v.Name)
				}
				if i < len(want) && !types.IsAssignable(want[i], sym.Type, c.opt.toTypes()) {
					c.errorf("TL3010", v.Span(), "type '%s' is not assignable to type '%s'", want[i].String(), sym.Type.String())
				}
			}
			c.push()
			c.checkBlock(sc.Block)
			c.pop()
		} else {
			// Bare `case ch:recv() then`: value discarded.
			c.push()
			c.checkBlock(sc.Block)
			c.pop()
		}
	case sc.Send != nil:
		if len(sc.Send.Args) != 1 {
			c.errorf("TL3804", sc.Send.Span(), "'ch:send' expects exactly 1 argument, got %d", len(sc.Send.Args))
			c.push()
			c.checkBlock(sc.Block)
			c.pop()
			return
		}
		objT := c.checkExpr(sc.Send.Obj)
		elem := c.chanElem(objT, sc.Send.Span())
		c.recordMemberUse(sc.Send.Method.Span(), objT, "send")
		argT := c.checkExpr(sc.Send.Args[0])
		if elem != nil && !types.IsAssignable(argT, elem, c.opt.toTypes()) {
			c.errorf("TL3010", sc.Send.Args[0].Span(), "type '%s' is not assignable to channel element type '%s'", argT.String(), elem.String())
		}
		c.push()
		c.checkBlock(sc.Block)
		c.pop()
	case sc.Timeout != nil:
		t := c.checkExpr(sc.Timeout)
		if !isAnyLike(t) && !isNumberLike(t) {
			c.errorf("TL3804", sc.Timeout.Span(), "select timeout expects seconds as a number, got '%s'", t.String())
		}
		c.push()
		c.checkBlock(sc.Block)
		c.pop()
	}
}

// chanElem returns the element type of a channel type (or Any with an
// error when the operand is not a channel).
func (c *Checker) chanElem(t types.Type, sp token.Span) types.Type {
	if ch, ok := t.(*types.Chan); ok {
		return ch.Elem
	}
	if isAnyLike(t) {
		return types.Any
	}
	// Resolve through generic params with channel constraints?
	if gp, ok := t.(*types.GenericParam); ok && gp.Constraint != nil {
		if ch, ok := gp.Constraint.(*types.Chan); ok {
			return ch.Elem
		}
		return types.Any
	}
	c.errorf("TL3804", sp, "select expects a 'chan<T>' (got '%s'); send/receive need channels", t.String())
	return types.Any
}

// chanMethodType resolves `send/recv/close/len/cap/closed/iter` on chan<T>.
func chanMethodType(elem types.Type, name string) (types.Type, bool) {
	switch name {
	case "send":
		return &types.Func{Params: []types.FuncParam{{Name: "value", Type: elem}}}, true
	case "recv":
		return &types.Func{Returns: []types.Type{elem, types.Boolean}}, true
	case "close":
		return &types.Func{}, true
	case "len", "cap":
		return &types.Func{Returns: []types.Type{types.Integer}}, true
	case "closed":
		return &types.Func{Returns: []types.Type{types.Boolean}}, true
	case "iter":
		// `for v in ch:iter() do`: iterator function yielding elements.
		return &types.Func{Returns: []types.Type{&types.Func{Returns: []types.Type{elem}}}}, true
	}
	return nil, false
}

// waitGroupType is the instance object for WaitGroup.new().
func waitGroupType() types.Type {
	return &types.Object{Name: "WaitGroup", Fields: map[string]types.Field{
		"add":  {Type: &types.Func{Params: []types.FuncParam{{Name: "n", Type: types.Integer}}}},
		"done": {Type: &types.Func{}},
		"wait": {Type: &types.Func{}},
	}}
}

// mutexType is the instance object for Mutex.new().
func mutexType() types.Type {
	return &types.Object{Name: "Mutex", Fields: map[string]types.Field{
		"lock":    {Type: &types.Func{}},
		"unlock":  {Type: &types.Func{}},
		"tryLock": {Type: &types.Func{Returns: []types.Type{types.Boolean}}},
	}}
}
