package expand

import (
	"fmt"
	"github.com/typelua/typelua/internal/ast"
	"github.com/typelua/typelua/internal/diag"
	"github.com/typelua/typelua/internal/token"
)

// 展开器核心：定义收集、语句/表达式驱动。

// maxExpandDepth 是宏递归展开的深度上限，防止无限递归。
const maxExpandDepth = 64

// macroDef 是收集到的单个宏定义（参数、是否 vararg、体、是否表达式宏）。
// 它只在展开期存活；类型检查与发射阶段完全不知道宏存在。
type macroDef struct {
	name      string
	params    []string
	hasVarArg bool       // 尾随 `...` 收集多余调用实参
	body      ast.Expr   // 表达式宏体
	block     *ast.Block // 语句宏体
	isExpr    bool
	span      token.Span
}

// scope 是宏定义的词法作用域：父子链 + 当前块的宏定义表。
// 与 checker 的 Scope 分离：宏只看语法，不看类型。
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

// expander 是一次 ExpandFile 调用的遍历状态：诊断、fresh 名计数器、活跃宏栈（递归检测）。
type expander struct {
	diags   []diag.Diagnostic
	counter int
	stack   []string // 活跃宏名，用于递归检测
}

func (e *expander) errorf(code diag.Code, sp token.Span, format string, args ...any) {
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

// ExpandFile 展开 f 中所有宏调用，并擦除定义。
// 幂等：第二次运行找不到定义，什么都不改。
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
	// 先收集定义（支持前向引用，类似类型）。
	for _, st := range b.Stmts {
		if md, ok := st.(*ast.MacroDef); ok {
			if _, dup := env.defs[md.Name.Name]; dup {
				e.errorf(diag.DuplicateMacro, md.Span(), "duplicate macro '%s'", md.Name.Name)
				continue
			}
			if env.defs == nil {
				env.defs = map[string]*macroDef{}
			}
			params := make([]string, len(md.Params))
			for i, p := range md.Params {
				params[i] = p.Name
			}
			env.defs[md.Name.Name] = &macroDef{name: md.Name.Name, params: params, hasVarArg: md.HasVarArg, body: md.Body, block: md.Block, isExpr: md.IsExpr, span: md.Span()}
		}
	}
	var out []ast.Stmt
	for _, st := range b.Stmts {
		if _, ok := st.(*ast.MacroDef); ok {
			continue // 定义擦除
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

// expandStmtInto 展开一条语句，拼接语句宏结果。
func (e *expander) expandStmtInto(st ast.Stmt, env *scope) []ast.Stmt {
	// 语句位置的语句宏调用进行拼接。
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
	case *ast.DeferStmt:
		n.Call = e.expandExpr(n.Call, env)
		return []ast.Stmt{st}
	case *ast.SpawnStmt:
		n.Call = e.expandExpr(n.Call, env)
		return []ast.Stmt{st}
	case *ast.SelectStmt:
		for _, sc := range n.Cases {
			if sc.Recv != nil {
				sc.Recv.Obj = e.expandExpr(sc.Recv.Obj, env)
				for i, a := range sc.Recv.Args {
					sc.Recv.Args[i] = e.expandExpr(a, env)
				}
			}
			if sc.Send != nil {
				sc.Send.Obj = e.expandExpr(sc.Send.Obj, env)
				for i, a := range sc.Send.Args {
					sc.Send.Args[i] = e.expandExpr(a, env)
				}
			}
			if sc.Timeout != nil {
				sc.Timeout = e.expandExpr(sc.Timeout, env)
			}
			e.expandBlock(sc.Block, &scope{parent: env})
		}
		if n.Default != nil {
			e.expandBlock(n.Default, &scope{parent: env})
		}
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
		return nil // 擦除（防御性；通常由 expandBlock 丢弃）
	default:
		return []ast.Stmt{st}
	}
}

// expandExpr 展开表达式内的宏调用。
func (e *expander) expandExpr(x ast.Expr, env *scope) ast.Expr {
	switch n := x.(type) {
	case *ast.CallExpr:
		if id, ok := n.Func.(*ast.Ident); ok {
			if md := env.lookup(id.Name); md != nil {
				if !md.isExpr {
					e.errorf(diag.StmtMacroInExprPosition, n.Span(), "statement macro '%s' cannot be used as an expression", id.Name)
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
	case *ast.AwaitExpr:
		n.X = e.expandExpr(n.X, env)
		return n
	default:
		return x
	}
}
