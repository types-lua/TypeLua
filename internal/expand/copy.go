package expand

import (
	"github.com/typelua/typelua/internal/ast"
)

// Deep copy (expansion works on copies; definitions stay pristine).

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
