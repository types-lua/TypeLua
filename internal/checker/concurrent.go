package checker

import (
	"github.com/typelua/typelua/internal/ast"
	"github.com/typelua/typelua/internal/diag"
	"github.com/typelua/typelua/internal/token"
	"github.com/typelua/typelua/internal/types"
)

// 设计者视角：async/spawn/select 的合法性由 asyncStack 与 spawnDepth 两个
// 计数器判定；阻塞方法（send/recv/iter）被标 IsAsync，以便 TL3805 检查。
// 并发单线程 CSP：async 可挂起、await 记号擦除、spawn 开任务。
//
//	async function f() ... end     may contain `await`
//
// 排查看 Span/诊断，如 `local x=1` 错圈整句。
// 排查看 Span/诊断，如 `local x=1` 错圈整句。
// 排查看 Span/诊断，如 `local x=1` 错圈整句。
// …（略，详见实现；排查先看 Span/诊断）
// IsAsync。障碍是嵌套函数：内层 sync 即使外层 async 亦假（栈顶为准），防挂起逃逸。
// 取舍：只读栈顶，不遍历。不变量：spawn 内仍假（spawn 豁免另由 spawnDepth 承担）。
// 排查现场：select/await 在 async 里仍报，先查方法/函数体的 asyncStack 入栈值。
func (c *Checker) inAsyncTask() bool {
	if len(c.asyncStack) == 0 {
		return true
	}
	return c.asyncStack[len(c.asyncStack)-1]
}

// 排查看 Span/诊断，如 `local x=1` 错圈整句。
// 排查看 Span/诊断，如 `local x=1` 错圈整句。
// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：checkAwait 是 await 的“检票口”——语境非法即报 AwaitOutsideAsync，
// 操作数照验、类型原样返回（擦除）。障碍是非法语境下仍需验操作数：否则操作数内
// 的错被吞，修完语境又冒新错；故先报后验。取舍：不改类型，不拦运行时阻塞调用
// 自身让出。不变量：永返操作数型。排查现场：await 类型丢失先查是否误包成了新类型。

func (c *Checker) checkAwait(n *ast.AwaitExpr) types.Type {
	if !c.inAsyncTask() {
		c.errorf(diag.AwaitOutsideAsync, n.Span(), "'await' is only allowed inside 'async' functions (or the top-level chunk)")
	}
	return c.checkExpr(n.X)
}

// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：checkAwaitMulti 与 checkAwait 同门禁不同装量——末位多返回需铺开
// （如 await f() 作调用末参），故调 checkExprMulti。障碍是单/多语境混用：
// 表达式与参数列表各走一版，错用即丢值。取舍：门禁逻辑复刻（不合并不分支，保报错
// 一致）。不变量：返回切片与操作数多值一致。排查现场：await 多值丢失先查调用方
// 是否误调单值版。
func (c *Checker) checkAwaitMulti(n *ast.AwaitExpr) []types.Type {
	if !c.inAsyncTask() {
		c.errorf(diag.AwaitOutsideAsync, n.Span(), "'await' is only allowed inside 'async' functions (or the top-level chunk)")
	}
	return c.checkExprMulti(n.X)
}

// 排查看 Span/诊断，如 `local x=1` 错圈整句。
// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：checkSpawn 是新任务的“发射架”——spawnDepth+1 后按常验调用，验完
// 即减，故被调 async 在其内豁免 TL3805（见 checkAsyncCall）。障碍是裸 `...` 不能
// 跨闭包（spawn 体无 vararg 可绑）：验后另查 hasBareVarArg 并指路“先存局部”。
// 取舍：spawn 处处可发（不挂起发射者）。不变量：spawnDepth 进出配对。排查现场：
// TL3805 在 spawn 内仍报，先查 depth 是否因早返未减。

func (c *Checker) checkSpawn(n *ast.SpawnStmt) {
	c.spawnDepth++
	c.checkExpr(n.Call)
	c.spawnDepth--
	if hasBareVarArg(n.Call) {
		c.errorf(diag.BareVarargInTaskCall, n.Call.Span(), "cannot use '...' in a 'spawn' call (copy it into a local first)")
	}
}

// 排查看 Span/诊断，如 `local x=1` 错圈整句。
// 排查看 Span/诊断，如 `local x=1` 错圈整句。
// 排查看 Span/诊断，如 `local x=1` 错圈整句。
// calls through `any` are unchecked) and never fires inside `spawn`.
//
// 设计者视角：checkAsyncCall 是阻塞调用的“无调度报警”——IsAsync 函数在同步语境
// 直调会让出而无调度，故报 AsyncCallOutsideAsync 并指路 await/spawn。障碍是动态
// 调用（any）无 IsAsync 可看：只能 best-effort 放过，防误报。spawn 内豁免（新任务
// 即调度）。取舍：只看静态 Func 型，非 Func 即返。不变量：纯校验，不改类型。
// 排查现场：该报未报先查被调型是否被 any 化或 IsAsync 丢失（如 chan 方法表）。

func (c *Checker) checkAsyncCall(fnT types.Type, sp token.Span, name string) {
	fn, ok := fnT.(*types.Func)
	if !ok || !fn.IsAsync {
		return
	}
	if c.spawnDepth > 0 || c.inAsyncTask() {
		return
	}
	if name == "" {
		name = "async function"
	}
	c.errorf(diag.AsyncCallOutsideAsync, sp, "calling async '%s' outside an async task (use 'await' inside 'async', or run it with 'spawn')", name)
}

// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：calleeName 是报错文案的“署名笔”——Ident 取名，Field 取字段名，
// 其余空串后由调用方兜底为 "async function"。障碍是复杂被调（call 结果再调）
// 无名可取：宁空不猜，防误导。取舍：纯读 AST，不查符号表。不变量：永不 panic
// （Field nil 守卫）。排查现场：报错名空先查是否为匿名调用链。
func calleeName(e ast.Expr) string {
	switch n := e.(type) {
	case *ast.Ident:
		return n.Name
	case *ast.FieldExpr:
		if n.Field != nil {
			return n.Field.Name
		}
	}
	return ""
}

// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：checkSelect 是多路复用的“总闸”——先验 async 语境，再验非空（case/
// default 必居其一），逐 case 分流，default 独开层验体。障碍是空 select 无语义：
// 早报早返，防后段空转。取舍：default 体独层（变量不外泄）。不变量：失败仍验剩余
// case（错误不中断）。排查现场：default 变量外泄先查 push/pop 是否配对。
func (c *Checker) checkSelect(n *ast.SelectStmt) {
	if !c.inAsyncTask() {
		c.errorf(diag.SelectOutsideAsync, n.Span(), "'select' is only allowed inside 'async' functions (or the top-level chunk)")
	}
	if len(n.Cases) == 0 && n.Default == nil {
		c.errorf(diag.SelectGuardInvalid, n.Span(), "select must have at least one 'case' or 'default'")
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

// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：checkSelectCase 是单个守卫的“验钞机”——recv/send/timeout 恰居其一，
// 否则报 GuardInvalid。recv 分 local/assign/bare 三态：local 绑 (elem, boolean)
// 进新层；assign 验既有变量可赋；bare 弃值。send 验单参可赋 elem，timeout 验数字。
// 障碍是通道元素型误取：经 chanElem 统一收口（非通道即报）。候选：三态合并 vs
// 分流；选分流，绑定与赋值语义迥异。图：
//
//	guard → chanElem → 绑/验变量 → push体 → pop
//
// 不变量：每分支体独层。排查现场：recv 变量类型错先查 elem 是否为 Any 兜底。
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
		c.errorf(diag.SelectGuardInvalid, sc.Span(), "select case must have exactly one guard ('ch:recv()', 'ch:send(v)' or 'timeout(s)')")
		return
	}
	switch {
	case sc.Recv != nil:
		if len(sc.Recv.Args) != 0 {
			c.errorf(diag.SelectGuardInvalid, sc.Recv.Span(), "'ch:recv' expects no arguments, got %d", len(sc.Recv.Args))
		}
		objT := c.checkExpr(sc.Recv.Obj)
		elem := c.chanElem(objT, sc.Recv.Span())
		c.recordMemberUse(sc.Recv.Method.Span(), objT, "recv")
		if len(sc.RecvVars) > 2 {
			c.errorf(diag.SelectGuardInvalid, sc.Span(), "select receive binds at most 2 variables (v, ok)")
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
					c.errorf(diag.UndefinedVariable, v.Span(), "undefined variable '%s' (use 'case local %s = ch:recv()' to declare it)", v.Name, v.Name)
					continue
				}
				c.recordUse(v.Span(), sym)
				if sym.IsConst {
					c.errorf(diag.AssignToConst, v.Span(), "cannot assign to const variable '%s'", v.Name)
				}
				if i < len(want) && !types.IsAssignable(want[i], sym.Type, c.opt.toTypes()) {
					c.errorf(diag.TypeNotAssignable, v.Span(), "type '%s' is not assignable to type '%s'", want[i].String(), sym.Type.String())
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
			c.errorf(diag.SelectGuardInvalid, sc.Send.Span(), "'ch:send' expects exactly 1 argument, got %d", len(sc.Send.Args))
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
			c.errorf(diag.TypeNotAssignable, sc.Send.Args[0].Span(), "type '%s' is not assignable to channel element type '%s'", argT.String(), elem.String())
		}
		c.push()
		c.checkBlock(sc.Block)
		c.pop()
	case sc.Timeout != nil:
		t := c.checkExpr(sc.Timeout)
		if !isAnyLike(t) && !isNumberLike(t) {
			c.errorf(diag.SelectGuardInvalid, sc.Timeout.Span(), "select timeout expects seconds as a number, got '%s'", t.String())
		}
		c.push()
		c.checkBlock(sc.Block)
		c.pop()
	}
}

// 排查看 Span/诊断，如 `local x=1` 错圈整句。
// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：chanElem 是通道的“拆信刀”——Chan 即拆 Elem，any 即放 Any，泛型
// 约束为 Chan 则拆约束，否则报 GuardInvalid 并返 Any 续流。障碍是泛型通道
// 详见实现，排查看 Span。
// 返 Any（不断流）。不变量：永不返 nil。排查现场：select 元素型 Any 泛滥先查
// 是否走 any/泛型兜底。
func (c *Checker) chanElem(t types.Type, sp token.Span) types.Type {
	if ch, ok := t.(*types.Chan); ok {
		return ch.Elem
	}
	if isAnyLike(t) {
		return types.Any
	}
	// 解析，排查先看 Span/诊断。
	if gp, ok := t.(*types.GenericParam); ok && gp.Constraint != nil {
		if ch, ok := gp.Constraint.(*types.Chan); ok {
			return ch.Elem
		}
		return types.Any
	}
	c.errorf(diag.SelectGuardInvalid, sp, "select expects a 'chan<T>' (got '%s'); send/receive need channels", t.String())
	return types.Any
}

// chanMethodType resolves `send/recv/trySend/tryRecv/close/len/cap/closed/iter`
// 排查看 Span/诊断，如 `local x=1` 错圈整句。
// 排查看 Span/诊断，如 `local x=1` 错圈整句。
// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：chanMethodType 是通道方法的“时刻表”——阻塞三件（send/recv/iter）
// 标 IsAsync 以复用 TL3805，非阻塞详见实现，排查看 Span。
// 障碍是 iter 的双层签名（返迭代函数，内层亦 async）：漏标内层即漏报 for 循环内
// 挂起。候选：运行时判阻塞 vs 类型标；选类型标，检查与内置同源。不变量：未知名
// 返 (nil,false)。排查现场：同步调 send 未报先查 IsAsync 是否丢失。

func chanMethodType(elem types.Type, name string) (types.Type, bool) {
	switch name {
	case "send":
		return &types.Func{Params: []types.FuncParam{{Name: "value", Type: elem}}, IsAsync: true}, true
	case "recv":
		return &types.Func{Returns: []types.Type{elem, types.Boolean}, IsAsync: true}, true
	case "trySend":
		return &types.Func{Params: []types.FuncParam{{Name: "value", Type: elem}}, Returns: []types.Type{types.Boolean}}, true
	case "tryRecv":
		return &types.Func{Returns: []types.Type{elem, types.Boolean}}, true
	case "close":
		return &types.Func{}, true
	case "len", "cap":
		return &types.Func{Returns: []types.Type{types.Integer}}, true
	case "closed":
		return &types.Func{Returns: []types.Type{types.Boolean}}, true
	case "iter":
		// `for v in ch:iter() do`: iterator function yielding elements.
		return &types.Func{Returns: []types.Type{&types.Func{Returns: []types.Type{elem}, IsAsync: true}}, IsAsync: true}, true
	}
	return nil, false
}

// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：waitGroupType 是 WaitGroup 的“现成模具”——add/done 同步、wait 标
// IsAsync（须 await）。障碍是并发原语若全同步会漏挂起：wait 必须异步化。取舍：
// 硬编码 Object（无泛型）。排查现场：wait 未报 TL3805 先查 IsAsync 标记。
func waitGroupType() types.Type {
	return &types.Object{Name: "WaitGroup", Fields: map[string]types.Field{
		"add":  {Type: &types.Func{Params: []types.FuncParam{{Name: "n", Type: types.Integer}}}},
		"done": {Type: &types.Func{}},
		"wait": {Type: &types.Func{IsAsync: true}},
	}}
}

// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：mutexType 与 WaitGroup 对偶——lock 标 IsAsync（须 await），
// unlock/tryLock 同步。障碍同 wait：阻塞必须异步化。排查现场：lock 未报先查标记。
func mutexType() types.Type {
	return &types.Object{Name: "Mutex", Fields: map[string]types.Field{
		"lock":    {Type: &types.Func{IsAsync: true}},
		"unlock":  {Type: &types.Func{}},
		"tryLock": {Type: &types.Func{Returns: []types.Type{types.Boolean}}},
	}}
}
