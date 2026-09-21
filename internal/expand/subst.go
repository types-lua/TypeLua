package expand

import (
	"github.com/typelua/typelua/internal/ast"
	"github.com/typelua/typelua/internal/token"
)

// Substitution (scope-aware: shadowed params are left alone).

// ---------- substitution (scope-aware: shadowed params are left alone) ----------

type substCtx struct {
	args   map[string]ast.Expr // param name -> arg AST (already expanded)
	shadow map[string]int      // param names currently shadowed (depth count)
	trail  []string            // push order for block-scoped unwinding
	// Variadic rest: extra call args spliced at an unshadowed bare `...`
	// in call-argument position. Nil outside variadic-macro expansion.
	varArgs []ast.Expr
	// dotsShadow counts enclosing function bodies: `...` inside one
	// belongs to that function, never to the macro.
	dotsShadow int
	// strayDots collects unshadowed `...` outside call arguments
	// (reported as TL3706 by the expansion driver).
	strayDots []token.Span
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

// substCallArgs substitutes call arguments, splicing an unshadowed bare
// `...` (variadic macro rest) into per-occurrence copies of the extras.
func substCallArgs(args []ast.Expr, s *substCtx) []ast.Expr {
	out := make([]ast.Expr, 0, len(args)+len(s.varArgs))
	for _, a := range args {
		if _, ok := a.(*ast.VarArg); ok && s.varArgs != nil && s.dotsShadow == 0 {
			for _, va := range s.varArgs {
				out = append(out, copyExpr(va))
			}
			continue
		}
		out = append(out, substExpr(a, s))
	}
	return out
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
		savedDots := s.dotsShadow
		s.dotsShadow++
		substBlock(n.Body, s)
		s.dotsShadow = savedDots
		s.unwindTo(cp)
		return n
	case *ast.VarArg:
		// Stray `...` (variadic rest outside call arguments): the driver
		// reports TL3706; substitute nil to keep output runnable.
		if s.varArgs != nil && s.dotsShadow == 0 {
			s.strayDots = append(s.strayDots, n.S)
			return &ast.NilLit{S: n.S}
		}
		return n
	case *ast.CallExpr:
		n.Func = substExpr(n.Func, s)
		n.Args = substCallArgs(n.Args, s)
		return n
	case *ast.MethodCallExpr:
		n.Obj = substExpr(n.Obj, s)
		n.Args = substCallArgs(n.Args, s)
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
	case *ast.DeferStmt:
		// Deferred calls substitute like normal calls (evaluated at exit,
		// but with the call-site argument values, as written).
		n.Call = substExpr(n.Call, s)
	case *ast.ImportStmt:
		// The bound name shadows macro parameters from here on.
		if n.Name != nil {
			s.pushShadow(n.Name.Name)
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
		savedDots := s.dotsShadow
		s.dotsShadow++
		substBlock(n.Func.Body, s)
		s.dotsShadow = savedDots
		s.unwindTo(cp)
	case *ast.FuncDefStmt:
		cp := s.checkpoint()
		for _, p := range n.Func.Params {
			s.pushShadow(p.Name.Name)
		}
		if n.Func.VarArgName != nil {
			s.pushShadow(n.Func.VarArgName.Name)
		}
		savedDots := s.dotsShadow
		s.dotsShadow++
		substBlock(n.Func.Body, s)
		s.dotsShadow = savedDots
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
				savedDots := s.dotsShadow
				s.dotsShadow++
				substBlock(m.Body, s)
				s.dotsShadow = savedDots
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
