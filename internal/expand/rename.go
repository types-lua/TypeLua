package expand

import (
	"github.com/typelua/typelua/internal/ast"
)

// Hygiene: scope-aware renaming of macro-introduced bindings.
//
// 设计者视角：卫生改名是“全员换证”——体拷贝内每个局部绑 fresh 新名，引用经
// save/restore 影子栈消解，label 另预扫（goto 可前跳）。障碍是遮参：影子绑与
// 其域引用须同新名；跨函数同名 label 永不交互故可统改；fresh 须避实参全拼。
// 图：prescan(labels)→rename(绑+引)→subst(参换实)。

// ---------- hygiene: scope-aware renaming ----------
//
// 复制体中的每个局部绑定都获得新名字；引用
// resolve through save/restore maps, so shadowing (including shadowing a
// macro parameter) stays coherent: the shadowing binding and exactly its
// scope's references share one fresh name.
// Labels are pre-scanned per body (gotos may precede their label);
// distinct functions' same-named labels never interact, so uniform
// renaming is sound for them.
// Fresh names avoid every caller-argument spelling, so capture is
// impossible short of adversarial hand-written identical names.

type renameCtx struct {
	e      *expander
	vars   map[string]string
	labels map[string]string
	avoid  map[string]bool
}

func copyStrMap(m map[string]string) map[string]string {
	// 设计者视角：copyStrMap 快照 vars 供 scoped 恢复，防兄弟块串名。障碍是 map
	// 引用共享：不拷即改穿。排查现场：兄弟块名串先查 scoped 快照。
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// scoped runs fn with a restored variable map afterwards.
//
// 设计者视角：scoped 给闭包拍 vars 快照、跑后恢复，保 for/select/函数形参域不泄。
// 障碍是忘恢复即兄弟串名。候选：checkpoint 计数 vs 全拷；选全拷，简单。不变量：
// 出口 vars 与入口等。排查现场：域外见 fresh 名先查 scoped。
func (r *renameCtx) scoped(fn func()) {
	saved := copyStrMap(r.vars)
	fn()
	r.vars = saved
}

func (r *renameCtx) bindVar(orig string) {
	// 设计者视角：bindVar 给旧名发 fresh 新证并入 avoid（后绑避前 fresh）。障碍是
	// 两绑同原：后者覆前者恰为遮蔽。排查现场：两同名绑同新名先查 avoid 更新。
	r.vars[orig] = r.e.fresh(orig, r.avoid)
	r.avoid[r.vars[orig]] = true
}

// prescanLabels assigns one fresh name per label spelling in the body.
//
// 设计者视角：label 须预扫——goto 可在 label 前，故一遍綁名、二遍改写，保前后跳
// 同名。障碍是函数边界：不同函数同名 label 互不干扰，此处全体綁亦 sound（见注释）。
// 候选：分函数域 vs 统一；选统一，简单且 sound。排查现场：label 跳错先查 prescan
// 是否覆盖该体。
func (r *renameCtx) prescanLabelsStmt(st ast.Stmt) {
	switch n := st.(type) {
	case *ast.GotoStmt:
		r.bindLabel(n.Label.Name)
	case *ast.LabelStmt:
		r.bindLabel(n.Name.Name)
	case *ast.DoBlock:
		r.prescanLabelsBlock(n.Block)
	case *ast.IfStmt:
		for _, s := range n.Then.Stmts {
			r.prescanLabelsStmt(s)
		}
		for _, ei := range n.Elseifs {
			for _, s := range ei.Block.Stmts {
				r.prescanLabelsStmt(s)
			}
		}
		if n.Else != nil {
			for _, s := range n.Else.Stmts {
				r.prescanLabelsStmt(s)
			}
		}
	case *ast.WhileStmt, *ast.RepeatStmt, *ast.NumericFor, *ast.GenericFor:
		for _, s := range childStmts(st) {
			r.prescanLabelsStmt(s)
		}
	case *ast.SelectStmt:
		for _, sc := range n.Cases {
			for _, s := range sc.Block.Stmts {
				r.prescanLabelsStmt(s)
			}
		}
		if n.Default != nil {
			for _, s := range n.Default.Stmts {
				r.prescanLabelsStmt(s)
			}
		}
	case *ast.LocalFuncDef:
		r.prescanLabelsBlock(n.Func.Body)
	case *ast.FuncDefStmt:
		r.prescanLabelsBlock(n.Func.Body)
	case *ast.ClassDecl:
		for _, m := range n.Members {
			r.prescanLabelsBlock(m.Body)
		}
	}
}

func (r *renameCtx) bindLabel(orig string) {
	// 设计者视角：bindLabel 一拼一名，重复綁复用首名，保 goto/label 同新。障碍是
	// 多 label 同拼：须同名。排查现场：跳接错位先查复用。
	if _, ok := r.labels[orig]; !ok {
		r.labels[orig] = r.e.fresh(orig, r.avoid)
		r.avoid[r.labels[orig]] = true
	}
}

func (r *renameCtx) prescanLabelsBlock(b *ast.Block) {
	// 设计者视角：块级预扫逐句，nil 宽容（空体）。障碍是 Ret 无 label：只扫 Stmts。
	// 排查现场：块内 label 漏改先查此循环。
	if b == nil {
		return
	}
	for _, st := range b.Stmts {
		r.prescanLabelsStmt(st)
	}
}

func (r *renameCtx) prescanLabelsExpr(x ast.Expr) {
	// 设计者视角：表达式内 FuncLit 体可藏 label/goto，须递归预扫；余者下钻。
	// 障碍是表达式无 label 位：只找 FuncLit。排查现场：闭包内跳错先查此下钻。
	switch n := x.(type) {
	case *ast.FuncLit:
		r.prescanLabelsBlock(n.Body)
	case *ast.CallExpr:
		r.prescanLabelsExpr(n.Func)
		for _, a := range n.Args {
			r.prescanLabelsExpr(a)
		}
	case *ast.MethodCallExpr:
		r.prescanLabelsExpr(n.Obj)
		for _, a := range n.Args {
			r.prescanLabelsExpr(a)
		}
	case *ast.BinOpExpr:
		r.prescanLabelsExpr(n.L)
		r.prescanLabelsExpr(n.R)
	case *ast.UnOpExpr:
		r.prescanLabelsExpr(n.X)
	case *ast.ParenExpr:
		r.prescanLabelsExpr(n.E)
	case *ast.IndexExpr:
		r.prescanLabelsExpr(n.Obj)
		r.prescanLabelsExpr(n.Key)
	case *ast.FieldExpr:
		r.prescanLabelsExpr(n.Obj)
	case *ast.TableLit:
		for _, f := range n.Fields {
			if f.KeyX != nil {
				r.prescanLabelsExpr(f.KeyX)
			}
			r.prescanLabelsExpr(f.Val)
		}
	case *ast.CastExpr:
		r.prescanLabelsExpr(n.E)
	case *ast.NonNullExpr:
		r.prescanLabelsExpr(n.E)
	}
}

func childStmts(st ast.Stmt) []ast.Stmt {
	// 设计者视角：childStmts 给循环三兄弟取体供预扫，余者 nil。障碍是 If/Select 体
	// 多：上层另行处理，此处只 cover 单体循环。排查现场：循环内 label 漏先查此处。
	switch n := st.(type) {
	case *ast.WhileStmt:
		return n.Block.Stmts
	case *ast.RepeatStmt:
		return n.Block.Stmts
	case *ast.NumericFor:
		return n.Block.Stmts
	case *ast.GenericFor:
		return n.Block.Stmts
	}
	return nil
}

func (r *renameCtx) renameBlock(b *ast.Block) {
	// 设计者视角：renameBlock 快照 vars、逐句改名、末恢复，保兄弟块隔离；Ret 亦改。
	// 障碍是块内绑须对后句可见、对块外不可见：快照+恢复恰之。排查现场：块外见 fresh
	// 先查恢复。
	if b == nil {
		return
	}
	saved := copyStrMap(r.vars)
	for _, st := range b.Stmts {
		r.renameStmt(st)
	}
	if b.Ret != nil {
		for _, x := range b.Ret.Exprs {
			r.renameExpr(x)
		}
	}
	r.vars = saved
}

func (r *renameCtx) renameFuncLit(fn *ast.FuncLit) {
	// 设计者视角：函数字面量 scoped 綁形参（含具名 rest）再改体，保参域干净。
	// 障碍是 ... 名亦须綁（见 VarArgName）。排查现场：参名残留先查此綁。
	r.scoped(func() {
		for _, p := range fn.Params {
			r.bindVar(p.Name.Name)
			p.Name.Name = r.vars[p.Name.Name]
		}
		if fn.VarArgName != nil {
			r.bindVar(fn.VarArgName.Name)
			fn.VarArgName.Name = r.vars[fn.VarArgName.Name]
		}
		r.renameBlock(fn.Body)
	})
}

func (r *renameCtx) renameStmt(st ast.Stmt) {
	// 设计者视角：renameStmt 先值后名（LocalAssign 值先改、名后綁），函数名先綁
	// （递归），for/select 量 scoped 綁，goto/label 查表换。障碍是 Import 绑名亦
	// 宏局须綁。候选：穷举 vs 反射；选穷举。排查现场：新语句漏改先查分支。
	switch n := st.(type) {
	case *ast.LocalAssign:
		for _, v := range n.Values {
			r.renameExpr(v)
		}
		for _, id := range n.Names {
			r.bindVar(id.Name)
			id.Name = r.vars[id.Name]
		}
	case *ast.ImportStmt:
		// The bound name is macro-local like any local.
		if n.Name != nil {
			r.bindVar(n.Name.Name)
			n.Name.Name = r.vars[n.Name.Name]
		}
	case *ast.DeferStmt:
		// The deferred call resolves names like any other call.
		r.renameExpr(n.Call)
	case *ast.SpawnStmt:
		r.renameExpr(n.Call)
	case *ast.SelectStmt:
		for _, sc := range n.Cases {
			if sc.Recv != nil {
				r.renameExpr(sc.Recv.Obj)
				for _, a := range sc.Recv.Args {
					r.renameExpr(a)
				}
			}
			if sc.Send != nil {
				r.renameExpr(sc.Send.Obj)
				for _, a := range sc.Send.Args {
					r.renameExpr(a)
				}
			}
			if sc.Timeout != nil {
				r.renameExpr(sc.Timeout)
			}
			r.scoped(func() {
				if sc.RecvLocal {
					for _, v := range sc.RecvVars {
						r.bindVar(v.Name)
						v.Name = r.vars[v.Name]
					}
				}
				r.renameBlock(sc.Block)
			})
		}
		if n.Default != nil {
			r.renameBlock(n.Default)
		}
	case *ast.LocalFuncDef:
		r.bindVar(n.Name.Name)
		n.Name.Name = r.vars[n.Name.Name]
		r.renameFuncLit(n.Func)
	case *ast.FuncDefStmt:
		r.renameFuncLit(n.Func)
	case *ast.ExprStmt:
		r.renameExpr(n.E)
	case *ast.AssignStmt:
		for _, t := range n.Targets {
			r.renameExpr(t)
		}
		for _, v := range n.Values {
			r.renameExpr(v)
		}
	case *ast.DoBlock:
		r.renameBlock(n.Block)
	case *ast.IfStmt:
		r.renameExpr(n.Cond)
		r.renameBlock(n.Then)
		for _, ei := range n.Elseifs {
			r.renameExpr(ei.Cond)
			r.renameBlock(ei.Block)
		}
		if n.Else != nil {
			r.renameBlock(n.Else)
		}
	case *ast.WhileStmt:
		r.renameExpr(n.Cond)
		r.renameBlock(n.Block)
	case *ast.RepeatStmt:
		r.renameBlock(n.Block)
		r.renameExpr(n.Cond)
	case *ast.NumericFor:
		r.renameExpr(n.Start)
		r.renameExpr(n.End)
		if n.Step != nil {
			r.renameExpr(n.Step)
		}
		r.scoped(func() {
			r.bindVar(n.Var.Name)
			n.Var.Name = r.vars[n.Var.Name]
			r.renameBlock(n.Block)
		})
	case *ast.GenericFor:
		for _, x := range n.Exprs {
			r.renameExpr(x)
		}
		r.scoped(func() {
			for _, id := range n.Names {
				r.bindVar(id.Name)
				id.Name = r.vars[id.Name]
			}
			r.renameBlock(n.Block)
		})
	case *ast.GotoStmt:
		if v, ok := r.labels[n.Label.Name]; ok {
			n.Label.Name = v
		}
	case *ast.LabelStmt:
		if v, ok := r.labels[n.Name.Name]; ok {
			n.Name.Name = v
		}
	case *ast.ClassDecl:
		for _, m := range n.Members {
			if m.Body != nil {
				r.scoped(func() {
					for _, p := range m.Params {
						r.bindVar(p.Name.Name)
						p.Name.Name = r.vars[p.Name.Name]
					}
					if m.VarArgName != nil {
						r.bindVar(m.VarArgName.Name)
						m.VarArgName.Name = r.vars[m.VarArgName.Name]
					}
					r.renameBlock(m.Body)
				})
			}
			if m.Init != nil {
				r.renameExpr(m.Init)
			}
		}
	case *ast.EnumDecl:
		for _, m := range n.Members {
			if m.Value != nil {
				r.renameExpr(m.Value)
			}
		}
	}
}

// renameExpr resolves references through the current maps. Note it takes
// only the expression: label positions never appear inside expressions.
//
// 设计者视角：renameExpr 只换 Ident（查 vars 表），FuncLit 开域，余者下钻；label
// 永不在表达式内故无分支。障碍是 Field 名（`a.foo` 的 foo）非变量须留。候选：全换
// vs 仅 Ident；选后者。不变量：Field/Method 名不动。排查现场：字段名被换先查分支。
func (r *renameCtx) renameExpr(x ast.Expr) {
	switch n := x.(type) {
	case *ast.Ident:
		if v, ok := r.vars[n.Name]; ok {
			n.Name = v
		}
	case *ast.FuncLit:
		r.renameFuncLit(n)
	case *ast.CallExpr:
		r.renameExpr(n.Func)
		for _, a := range n.Args {
			r.renameExpr(a)
		}
	case *ast.MethodCallExpr:
		r.renameExpr(n.Obj)
		for _, a := range n.Args {
			r.renameExpr(a)
		}
	case *ast.BinOpExpr:
		r.renameExpr(n.L)
		r.renameExpr(n.R)
	case *ast.UnOpExpr:
		r.renameExpr(n.X)
	case *ast.ParenExpr:
		r.renameExpr(n.E)
	case *ast.IndexExpr:
		r.renameExpr(n.Obj)
		r.renameExpr(n.Key)
	case *ast.FieldExpr:
		r.renameExpr(n.Obj)
	case *ast.TableLit:
		for _, f := range n.Fields {
			if f.KeyX != nil {
				r.renameExpr(f.KeyX)
			}
			r.renameExpr(f.Val)
		}
	case *ast.CastExpr:
		r.renameExpr(n.E)
	case *ast.NonNullExpr:
		r.renameExpr(n.E)
	case *ast.AwaitExpr:
		r.renameExpr(n.X)
	}
}

// collectIdents gathers every identifier spelling in an expression
// (over-approximate and safe: extra avoid-names only bump the counter).
//
// 设计者视角：collectIdents 为 avoid 采“全拼过近似”——宁多采（counter 多跳）不漏采
// （捕获）。障碍是 FuncLit 体与 Field 名：体须下钻采，字段名亦采（保 fresh 避之）。
// 候选：精确自由变量 vs 过近似；选后者，简单安全。不变量：多采无害、少采致命。
// 排查现场：捕获先查此覆盖。
func collectIdents(out map[string]bool, x ast.Expr) {
	switch n := x.(type) {
	case *ast.Ident:
		out[n.Name] = true
	case *ast.CallExpr:
		collectIdents(out, n.Func)
		for _, a := range n.Args {
			collectIdents(out, a)
		}
	case *ast.MethodCallExpr:
		collectIdents(out, n.Obj)
		out[n.Method.Name] = true
		for _, a := range n.Args {
			collectIdents(out, a)
		}
	case *ast.BinOpExpr:
		collectIdents(out, n.L)
		collectIdents(out, n.R)
	case *ast.UnOpExpr:
		collectIdents(out, n.X)
	case *ast.ParenExpr:
		collectIdents(out, n.E)
	case *ast.IndexExpr:
		collectIdents(out, n.Obj)
		collectIdents(out, n.Key)
	case *ast.FieldExpr:
		collectIdents(out, n.Obj)
		out[n.Field.Name] = true
	case *ast.TableLit:
		for _, f := range n.Fields {
			if f.Key != nil {
				out[f.Key.Name] = true
			}
			if f.KeyX != nil {
				collectIdents(out, f.KeyX)
			}
			collectIdents(out, f.Val)
		}
	case *ast.FuncLit:
		for _, p := range n.Params {
			out[p.Name.Name] = true
		}
		for _, s := range n.Body.Stmts {
			collectStmtIdents(out, s)
		}
		if n.Body.Ret != nil {
			for _, e := range n.Body.Ret.Exprs {
				collectIdents(out, e)
			}
		}
	case *ast.CastExpr:
		collectIdents(out, n.E)
	case *ast.NonNullExpr:
		collectIdents(out, n.E)
	case *ast.AwaitExpr:
		collectIdents(out, n.X)
	}
}

func collectStmtIdents(out map[string]bool, st ast.Stmt) {
	// 设计者视角：语句版全拼采集，与 collectIdents 对仗，供实参 avoid。障碍是新语句
	// 须同步加分支，否则 fresh 撞名。候选：复用 rename 走 vs 独立；选独立采拼。
	// 排查现场：实参名被捕获先查此分支。
	switch n := st.(type) {
	case *ast.LocalAssign:
		for _, id := range n.Names {
			out[id.Name] = true
		}
		for _, v := range n.Values {
			collectIdents(out, v)
		}
	case *ast.LocalFuncDef:
		out[n.Name.Name] = true
		collectIdents(out, n.Func)
	case *ast.FuncDefStmt:
		collectIdents(out, n.Func)
	case *ast.ExprStmt:
		collectIdents(out, n.E)
	case *ast.AssignStmt:
		for _, t := range n.Targets {
			collectIdents(out, t)
		}
		for _, v := range n.Values {
			collectIdents(out, v)
		}
	case *ast.GotoStmt:
		out[n.Label.Name] = true
	case *ast.LabelStmt:
		out[n.Name.Name] = true
	case *ast.EnumDecl:
		for _, m := range n.Members {
			if m.Value != nil {
				collectIdents(out, m.Value)
			}
		}
	case *ast.DoBlock:
		for _, s := range n.Block.Stmts {
			collectStmtIdents(out, s)
		}
	case *ast.IfStmt:
		collectIdents(out, n.Cond)
		for _, s := range n.Then.Stmts {
			collectStmtIdents(out, s)
		}
		for _, ei := range n.Elseifs {
			collectIdents(out, ei.Cond)
			for _, s := range ei.Block.Stmts {
				collectStmtIdents(out, s)
			}
		}
		if n.Else != nil {
			for _, s := range n.Else.Stmts {
				collectStmtIdents(out, s)
			}
		}
	case *ast.WhileStmt:
		collectIdents(out, n.Cond)
		for _, s := range n.Block.Stmts {
			collectStmtIdents(out, s)
		}
	case *ast.RepeatStmt:
		for _, s := range n.Block.Stmts {
			collectStmtIdents(out, s)
		}
		collectIdents(out, n.Cond)
	case *ast.NumericFor:
		out[n.Var.Name] = true
		collectIdents(out, n.Start)
		collectIdents(out, n.End)
		if n.Step != nil {
			collectIdents(out, n.Step)
		}
		for _, s := range n.Block.Stmts {
			collectStmtIdents(out, s)
		}
	case *ast.GenericFor:
		for _, id := range n.Names {
			out[id.Name] = true
		}
		for _, x := range n.Exprs {
			collectIdents(out, x)
		}
		for _, s := range n.Block.Stmts {
			collectStmtIdents(out, s)
		}
	case *ast.ClassDecl:
		for _, m := range n.Members {
			if m.Body != nil {
				for _, s := range m.Body.Stmts {
					collectStmtIdents(out, s)
				}
			}
			if m.Init != nil {
				collectIdents(out, m.Init)
			}
		}
	case *ast.SpawnStmt:
		collectIdents(out, n.Call)
	case *ast.SelectStmt:
		for _, sc := range n.Cases {
			if sc.Recv != nil {
				collectIdents(out, sc.Recv.Obj)
				for _, a := range sc.Recv.Args {
					collectIdents(out, a)
				}
			}
			if sc.Send != nil {
				collectIdents(out, sc.Send.Obj)
				for _, a := range sc.Send.Args {
					collectIdents(out, a)
				}
			}
			if sc.Timeout != nil {
				collectIdents(out, sc.Timeout)
			}
			for _, v := range sc.RecvVars {
				out[v.Name] = true
			}
			for _, s := range sc.Block.Stmts {
				collectStmtIdents(out, s)
			}
		}
		if n.Default != nil {
			for _, s := range n.Default.Stmts {
				collectStmtIdents(out, s)
			}
		}
	}
}
