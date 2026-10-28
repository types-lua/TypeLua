package expand

import (
	"fmt"
	"github.com/typelua/typelua/internal/ast"
	"github.com/typelua/typelua/internal/diag"
	"github.com/typelua/typelua/internal/token"
)

// 展开器是印前制版：先收 `macro twice(x) x+x end`，`twice(21)` 复印改名代入成 `21+21`，定义随即擦除。
//
// 设计者视角：展开器是“印前制版”——先收宏定义、再复印改名代入、末擦定义，检查器
// 见到的已是成品。障碍是作用域污染：宏表须与 checker.Scope 分离、只看语法。

// maxExpandDepth=64 是宏熔丝：`a→b→a` 互递归到 64 层报 RecursiveMacro 仍产可跑输出。
//
// 设计者视角：64 步是“熔断丝”——自递归/互递归（见 enter 栈检）至此报诊断不停机，
// 保幂等与可跑输出。候选：无限 vs 上限；选上限。排查现场：误报深嵌套先查上限。
const maxExpandDepth = 64

// macroDef 是宏底片：存形参/vararg/体（表达式 `x+x` 或语句块），`isExpr` 错位如语句宏进表达式即报 TL3706。
//
// 设计者视角：macroDef 是宏的“底片”——存形参名、vararg 旗、双体（body/block）
// 与 isExpr 旗，span 供诊断。障碍是语句/表达式宏混用：isExpr 分流，错位报
// StmtMacroInExprPosition。候选：两类分表 vs 一表+旗；选一表，lookup 统一。
// 不变量：展开期外无残留。排查现场：宏残留先查擦除分支。
type macroDef struct {
	name      string
	params    []string
	hasVarArg bool       // 尾随 `...` 收集多余调用实参
	body      ast.Expr   // 表达式宏体
	block     *ast.Block // 语句宏体
	isExpr    bool
	span      token.Span
}

// scope 是宏一室一册：只记宏名（如内层 `twice` 遮外层），与 checker 类型域分家防串味。
//
// 设计者视角：scope 是宏的“一室一册”——只记宏名，函数/块进新层（parent 链），
// 查找自内向外，遮蔽自然成立。障碍是与类型域混用：若合表，类型名会误触宏；故
// 分离。不变量：只写当前层。排查现场：宏找不到先查层 parent 是否接错。
type scope struct {
	parent *scope
	defs   map[string]*macroDef
}

func (s *scope) lookup(name string) *macroDef {
	// 设计者视角：lookup 沿 parent 上爬，内层遮外层。障碍是 nil 层：循环判空，
	// 漏宏不 panic。候选：map 扁平 vs 链；选链，遮蔽语义对。排查现场：外层宏被遮
	// 先查内层同名定义。
	for sc := s; sc != nil; sc = sc.parent {
		if d, ok := sc.defs[name]; ok {
			return d
		}
	}
	return nil
}

// expander 是单次展开随身包：diags 攒错，counter 保 `__tl_macro_7__x` 单调，stack 防 `a→b→a` 递归。
//
// 设计者视角：expander 是单次展开的“随身包”——diags 攒错，counter 保 fresh 单调，
// stack 记活跃宏名防递归。障碍是复用：每 ExpandFile 新造，不跨文件带计数。不变量：
// counter 只增。排查现场：fresh 碰撞先查计数器是否重置。
type expander struct {
	diags   []diag.Diagnostic
	counter int
	stack   []string // 活跃宏名，用于递归检测
}

func (e *expander) errorf(code diag.Code, sp token.Span, format string, args ...any) {
	// 设计者视角：errorf 直通 diag.Errorf，span 保源坐标。障碍是展开错须可跑：
	// 报错同时产 nil/跳过（见 checkArity），不中断。排查现场：诊断无位先查 span。
	e.diags = append(e.diags, diag.Errorf(code, sp, format, args...))
}

func (e *expander) fresh(base string, avoid map[string]bool) string {
	// 设计者视角：fresh 造 `__tl_macro_N__base` 唯一名，撞 avoid 即 bump。障碍是
	// 调用点捕获：avoid 含实参全拼（见 renameScope），fresh 必避之。候选：UUID vs
	// 计数；选计数，可读且确定。不变量：返名必不在 avoid。排查现场：卫生泄漏先查
	// avoid 是否漏采。
	for {
		e.counter++
		name := fmt.Sprintf("__tl_macro_%d__%s", e.counter, base)
		if !avoid[name] {
			return name
		}
	}
}

// ExpandFile 是总闸：空文件 nil 宽容，一遍收集+展开+擦除，二轮无定义即零改（幂等）。
//
// 设计者视角：ExpandFile 是宏的“总闸”——空文件 nil 宽容，一遍 expandBlock 走完
// 定义收集+语句展开+return 展开，定义随之擦除。障碍是重复展开：二轮无定义即零改，
// 故幂等天然成立；障碍是诊断序：收集错先于展开错。图：收集→拼接→擦除。不变量：
// 出口无 MacroDef。排查现场：宏残留先查擦除分支，二次展开有变先查是否复用旧 env。
func ExpandFile(f *ast.File) []diag.Diagnostic {
	e := &expander{}
	if f.Block != nil {
		e.expandBlock(f.Block, &scope{})
	}
	return e.diags
}

func (e *expander) expandBlock(b *ast.Block, env *scope) {
	// 设计者视角：expandBlock 两遍走——先收同块全定义（前向引用可 call 在前定义在后），
	// 再逐句 expandStmtInto 拼接+擦定义，末展 return。障碍是同名重复：报 DuplicateMacro
	// 并保留首个；障碍是块作用域：子块 parent 新层，宏定义不泄。候选：一遍 vs 两遍；
	// 选两遍，前向可用。不变量：MacroDef 零残留。排查现场：前向宏找不到先查收集遍。
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
//
// 设计者视角：expandStmtInto 是语句的“岔口”——ExprStmt 形 `foo(args)` 先嗅语句宏
// （isExpr 为 false 方命中），余者按型下钻（函数体开子域防宏泄）。障碍是语句宏
// 误入表达式位：此处只认语句位，表达式位另由 expandExpr 拒之。候选：统一入口 vs
// 分流；选分流，错位诊断精确。不变量：返片可直接拼 out。排查现场：语句宏未展先查
// 是否包了括号（非裸 Call 即不命中）。
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
//
// 设计者视角：expandExpr 在表达式林里找裸 `name(args)`——命中表达式宏即展，
// 命中语句宏即报 StmtMacroInExprPosition 并产 nil 保可跑；余者递归下钻，FuncLit
// 开子域。障碍是递归深度：嵌套宏经 expandExprMacro 回调此处，enter 栈防环。候选：
// 自顶 vs 自底；选先展实参（调用点语境）再展体（见 macro.go）。不变量：永返非 nil。
// 排查现场：语句宏入错位先查 isExpr，栈溢出先查 enter。
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
