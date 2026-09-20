package expand

import (
	"fmt"

	"github.com/typelua/typelua/internal/ast"
	"github.com/typelua/typelua/internal/diag"
	"github.com/typelua/typelua/internal/token"
)

// Diagnostics: TL37xx macro expansion.
const maxExpandDepth = 64

// macroDef is a collected macro definition.
type macroDef struct {
	name   string
	params []string
	body   ast.Expr   // expression macro
	block  *ast.Block // statement macro
	isExpr bool
	span   token.Span
}

type scope struct {
	parent *scope
	defs   map[string]*macroDef
}

func (s *scope) lookup(name string) *macroDef {
	for sc := s; sc != nil; sc = sc.parent {
		if d, ok := sc.defs[name]; ok {
			return d
		}
	}
	return nil
}

type expander struct {
	diags   []diag.Diagnostic
	counter int
	stack   []string // active macro names for recursion detection
}

func (e *expander) errorf(code string, sp token.Span, format string, args ...any) {
	e.diags = append(e.diags, diag.Errorf(code, sp, format, args...))
}

func (e *expander) fresh(base string, avoid map[string]bool) string {
	for {
		e.counter++
		name := fmt.Sprintf("__tl_macro_%d__%s", e.counter, base)
		if !avoid[name] {
			return name
		}
	}
}

// ExpandFile expands all macro calls in f, erasing definitions.
// Idempotent: a second run finds no definitions and changes nothing.
func ExpandFile(f *ast.File) []diag.Diagnostic {
	e := &expander{}
	if f.Block != nil {
		e.expandBlock(f.Block, &scope{})
	}
	return e.diags
}

func (e *expander) expandBlock(b *ast.Block, env *scope) {
	if b == nil {
		return
	}
	// Collect definitions first (forward references work, like types).
	for _, st := range b.Stmts {
		if md, ok := st.(*ast.MacroDef); ok {
			if _, dup := env.defs[md.Name.Name]; dup {
				e.errorf("TL3701", md.Span(), "duplicate macro '%s'", md.Name.Name)
				continue
			}
			if env.defs == nil {
				env.defs = map[string]*macroDef{}
			}
			params := make([]string, len(md.Params))
			for i, p := range md.Params {
				params[i] = p.Name
			}
			env.defs[md.Name.Name] = &macroDef{name: md.Name.Name, params: params, body: md.Body, block: md.Block, isExpr: md.IsExpr, span: md.Span()}
		}
	}
	var out []ast.Stmt
	for _, st := range b.Stmts {
		if _, ok := st.(*ast.MacroDef); ok {
			continue // definitions erase
		}
		out = append(out, e.expandStmtInto(st, env)...)
	}
	b.Stmts = out
	if b.Ret != nil {
		for i, x := range b.Ret.Exprs {
			b.Ret.Exprs[i] = e.expandExpr(x, env)
		}
	}
}

// expandStmtInto expands one statement, splicing statement-macro results.
func (e *expander) expandStmtInto(st ast.Stmt, env *scope) []ast.Stmt {
	// Statement-macro call in statement position splices.
	if es, ok := st.(*ast.ExprStmt); ok {
		if call, ok := es.E.(*ast.CallExpr); ok {
			if id, ok := call.Func.(*ast.Ident); ok {
				if md := env.lookup(id.Name); md != nil && !md.isExpr {
					return e.expandStmtMacro(md, call, env)
				}
			}
		}
	}
	switch n := st.(type) {
	case *ast.EmptyStmt, *ast.BreakStmt, *ast.GotoStmt, *ast.LabelStmt,
		*ast.TypeAliasDecl, *ast.InterfaceDecl:
		return []ast.Stmt{st}
	case *ast.EnumDecl:
		for _, m := range n.Members {
			if m.Value != nil {
				m.Value = e.expandExpr(m.Value, env)
			}
		}
		return []ast.Stmt{st}
	case *ast.ExprStmt:
		n.E = e.expandExpr(n.E, env)
		return []ast.Stmt{st}
	case *ast.AssignStmt:
		for i, t := range n.Targets {
			n.Targets[i] = e.expandExpr(t, env)
		}
		for i, v := range n.Values {
			n.Values[i] = e.expandExpr(v, env)
		}
		return []ast.Stmt{st}
	case *ast.LocalAssign:
		for i, v := range n.Values {
			n.Values[i] = e.expandExpr(v, env)
		}
		return []ast.Stmt{st}
	case *ast.LocalFuncDef:
		e.expandBlock(n.Func.Body, &scope{parent: env})
		return []ast.Stmt{st}
	case *ast.FuncDefStmt:
		e.expandBlock(n.Func.Body, &scope{parent: env})
		return []ast.Stmt{st}
	case *ast.DoBlock:
		e.expandBlock(n.Block, &scope{parent: env})
		return []ast.Stmt{st}
	case *ast.IfStmt:
		n.Cond = e.expandExpr(n.Cond, env)
		e.expandBlock(n.Then, &scope{parent: env})
		for _, ei := range n.Elseifs {
			ei.Cond = e.expandExpr(ei.Cond, env)
			e.expandBlock(ei.Block, &scope{parent: env})
		}
		if n.Else != nil {
			e.expandBlock(n.Else, &scope{parent: env})
		}
		return []ast.Stmt{st}
	case *ast.WhileStmt:
		n.Cond = e.expandExpr(n.Cond, env)
		e.expandBlock(n.Block, &scope{parent: env})
		return []ast.Stmt{st}
	case *ast.RepeatStmt:
		e.expandBlock(n.Block, &scope{parent: env})
		n.Cond = e.expandExpr(n.Cond, env)
		return []ast.Stmt{st}
	case *ast.NumericFor:
		n.Start = e.expandExpr(n.Start, env)
		n.End = e.expandExpr(n.End, env)
		if n.Step != nil {
			n.Step = e.expandExpr(n.Step, env)
		}
		e.expandBlock(n.Block, &scope{parent: env})
		return []ast.Stmt{st}
	case *ast.GenericFor:
		for i, x := range n.Exprs {
			n.Exprs[i] = e.expandExpr(x, env)
		}
		e.expandBlock(n.Block, &scope{parent: env})
		return []ast.Stmt{st}
	case *ast.ClassDecl:
		for _, m := range n.Members {
			if m.Body != nil {
				e.expandBlock(m.Body, &scope{parent: env})
			}
			if m.Init != nil {
				m.Init = e.expandExpr(m.Init, env)
			}
		}
		return []ast.Stmt{st}
	case *ast.MacroDef:
		return nil // erased (defensive; normally dropped by expandBlock)
	default:
		return []ast.Stmt{st}
	}
}

// expandExpr expands macro calls inside an expression.
func (e *expander) expandExpr(x ast.Expr, env *scope) ast.Expr {
	switch n := x.(type) {
	case *ast.CallExpr:
		if id, ok := n.Func.(*ast.Ident); ok {
			if md := env.lookup(id.Name); md != nil {
				if !md.isExpr {
					e.errorf("TL3704", n.Span(), "statement macro '%s' cannot be used as an expression", id.Name)
					for i, a := range n.Args {
						n.Args[i] = e.expandExpr(a, env)
					}
					return &ast.NilLit{S: n.Span()}
				}
				return e.expandExprMacro(md, n, env)
			}
		}
		n.Func = e.expandExpr(n.Func, env)
		for i, a := range n.Args {
			n.Args[i] = e.expandExpr(a, env)
		}
		return n
	case *ast.MethodCallExpr:
		n.Obj = e.expandExpr(n.Obj, env)
		for i, a := range n.Args {
			n.Args[i] = e.expandExpr(a, env)
		}
		return n
	case *ast.BinOpExpr:
		n.L = e.expandExpr(n.L, env)
		n.R = e.expandExpr(n.R, env)
		return n
	case *ast.UnOpExpr:
		n.X = e.expandExpr(n.X, env)
		return n
	case *ast.ParenExpr:
		n.E = e.expandExpr(n.E, env)
		return n
	case *ast.IndexExpr:
		n.Obj = e.expandExpr(n.Obj, env)
		n.Key = e.expandExpr(n.Key, env)
		return n
	case *ast.FieldExpr:
		n.Obj = e.expandExpr(n.Obj, env)
		return n
	case *ast.TableLit:
		for _, f := range n.Fields {
			if f.KeyX != nil {
				f.KeyX = e.expandExpr(f.KeyX, env)
			}
			f.Val = e.expandExpr(f.Val, env)
		}
		return n
	case *ast.FuncLit:
		e.expandBlock(n.Body, &scope{parent: env})
		return n
	case *ast.CastExpr:
		n.E = e.expandExpr(n.E, env)
		return n
	case *ast.NonNullExpr:
		n.E = e.expandExpr(n.E, env)
		return n
	default:
		return x
	}
}

// ---------- deep copy (expansion works on copies; definitions stay pristine) ----------

func copyExpr(x ast.Expr) ast.Expr {
	switch n := x.(type) {
	case *ast.Ident:
		c := *n
		return &c
	case *ast.NumberLit:
		c := *n
		return &c
	case *ast.StringLit:
		c := *n
		return &c
	case *ast.BoolLit:
		c := *n
		return &c
	case *ast.NilLit:
		c := *n
		return &c
	case *ast.VarArg:
		c := *n
		return &c
	case *ast.ParenExpr:
		c := *n
		c.E = copyExpr(n.E)
		return &c
	case *ast.TableLit:
		c := *n
		c.Fields = make([]*ast.TableField, len(n.Fields))
		for i, f := range n.Fields {
			nf := *f
			if f.Key != nil {
				k := *f.Key
				nf.Key = &k
			}
			if f.KeyX != nil {
				nf.KeyX = copyExpr(f.KeyX)
			}
			nf.Val = copyExpr(f.Val)
			c.Fields[i] = &nf
		}
		return &c
	case *ast.FuncLit:
		c := *n
		c.Params = copyParams(n.Params)
		if n.VarArgName != nil {
			v := *n.VarArgName
			c.VarArgName = &v
		}
		c.Generics = append([]*ast.GenericParam(nil), n.Generics...)
		c.Body = copyBlock(n.Body)
		return &c
	case *ast.IndexExpr:
		c := *n
		c.Obj = copyExpr(n.Obj)
		c.Key = copyExpr(n.Key)
		return &c
	case *ast.FieldExpr:
		c := *n
		f := *n.Field
		c.Field = &f
		c.Obj = copyExpr(n.Obj)
		return &c
	case *ast.CallExpr:
		c := *n
		c.Func = copyExpr(n.Func)
		c.TypeArgs = append([]ast.TypeNode(nil), n.TypeArgs...)
		c.Args = copyExprs(n.Args)
		return &c
	case *ast.MethodCallExpr:
		c := *n
		m := *n.Method
		c.Method = &m
		c.Obj = copyExpr(n.Obj)
		c.TypeArgs = append([]ast.TypeNode(nil), n.TypeArgs...)
		c.Args = copyExprs(n.Args)
		return &c
	case *ast.BinOpExpr:
		c := *n
		c.L = copyExpr(n.L)
		c.R = copyExpr(n.R)
		return &c
	case *ast.UnOpExpr:
		c := *n
		c.X = copyExpr(n.X)
		return &c
	case *ast.CastExpr:
		c := *n
		c.E = copyExpr(n.E)
		return &c
	case *ast.NonNullExpr:
		c := *n
		c.E = copyExpr(n.E)
		return &c
	default:
		return x
	}
}

func copyExprs(xs []ast.Expr) []ast.Expr {
	out := make([]ast.Expr, len(xs))
	for i, x := range xs {
		out[i] = copyExpr(x)
	}
	return out
}

func copyParams(ps []*ast.Param) []*ast.Param {
	out := make([]*ast.Param, len(ps))
	for i, p := range ps {
		np := *p
		n := *p.Name
		np.Name = &n
		out[i] = &np
	}
	return out
}

func copyBlock(b *ast.Block) *ast.Block {
	if b == nil {
		return nil
	}
	c := *b
	c.Stmts = make([]ast.Stmt, len(b.Stmts))
	for i, st := range b.Stmts {
		c.Stmts[i] = copyStmt(st)
	}
	if b.Ret != nil {
		r := *b.Ret
		r.Exprs = copyExprs(b.Ret.Exprs)
		c.Ret = &r
	}
	return &c
}

func copyStmt(st ast.Stmt) ast.Stmt {
	switch n := st.(type) {
	case *ast.EmptyStmt:
		c := *n
		return &c
	case *ast.BreakStmt:
		c := *n
		return &c
	case *ast.GotoStmt:
		c := *n
		l := *n.Label
		c.Label = &l
		return &c
	case *ast.LabelStmt:
		c := *n
		l := *n.Name
		c.Name = &l
		return &c
	case *ast.ExprStmt:
		c := *n
		c.E = copyExpr(n.E)
		return &c
	case *ast.AssignStmt:
		c := *n
		c.Targets = copyExprs(n.Targets)
		c.Values = copyExprs(n.Values)
		return &c
	case *ast.LocalAssign:
		c := *n
		c.Names = copyIdents(n.Names)
		c.Values = copyExprs(n.Values)
		return &c
	case *ast.LocalFuncDef:
		c := *n
		l := *n.Name
		c.Name = &l
		c.Func = copyExpr(n.Func).(*ast.FuncLit)
		return &c
	case *ast.FuncDefStmt:
		c := *n
		fn := *n.Name
		fn.Prefix = copyIdents(n.Name.Prefix)
		if n.Name.Method != nil {
			m := *n.Name.Method
			fn.Method = &m
		}
		c.Name = &fn
		c.Func = copyExpr(n.Func).(*ast.FuncLit)
		return &c
	case *ast.DoBlock:
		c := *n
		c.Block = copyBlock(n.Block)
		return &c
	case *ast.IfStmt:
		c := *n
		c.Cond = copyExpr(n.Cond)
		c.Then = copyBlock(n.Then)
		c.Elseifs = make([]*ast.ElseifClause, len(n.Elseifs))
		for i, ei := range n.Elseifs {
			ne := *ei
			ne.Cond = copyExpr(ei.Cond)
			ne.Block = copyBlock(ei.Block)
			c.Elseifs[i] = &ne
		}
		c.Else = copyBlock(n.Else)
		return &c
	case *ast.WhileStmt:
		c := *n
		c.Cond = copyExpr(n.Cond)
		c.Block = copyBlock(n.Block)
		return &c
	case *ast.RepeatStmt:
		c := *n
		c.Block = copyBlock(n.Block)
		c.Cond = copyExpr(n.Cond)
		return &c
	case *ast.NumericFor:
		c := *n
		v := *n.Var
		c.Var = &v
		c.Start = copyExpr(n.Start)
		c.End = copyExpr(n.End)
		if n.Step != nil {
			c.Step = copyExpr(n.Step)
		}
		c.Block = copyBlock(n.Block)
		return &c
	case *ast.GenericFor:
		c := *n
		c.Names = copyIdents(n.Names)
		c.Exprs = copyExprs(n.Exprs)
		c.Block = copyBlock(n.Block)
		return &c
	case *ast.TypeAliasDecl, *ast.InterfaceDecl, *ast.EnumDecl, *ast.ClassDecl:
		// Declarations carry no macro-expandable runtime code.
		return st
	case *ast.MacroDef:
		return st
	default:
		return st
	}
}

func copyIdents(xs []*ast.Ident) []*ast.Ident {
	out := make([]*ast.Ident, len(xs))
	for i, x := range xs {
		c := *x
		out[i] = &c
	}
	return out
}

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
	}
}

// ---------- substitution (scope-aware: shadowed params are left alone) ----------

type substCtx struct {
	args   map[string]ast.Expr // param name -> arg AST (already expanded)
	shadow map[string]int      // param names currently shadowed (depth count)
	trail  []string            // push order for block-scoped unwinding
}

func (s *substCtx) pushShadow(name string) {
	s.shadow[name]++
	s.trail = append(s.trail, name)
}

func (s *substCtx) checkpoint() int { return len(s.trail) }

func (s *substCtx) unwindTo(cp int) {
	for len(s.trail) > cp {
		name := s.trail[len(s.trail)-1]
		s.trail = s.trail[:len(s.trail)-1]
		s.shadow[name]--
		if s.shadow[name] <= 0 {
			delete(s.shadow, name)
		}
	}
}

func (s *substCtx) isShadowed(name string) bool { return s.shadow[name] > 0 }

// substBlock substitutes a block, unwinding block-local shadows at the end
// so later siblings (and outer scopes) are unaffected.
func substBlock(b *ast.Block, s *substCtx) {
	if b == nil {
		return
	}
	cp := s.checkpoint()
	for _, stmt := range b.Stmts {
		substStmt(stmt, s)
	}
	substRet(b, s)
	s.unwindTo(cp)
}

// substExpr replaces unshadowed param references with per-occurrence copies
// of the argument ASTs.
func substExpr(x ast.Expr, s *substCtx) ast.Expr {
	switch n := x.(type) {
	case *ast.Ident:
		if s.isShadowed(n.Name) {
			return n
		}
		if a, ok := s.args[n.Name]; ok {
			return copyExpr(a)
		}
		return n
	case *ast.FuncLit:
		cp := s.checkpoint()
		for _, p := range n.Params {
			s.pushShadow(p.Name.Name)
		}
		if n.VarArgName != nil {
			s.pushShadow(n.VarArgName.Name)
		}
		substBlock(n.Body, s)
		s.unwindTo(cp)
		return n
	case *ast.CallExpr:
		n.Func = substExpr(n.Func, s)
		for i, a := range n.Args {
			n.Args[i] = substExpr(a, s)
		}
		return n
	case *ast.MethodCallExpr:
		n.Obj = substExpr(n.Obj, s)
		for i, a := range n.Args {
			n.Args[i] = substExpr(a, s)
		}
		return n
	case *ast.BinOpExpr:
		n.L = substExpr(n.L, s)
		n.R = substExpr(n.R, s)
		return n
	case *ast.UnOpExpr:
		n.X = substExpr(n.X, s)
		return n
	case *ast.ParenExpr:
		n.E = substExpr(n.E, s)
		return n
	case *ast.IndexExpr:
		n.Obj = substExpr(n.Obj, s)
		n.Key = substExpr(n.Key, s)
		return n
	case *ast.FieldExpr:
		n.Obj = substExpr(n.Obj, s)
		return n
	case *ast.TableLit:
		for _, f := range n.Fields {
			if f.KeyX != nil {
				f.KeyX = substExpr(f.KeyX, s)
			}
			f.Val = substExpr(f.Val, s)
		}
		return n
	case *ast.CastExpr:
		n.E = substExpr(n.E, s)
		return n
	case *ast.NonNullExpr:
		n.E = substExpr(n.E, s)
		return n
	default:
		return x
	}
}

func substStmt(st ast.Stmt, s *substCtx) {
	switch n := st.(type) {
	case *ast.LocalAssign:
		for i, v := range n.Values {
			n.Values[i] = substExpr(v, s)
		}
		// Names bind after values evaluate and stay visible to later
		// siblings; the enclosing substBlock unwinds at block end.
		for _, id := range n.Names {
			s.pushShadow(id.Name)
		}
	case *ast.LocalFuncDef:
		// Body sees the name (recursion); params shadow inside only.
		s.pushShadow(n.Name.Name)
		cp := s.checkpoint()
		for _, p := range n.Func.Params {
			s.pushShadow(p.Name.Name)
		}
		if n.Func.VarArgName != nil {
			s.pushShadow(n.Func.VarArgName.Name)
		}
		substBlock(n.Func.Body, s)
		s.unwindTo(cp)
	case *ast.FuncDefStmt:
		cp := s.checkpoint()
		for _, p := range n.Func.Params {
			s.pushShadow(p.Name.Name)
		}
		if n.Func.VarArgName != nil {
			s.pushShadow(n.Func.VarArgName.Name)
		}
		substBlock(n.Func.Body, s)
		s.unwindTo(cp)
	case *ast.ExprStmt:
		n.E = substExpr(n.E, s)
	case *ast.AssignStmt:
		for i, t := range n.Targets {
			n.Targets[i] = substExpr(t, s)
		}
		for i, v := range n.Values {
			n.Values[i] = substExpr(v, s)
		}
	case *ast.DoBlock:
		substBlock(n.Block, s)
	case *ast.IfStmt:
		n.Cond = substExpr(n.Cond, s)
		substBlock(n.Then, s)
		for _, ei := range n.Elseifs {
			ei.Cond = substExpr(ei.Cond, s)
			substBlock(ei.Block, s)
		}
		if n.Else != nil {
			substBlock(n.Else, s)
		}
	case *ast.WhileStmt:
		n.Cond = substExpr(n.Cond, s)
		substBlock(n.Block, s)
	case *ast.RepeatStmt:
		// repeat..until condition sees loop locals: substitute the body
		// first, then the condition, then unwind both together.
		cp := s.checkpoint()
		for _, stmt := range n.Block.Stmts {
			substStmt(stmt, s)
		}
		substRet(n.Block, s)
		n.Cond = substExpr(n.Cond, s)
		s.unwindTo(cp)
	case *ast.NumericFor:
		n.Start = substExpr(n.Start, s)
		n.End = substExpr(n.End, s)
		if n.Step != nil {
			n.Step = substExpr(n.Step, s)
		}
		cp := s.checkpoint()
		s.pushShadow(n.Var.Name)
		substBlock(n.Block, s)
		s.unwindTo(cp)
	case *ast.GenericFor:
		for i, x := range n.Exprs {
			n.Exprs[i] = substExpr(x, s)
		}
		cp := s.checkpoint()
		for _, id := range n.Names {
			s.pushShadow(id.Name)
		}
		substBlock(n.Block, s)
		s.unwindTo(cp)
	case *ast.GotoStmt, *ast.LabelStmt, *ast.EmptyStmt, *ast.BreakStmt,
		*ast.TypeAliasDecl, *ast.InterfaceDecl:
		// No value positions (labels handled by the rename pass).
	case *ast.EnumDecl:
		for _, m := range n.Members {
			if m.Value != nil {
				m.Value = substExpr(m.Value, s)
			}
		}
	case *ast.ClassDecl:
		for _, m := range n.Members {
			if m.Body != nil {
				cp := s.checkpoint()
				for _, p := range m.Params {
					s.pushShadow(p.Name.Name)
				}
				if m.VarArgName != nil {
					s.pushShadow(m.VarArgName.Name)
				}
				substBlock(m.Body, s)
				s.unwindTo(cp)
			}
			if m.Init != nil {
				m.Init = substExpr(m.Init, s)
			}
		}
	}
}

func substRet(b *ast.Block, s *substCtx) {
	if b.Ret != nil {
		for i, e := range b.Ret.Exprs {
			b.Ret.Exprs[i] = substExpr(e, s)
		}
	}
}

// ---------- macro expansion core ----------

// checkArity validates argument count, padding missing with nil and
// dropping extras only after reporting (best-effort runnable output).
func (e *expander) checkArity(md *macroDef, call token.Span, nargs int) []int {
	if nargs == len(md.params) {
		idx := make([]int, nargs)
		for i := range idx {
			idx[i] = i
		}
		return idx
	}
	e.errorf("TL3702", call, "macro '%s' expects %d argument(s), got %d", md.name, len(md.params), nargs)
	return nil
}

// enter guards recursion: same macro re-entered, or depth exceeded.
func (e *expander) enter(name string, sp token.Span) bool {
	for _, n := range e.stack {
		if n == name {
			e.errorf("TL3703", sp, "recursive macro expansion of '%s'", name)
			return false
		}
	}
	if len(e.stack) >= maxExpandDepth {
		e.errorf("TL3703", sp, "macro expansion too deep (possible recursion via '%s')", name)
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
	body = substExpr(body, sub)
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
	substBlock(blk, sub)
	// Spliced placeholders must yield real statements (calls); anything
	// else would emit invalid Lua.
	var valid []ast.Stmt
	for _, st := range blk.Stmts {
		if es, ok := st.(*ast.ExprStmt); ok {
			switch es.E.(type) {
			case *ast.CallExpr, *ast.MethodCallExpr:
			default:
				e.errorf("TL3705", es.Span(), "macro '%s' expanded to an invalid statement", md.name)
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
