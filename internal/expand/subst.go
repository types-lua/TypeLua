package expand

import (
	"github.com/typelua/typelua/internal/ast"
	"github.com/typelua/typelua/internal/token"
)

// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：代入是“影子感知”的——shadow 计数记被绑走的形参，命中即留；
// trail/checkpoint/unwind 管块域进出；dotsShadow 区分宏 ... 与函数 ...。
// 障碍是 repeat..until 体与 cond 同域（须同 checkpoint），LocalAssign 名绑晚于值。

// ---------- substitution (scope-aware: shadowed params are left alone) ----------

type substCtx struct {
	args   map[string]ast.Expr // param name -> arg AST (already expanded)
	shadow map[string]int      // param names currently shadowed (depth count)
	trail  []string            // push order for block-scoped unwinding
	// Variadic rest: extra call args spliced at an unshadowed bare `...`
	// 排查看 Span/诊断，如 `local x=1` 错圈整句。
	varArgs []ast.Expr
	// dotsShadow counts enclosing function bodies: `...` inside one
	// 排查看 Span/诊断，如 `local x=1` 错圈整句。
	dotsShadow int
	// strayDots collects unshadowed `...` outside call arguments
	// 排查看 Span/诊断，如 `local x=1` 错圈整句。
	strayDots []token.Span
}

func (s *substCtx) pushShadow(name string) {
	// 设计者视角：pushShadow 记一层遮蔽并留 trail 供 unwind。障碍是同名多层：
	// 计数而非布尔，退一层减一。排查现场：影子未解先查 unwindTo。
	s.shadow[name]++
	s.trail = append(s.trail, name)
}

func (s *substCtx) checkpoint() int { return len(s.trail) }

func (s *substCtx) unwindTo(cp int) {
	// 设计者视角：unwindTo 退到 checkpoint，块出即清本块影子，兄弟不受染。障碍是
	// repeat 体+cond 同 checkpoint（见 substStmt）：须一并退。排查现场：兄弟误影
	// 先查 checkpoint 配对。
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

// 排查看 Span/诊断，如 `local x=1` 错圈整句。
// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：substBlock 给块拍快照、代入、退影子，保兄弟与外层干净。障碍是 Ret
// 亦须代（尾 return 含参）。候选：调用方退 vs 内部退；选内部，配对不漏。不变量：
// 出口 shadow 恢复入口。排查现场：跨块影子泄漏先查 unwind。
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

// 排查看 Span/诊断，如 `local x=1` 错圈整句。
// `...` (variadic macro rest) into per-occurrence copies of the extras.
//
// 设计者视角：substCallArgs 是变参的“摊饼处”——唯 call-args 位裸 ... 方 splice，
// 每份 extras 独拷（防多处共享），函数内 ...（dotsShadow>0）不动。障碍是
// 非 call 位 ...：此不理，留 strayDots 报。候选：共享 vs 复印；选复印。不变量：
// 非 splice 位逐个 subst。排查现场：... 未展先查位判，extras 互串先查 copy。
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

// 排查看 Span/诊断，如 `local x=1` 错圈整句。
// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：substExpr 见 Ident 即换（影子则留），见 FuncLit 即压参影+dots++，
// 见 VarArg 非 call 位即记 stray+产 nil 保可跑。障碍是参数 AST 须每用复印（同参
// 多处），否则改写串。候选：引用 vs 复印；选复印。不变量：影子名永不换。排查现场：
// 同参两处一改俱改先查 copyExpr。
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
		// 报告TL，如 `tl` 例，排查看 Span。
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
	case *ast.AwaitExpr:
		n.X = substExpr(n.X, s)
		return n
	default:
		return x
	}
}

func substStmt(st ast.Stmt, s *substCtx) {
	// 设计者视角：substStmt 按语句型代入，绑定语义各守其律——LocalAssign 先值后名
	// （名对后兄弟可见），LocalFuncDef 名先绑（递归），for 变量块内影，repeat 体
	// cond 同域。障碍是 select recv 局部量：RecvlLocal 方压影。候选：穷举 vs 反射；
	// 选穷举，新语法须显式。排查现场：新语句参未换先查此处缺分支。
	switch n := st.(type) {
	case *ast.LocalAssign:
		for i, v := range n.Values {
			n.Values[i] = substExpr(v, s)
		}
		// 排查看 Span/诊断，如 `local x=1` 错圈整句。
		// 排查看 Span/诊断，如 `local x=1` 错圈整句。
		for _, id := range n.Names {
			s.pushShadow(id.Name)
		}
	case *ast.DeferStmt:
		// 排查看 Span/诊断，如 `local x=1` 错圈整句。
		// 排查看 Span/诊断，如 `local x=1` 错圈整句。
		n.Call = substExpr(n.Call, s)
	case *ast.SpawnStmt:
		// 排查看 Span/诊断，如 `local x=1` 错圈整句。
		n.Call = substExpr(n.Call, s)
	case *ast.SelectStmt:
		for _, sc := range n.Cases {
			if sc.Recv != nil {
				sc.Recv.Obj = substExpr(sc.Recv.Obj, s)
				sc.Recv.Args = substCallArgs(sc.Recv.Args, s)
			}
			if sc.Send != nil {
				sc.Send.Obj = substExpr(sc.Send.Obj, s)
				sc.Send.Args = substCallArgs(sc.Send.Args, s)
			}
			if sc.Timeout != nil {
				sc.Timeout = substExpr(sc.Timeout, s)
			}
			cp := s.checkpoint()
			if sc.RecvLocal {
				for _, v := range sc.RecvVars {
					s.pushShadow(v.Name)
				}
			}
			substBlock(sc.Block, s)
			s.unwindTo(cp)
		}
		if n.Default != nil {
			substBlock(n.Default, s)
		}
	case *ast.ImportStmt:
		// 排查看 Span/诊断，如 `local x=1` 错圈整句。
		if n.Name != nil {
			s.pushShadow(n.Name.Name)
		}
	case *ast.LocalFuncDef:
		// 排查看 Span/诊断，如 `local x=1` 错圈整句。
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
		// 排查看 Span/诊断，如 `local x=1` 错圈整句。
		// 排查看 Span/诊断，如 `local x=1` 错圈整句。
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
		// 排查看 Span/诊断，如 `local x=1` 错圈整句。
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
	// 设计者视角：尾 return 亦代入（`return x` 中 x 可为参），nil 宽容。障碍是忘代
	// 即参残留。排查现场：return 内参未换先查此处。
	if b.Ret != nil {
		for i, e := range b.Ret.Exprs {
			b.Ret.Exprs[i] = substExpr(e, s)
		}
	}
}
