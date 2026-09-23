package expand

import (
	"github.com/typelua/typelua/internal/ast"
)

// Hygiene: scope-aware renaming of macro-introduced bindings.

// ---------- hygiene: scope-aware renaming ----------
//
// Every local binding in the copied body gets a fresh name; references
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
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// scoped runs fn with a restored variable map afterwards.
func (r *renameCtx) scoped(fn func()) {
	saved := copyStrMap(r.vars)
	fn()
	r.vars = saved
}

func (r *renameCtx) bindVar(orig string) {
	r.vars[orig] = r.e.fresh(orig, r.avoid)
	r.avoid[r.vars[orig]] = true
}

// prescanLabels assigns one fresh name per label spelling in the body.
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
	if _, ok := r.labels[orig]; !ok {
		r.labels[orig] = r.e.fresh(orig, r.avoid)
		r.avoid[r.labels[orig]] = true
	}
}

func (r *renameCtx) prescanLabelsBlock(b *ast.Block) {
	if b == nil {
		return
	}
	for _, st := range b.Stmts {
		r.prescanLabelsStmt(st)
	}
}

func (r *renameCtx) prescanLabelsExpr(x ast.Expr) {
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
