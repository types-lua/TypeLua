package expand

import (
	"github.com/typelua/typelua/internal/ast"
)

// Deep copy (expansion works on copies; definitions stay pristine).
//
// 设计者视角：复印是卫生的“底片保护”——每次展开全深拷体与实参，定义永 pristine，
// 多次调用互不串。障碍是浅拷：Ident/Param/Block 若共享，改名会污染定义致二次展
// 变味；故逐层深拷（含 TypeArgs/Generics）。候选：复用 vs 复印；选复印。不变量：
// 拷后改之不影响原。排查现场：二次展开味变先查是否漏拷某层。

// ---------- deep copy (expansion works on copies; definitions stay pristine) ----------

func copyExpr(x ast.Expr) ast.Expr {
	// 设计者视角：copyExpr 按型深拷表达式，末 default 直返（无子结构者）。障碍是
	// Field/Method 的名指针亦须拷，否则改名串。排查现场：改名串定义先查此拷。
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
	case *ast.AwaitExpr:
		c := *n
		c.X = copyExpr(n.X)
		return &c
	default:
		return x
	}
}

func copyExprs(xs []ast.Expr) []ast.Expr {
	// 设计者视角：copyExprs 批量拷实参列，保代入时每 occurrence 独份（见 subst 每用
	// 必 copy）。障碍是空片：make 零长亦可。排查现场：多处代入互串先查是否复用同份。
	out := make([]ast.Expr, len(xs))
	for i, x := range xs {
		out[i] = copyExpr(x)
	}
	return out
}

func copyParams(ps []*ast.Param) []*ast.Param {
	// 设计者视角：copyParams 连 Param 与其 Name 双拷，防改名透过指针污染定义。
	// 障碍是只拷一层：Name 指针共享即串。不变量：双层新。排查现场：形参改名串定义
	// 先查此处。
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
	// 设计者视角：copyBlock 深拷语句列+return，nil 宽容。障碍是 Ret nil：须判空。
	// 不变量：nil 进 nil 出。排查现场：块共享致改名串先查语句拷。
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
	// 设计者视角：copyStmt 按语句型深拷，声明类（Type/Interface/Enum/Class）无运行时
	// 宏位直返原（见注释），MacroDef 亦直返（上层已擦）。障碍是 Select/FuncDef 的
	// 嵌套块与名指针：须递归拷。候选：反射全拷 vs 穷举；选穷举，新节点须显式决定。
	// 不变量：新节点未覆盖走 default 直返。排查现场：新语法改名串先查此处是否直返。
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
	case *ast.ImportStmt:
		c := *n
		if n.Name != nil {
			v := *n.Name
			c.Name = &v
		}
		return &c
	case *ast.DeferStmt:
		c := *n
		c.Call = copyExpr(n.Call)
		return &c
	case *ast.SpawnStmt:
		c := *n
		c.Call = copyExpr(n.Call)
		return &c
	case *ast.SelectStmt:
		c := *n
		c.Cases = make([]*ast.SelectCase, len(n.Cases))
		for i, sc := range n.Cases {
			nsc := *sc
			if sc.Recv != nil {
				m := *sc.Recv
				mm := *sc.Recv.Method
				m.Method = &mm
				m.Obj = copyExpr(sc.Recv.Obj)
				m.Args = copyExprs(sc.Recv.Args)
				nsc.Recv = &m
			}
			if sc.Send != nil {
				m := *sc.Send
				mm := *sc.Send.Method
				m.Method = &mm
				m.Obj = copyExpr(sc.Send.Obj)
				m.Args = copyExprs(sc.Send.Args)
				nsc.Send = &m
			}
			if sc.Timeout != nil {
				nsc.Timeout = copyExpr(sc.Timeout)
			}
			nsc.RecvVars = copyIdents(sc.RecvVars)
			nsc.Block = copyBlock(sc.Block)
			c.Cases[i] = &nsc
		}
		c.Default = copyBlock(n.Default)
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
	// 设计者视角：copyIdents 逐个值拷 Ident，防改名经指针串。障碍是空列。排查现场：
	// 名串先查此处。
	out := make([]*ast.Ident, len(xs))
	for i, x := range xs {
		c := *x
		out[i] = &c
	}
	return out
}
