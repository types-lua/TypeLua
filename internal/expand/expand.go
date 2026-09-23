package expand

import (
	"fmt"
	"github.com/typelua/typelua/internal/ast"
	"github.com/typelua/typelua/internal/diag"
	"github.com/typelua/typelua/internal/token"
)

// Core expander: definition collection, statement/expression drivers.

// Diagnostics: TL37xx macro expansion.
const maxExpandDepth = 64

// macroDef is a collected macro definition.
type macroDef struct {
	name      string
	params    []string
	hasVarArg bool       // trailing `...` collects extra call arguments
	body      ast.Expr   // expression macro
	block     *ast.Block // statement macro
	isExpr    bool
	span      token.Span
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
		env.defs[md.Name.Name] = &macroDef{name: md.Name.Name, params: params, hasVarArg: md.HasVarArg, body: md.Body, block: md.Block, isExpr: md.IsExpr, span: md.Span()}
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
	case *ast.AwaitExpr:
		n.X = e.expandExpr(n.X, env)
		return n
	default:
		return x
	}
}
