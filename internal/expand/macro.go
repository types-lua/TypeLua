package expand

import (
	"github.com/typelua/typelua/internal/ast"
	"github.com/typelua/typelua/internal/diag"
	"github.com/typelua/typelua/internal/token"
)

// 宏详见实现，排查看 Span。
//
// 设计者视角：本文件是宏调用的“接生房”——arity 把门、enter/leave 防递归、
// renameScope 备卫生、expand*Macro 走复印改名代入全套。障碍是实参须先在调用点
// 展开详见实现，排查看 Span。
// ---------- macro expansion core ----------

// checkArity validates argument count. A variadic macro (`...`) requires
// 排查看 Span/诊断，如 `local x=1` 错圈整句。
// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：checkArity 是“检票口”——变参下限对、定参精确对，错即报
// MacroArityMismatch 返 nil 让上层产 nil/空保可跑。障碍是变参 extras 可零：
// nargs==len(params) 亦过。候选：抛错中断 vs 诊断+可跑；选后者。不变量：过则返
// 索引片。排查现场：变参误报先查 hasVarArg。
func (e *expander) checkArity(md *macroDef, call token.Span, nargs int) []int {
	if md.hasVarArg {
		if nargs < len(md.params) {
			e.errorf(diag.MacroArityMismatch, call, "macro '%s' expects at least %d argument(s), got %d", md.name, len(md.params), nargs)
			return nil
		}
	} else if nargs != len(md.params) {
		e.errorf(diag.MacroArityMismatch, call, "macro '%s' expects %d argument(s), got %d", md.name, len(md.params), nargs)
		return nil
	}
	idx := make([]int, nargs)
	for i := range idx {
		idx[i] = i
	}
	return idx
}

// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：enter 是递归的“闸刀”——栈中重名即 RecursiveMacro，栈深超 64 亦拦，
// 过则压栈。障碍是间接递归（a→b→a）：单名判不够，须整栈扫+深度双保险。候选：
// visited 集 vs 栈；选栈，报错可指名。不变量：true 恒配 leave详见实现，排查看 Span。
// 误报递归先查栈是否忘弹。
func (e *expander) enter(name string, sp token.Span) bool {
	for _, n := range e.stack {
		if n == name {
			e.errorf(diag.RecursiveMacro, sp, "recursive macro expansion of '%s'", name)
			return false
		}
	}
	if len(e.stack) >= maxExpandDepth {
		e.errorf(diag.RecursiveMacro, sp, "macro expansion too deep (possible recursion via '%s')", name)
		return false
	}
	e.stack = append(e.stack, name)
	return true
}

func (e *expander) leave() {
	// 设计者视角：leave 与 enter 配对弹栈（调用方 defer），保误报不蔓延。障碍是
	// 早返忘弹：须 defer。排查现场：后续宏误报递归先查 leave。
	e.stack = e.stack[:len(e.stack)-1]
}

// 排查看 Span/诊断，如 `local x=1` 错圈整句。
// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：renameScope 备“卫生包”——avoid 采实参全拼（见 collectIdents），
// labels 待 prescan。障碍是漏采：少一拼即可能捕获；故过近似宁多勿少。候选：
// 精确自由变量 vs 过近似；选过近似，counter 多跳无害。不变量：avoid 含实参全拼。
// 排查现场：捕获先查 collect 覆盖。
func (e *expander) renameScope(args []ast.Expr) *renameCtx {
	r := &renameCtx{e: e, vars: map[string]string{}, labels: map[string]string{}, avoid: map[string]bool{}}
	for _, a := range args {
		collectIdents(r.avoid, a)
	}
	return r
}

// reportStrayDots reports variadic `...` used outside call arguments.
//
// 设计者视角：reportStrayDots 把代入期收的 strayDots 统一报 TL3706。障碍是 `...`
// 在函数体内属该函数而非宏（见 dotsShadow），须免报；此处只报真 stray。候选：
// 代入点即报 vs 收集后报；选后者，一调用一错。不变量：纯报不改。排查现场：漏报先查
// dotsShadow，误报先查 call-args 判。
func (e *expander) reportStrayDots(md *macroDef, sub *substCtx) {
	for _, sp := range sub.strayDots {
		e.errorf(diag.MacroVarargOutsideCallArgs, sp, "variadic '...' of macro '%s' must appear in call arguments", md.name)
	}
}

// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：表达式宏五步曲——展实参→arity→enter→复印改名代入→递归展结果。
// 障碍是改名与代入序：须先 prescanLabels+rename 再 subst，否则影子参错位；
// 变参 extras 经 varArgs 在 call-args 位 splice。候选：原地展 vs 复印展；选复印，
// 定义底片 pristine。不变量：失败返 NilLit 保可跑。排查现场：卫生破先查序，
// ... 未展先查 splice 位。
func (e *expander) expandExprMacro(md *macroDef, call *ast.CallExpr, env *scope) ast.Expr {
	// 排查看 Span/诊断，如 `local x=1` 错圈整句。
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
	if md.hasVarArg {
		sub.varArgs = append([]ast.Expr{}, args[len(md.params):]...)
	}
	body = substExpr(body, sub)
	e.reportStrayDots(md, sub)
	return e.expandExpr(body, env)
}

// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：语句宏七步曲——前五同表达式，另加语句合法性筛（splice 占位须为
// Call/MethodCall，否则 MacroExpandsToInvalidStmt 汰之）与调用点域二次展
// （嵌套宏/定义）。障碍是占位 `...()` 展后须为真调用，否则吐非法 Lua；故筛。
// 候选：筛 vs 不筛；选筛，发射永合法。不变量：返片可拼。排查现场：语句丢失先查
// valid 过滤，嵌套未展先查二次 expandBlock。
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
	if md.hasVarArg {
		sub.varArgs = append([]ast.Expr{}, args[len(md.params):]...)
	}
	substBlock(blk, sub)
	e.reportStrayDots(md, sub)
	// 排查看 Span/诊断，如 `local x=1` 错圈整句。
	// 排查看 Span/诊断，如 `local x=1` 错圈整句。
	var valid []ast.Stmt
	for _, st := range blk.Stmts {
		if es, ok := st.(*ast.ExprStmt); ok {
			inner := es.E
			for {
				aw, ok := inner.(*ast.AwaitExpr)
				if !ok {
					break
				}
				inner = aw.X
			}
			switch inner.(type) {
			case *ast.CallExpr, *ast.MethodCallExpr:
			default:
				e.errorf(diag.MacroExpandsToInvalidStmt, es.Span(), "macro '%s' expanded to an invalid statement", md.name)
				continue
			}
		}
		valid = append(valid, st)
	}
	blk.Stmts = valid
	// 排查看 Span/诊断，如 `local x=1` 错圈整句。
	child := &scope{parent: env}
	tmp := &ast.Block{Stmts: blk.Stmts, S: call.Span()}
	e.expandBlock(tmp, child)
	return tmp.Stmts
}
