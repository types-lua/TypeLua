package checker

import (
	"github.com/typelua/typelua/internal/ast"
	"github.com/typelua/typelua/internal/types"
)

// Cross-file require support: the driver (compiler.Program) resolves
// dotted require paths to files, checks them in dependency order, and
// feeds each file the module types of its requirements through
// Options.RequireTypes. Unresolved requirements stay `any`.

// isShadowedRequire reports whether a local shadows the require builtin.
func isShadowedRequire(c *Checker) bool {
	if s := c.cur().lookupVar("require"); s != nil && !s.IsBuiltin {
		return true
	}
	return false
}

// unquoteModulePath strips one layer of Lua quotes from a require path
// literal (best-effort; escapes are rare in module paths).
func unquoteModulePath(sl *ast.StringLit) (string, bool) {
	if sl == nil {
		return "", false
	}
	lit := sl.Literal
	if len(lit) < 2 {
		return "", false
	}
	q := lit[0]
	if (q != '"' && q != '\'') || lit[len(lit)-1] != q {
		return "", false
	}
	name, _ := parseStringLiteral(lit)
	return name, true
}

// requirePathType resolves a require path literal through RequireTypes
// (Any when unresolved or shadowed).
func (c *Checker) requirePathType(lit *ast.StringLit) types.Type {
	if isShadowedRequire(c) || len(c.opt.RequireTypes) == 0 {
		return types.Any
	}
	name, ok := unquoteModulePath(lit)
	if !ok {
		return types.Any
	}
	if t, ok := c.opt.RequireTypes[name]; ok && t != nil {
		return t
	}
	return types.Any
}

// RequiredModules lists dotted require paths in a file, first-seen order:
// `require("lit")` calls (any function position, shadowed or not) and
// import statement paths. Best-effort: unresolvable spellings are skipped,
// never reported (checking stays total).
func RequiredModules(f *ast.File) []string {
	w := &requireWalker{seen: map[string]bool{}}
	if f.Block != nil {
		w.block(f.Block)
	}
	return w.out
}

type requireWalker struct {
	seen map[string]bool
	out  []string
}

func (w *requireWalker) add(sl *ast.StringLit) {
	name, ok := unquoteModulePath(sl)
	if !ok || w.seen[name] {
		return
	}
	w.seen[name] = true
	w.out = append(w.out, name)
}

func (w *requireWalker) block(b *ast.Block) {
	if b == nil {
		return
	}
	for _, st := range b.Stmts {
		w.stmt(st)
	}
	if b.Ret != nil {
		for _, e := range b.Ret.Exprs {
			w.expr(e)
		}
	}
}

func (w *requireWalker) stmt(st ast.Stmt) {
	switch n := st.(type) {
	case *ast.ExprStmt:
		w.expr(n.E)
	case *ast.AssignStmt:
		for _, t := range n.Targets {
			w.expr(t)
		}
		for _, v := range n.Values {
			w.expr(v)
		}
	case *ast.LocalAssign:
		for _, v := range n.Values {
			w.expr(v)
		}
	case *ast.LocalFuncDef:
		w.expr(n.Func)
	case *ast.FuncDefStmt:
		w.expr(n.Func)
	case *ast.DoBlock:
		w.block(n.Block)
	case *ast.IfStmt:
		w.expr(n.Cond)
		w.block(n.Then)
		for _, ei := range n.Elseifs {
			w.expr(ei.Cond)
			w.block(ei.Block)
		}
		w.block(n.Else)
	case *ast.WhileStmt:
		w.expr(n.Cond)
		w.block(n.Block)
	case *ast.RepeatStmt:
		w.block(n.Block)
		w.expr(n.Cond)
	case *ast.NumericFor:
		w.expr(n.Start)
		w.expr(n.End)
		if n.Step != nil {
			w.expr(n.Step)
		}
		w.block(n.Block)
	case *ast.GenericFor:
		for _, x := range n.Exprs {
			w.expr(x)
		}
		w.block(n.Block)
	case *ast.EnumDecl:
		for _, m := range n.Members {
			if m.Value != nil {
				w.expr(m.Value)
			}
		}
	case *ast.MacroDef:
		if n.Body != nil {
			w.expr(n.Body)
		}
		w.block(n.Block)
	case *ast.ClassDecl:
		for _, m := range n.Members {
			if m.Init != nil {
				w.expr(m.Init)
			}
			w.block(m.Body)
		}
	case *ast.ImportStmt:
		w.add(n.Path)
	case *ast.DeferStmt:
		w.expr(n.Call)
	case *ast.SpawnStmt:
		w.expr(n.Call)
	case *ast.SelectStmt:
		for _, sc := range n.Cases {
			if sc.Recv != nil {
				w.expr(sc.Recv.Obj)
				for _, a := range sc.Recv.Args {
					w.expr(a)
				}
			}
			if sc.Send != nil {
				w.expr(sc.Send.Obj)
				for _, a := range sc.Send.Args {
					w.expr(a)
				}
			}
			if sc.Timeout != nil {
				w.expr(sc.Timeout)
			}
			w.block(sc.Block)
		}
		w.block(n.Default)
	}
}

func (w *requireWalker) expr(x ast.Expr) {
	switch n := x.(type) {
	case *ast.ParenExpr:
		w.expr(n.E)
	case *ast.TableLit:
		for _, f := range n.Fields {
			if f.KeyX != nil {
				w.expr(f.KeyX)
			}
			if f.Val != nil {
				w.expr(f.Val)
			}
		}
	case *ast.FuncLit:
		w.block(n.Body)
	case *ast.IndexExpr:
		w.expr(n.Obj)
		w.expr(n.Key)
	case *ast.FieldExpr:
		w.expr(n.Obj)
	case *ast.CallExpr:
		if id, ok := n.Func.(*ast.Ident); ok && id.Name == "require" && len(n.Args) == 1 {
			if sl, ok := n.Args[0].(*ast.StringLit); ok {
				w.add(sl)
			}
		}
		w.expr(n.Func)
		for _, a := range n.Args {
			w.expr(a)
		}
	case *ast.MethodCallExpr:
		w.expr(n.Obj)
		for _, a := range n.Args {
			w.expr(a)
		}
	case *ast.BinOpExpr:
		w.expr(n.L)
		w.expr(n.R)
	case *ast.UnOpExpr:
		w.expr(n.X)
	case *ast.CastExpr:
		w.expr(n.E)
	case *ast.NonNullExpr:
		w.expr(n.E)
	case *ast.AwaitExpr:
		w.expr(n.X)
	}
}
