package emitter

import (
	"strconv"

	"github.com/typelua/typelua/internal/ast"
)

// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
//   - `defer <call>` registers cleanup run LIFO on every NORMAL function
//     exit (each `return`, plus fallthrough). Calls evaluate lazily inside
//
// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//   - `errdefer <call>` registers cleanup run LIFO only when leaving via
//     an explicit `error(...)` call in the same function.
//
// …（略，详见实现；排查先看 Span/诊断）
// 点）。候选：一函数一帧 vs 全文件一表；选前者，嵌套闭包互不串单。不变量：
// 无 defer 帧的三变量全空，发射零开销。排查现场：defer 未执行先查帧归属
// （top 是否为当前函数体）与 hasDefer 预扫。
type deferFrame struct {
	top      *ast.Block // function body (nil until set)
	listVar  string     // __tl_defer_N
	runVar   string     // normal-exit runner
	errVar   string     // error-exit runner (only when hasErr)
	hasDefer bool
	hasErr   bool
}

func (e *emitter) deferTop() *deferFrame {
	// 设计者视角：deferTop 取栈顶帧，好比看最上层保洁单——nil 即无函数上下文
	// （理论上不应发生，调用方须 nil 宽容）。障碍是并发包装与 defer 叠加：
	// chunk 帧与 __tl_run 闭包帧是两层，不可混。候选：返回零值 vs 指针 nil；
	// 选 nil，让调用方显式分支。不变量：非空栈恒返有效帧。排查现场：defer 改写
	// 失效先查栈是否提前 pop。
	if len(e.deferStack) == 0 {
		return nil
	}
	return e.deferStack[len(e.deferStack)-1]
}

// 排查看 Span/诊断，如 `local x=1` 错圈整句。
// 排查看 Span/诊断，如 `local x=1` 错圈整句。
// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：scanDefers 是发射前的“侦察机”——只钻普通块（do/if/循环/select），
// 见函数/类/macro 即停（彼等是新帧）。障碍是 SpawnStmt 体：看似块实为新任务
// 闭包，若钻入会把子任务的 defer 算到父帧，导致双跑；故显式跳过。候选：发射时
// 按需分配 vs 预扫分配；选预扫，一次遍历定 hasDefer/hasErr，后续 prelude/trail/
// return 改写才有据可依。不变量：返回值决定临时变量是否分配。排查现场：defer
// 前导缺失先查是否藏在未覆盖的语句类型里（如新增语法未加入 walkStmt）。
func scanDefers(b *ast.Block) (hasDefer, hasErr bool) {
	if b == nil {
		return false, false
	}
	var walkBlock func(b *ast.Block)
	var walkStmt func(st ast.Stmt)
	walkStmt = func(st ast.Stmt) {
		switch n := st.(type) {
		case *ast.DeferStmt:
			if n.IsErr {
				hasErr = true
			} else {
				hasDefer = true
			}
		case *ast.DoBlock:
			walkBlock(n.Block)
		case *ast.IfStmt:
			walkBlock(n.Then)
			for _, ei := range n.Elseifs {
				walkBlock(ei.Block)
			}
			walkBlock(n.Else)
		case *ast.WhileStmt:
			walkBlock(n.Block)
		case *ast.RepeatStmt:
			walkBlock(n.Block)
		case *ast.NumericFor:
			walkBlock(n.Block)
		case *ast.GenericFor:
			walkBlock(n.Block)
		case *ast.SpawnStmt:
			// 排查看 Span/诊断，如 `local x=1` 错圈整句。
		case *ast.SelectStmt:
			for _, sc := range n.Cases {
				walkBlock(sc.Block)
			}
			walkBlock(n.Default)
		}
	}
	walkBlock = func(b *ast.Block) {
		if b == nil {
			return
		}
		for _, st := range b.Stmts {
			walkStmt(st)
		}
	}
	walkBlock(b)
	return hasDefer, hasErr
}

// 排查看 Span/诊断，如 `local x=1` 错圈整句。
// 排查看 Span/诊断，如 `local x=1` 错圈整句。
// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：pushDeferFrame 是“开账”——先 scanDefers 侦察，再按需领号
// （__tl_defer_N 全文件单调，防嵌套碰撞），最后压栈。障碍是无 defer 函数须
// 零开销：不领号、prelude/trail 全跳。图：
//
//	push(帧+号) → prelude(建表+runner) → 体 → return改写/trail → pop
//
// 不变量：push/pop 严格配对（含 chunk）；tmpCount 只增。排查现场：临时变量
// 碰撞先查 tmpCount 是否多处自增，defer 串帧先查 pop 是否遗漏。
func (e *emitter) pushDeferFrame(b *ast.Block) {
	hasDefer, hasErr := scanDefers(b)
	fr := &deferFrame{top: b, hasDefer: hasDefer, hasErr: hasErr}
	if hasDefer || hasErr {
		e.tmpCount++
		fr.listVar = "__tl_defer_" + strconv.Itoa(e.tmpCount)
		fr.runVar = "__tl_rundefer_" + strconv.Itoa(e.tmpCount)
		if hasErr {
			fr.errVar = "__tl_runerr_" + strconv.Itoa(e.tmpCount)
		}
	}
	e.deferStack = append(e.deferStack, fr)
}

func (e *emitter) popDeferFrame() {
	// 设计者视角：popDeferFrame 是“销账”——发射完一函数体即弹栈，防内层帧泄漏
	// 到外层（否则外层 return 会误跑内层的 runner）。障碍是提前返回路径多：
	// 每个 push 点须肉眼配对 pop，defer 语法的 Go 风格极易遗漏。候选：defer 语句
	// 自动弹 vs 手动；选手动，与 indent saved 恢复同节奏。不变量：栈深恒等于
	// 正在发射的函数嵌套深。排查现场：外层多跑 defer 先查内层 pop 是否缺失。
	e.deferStack = e.deferStack[:len(e.deferStack)-1]
}

// 排查看 Span/诊断，如 `local x=1` 错圈整句。
// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：prelude 是“保洁工具间”——先建空表，再按需建 runner：正常 runner
// 倒序跑非 err 项并透传多值（return __r(f()) 保多值），错误 runner 全跑。
// 障碍是纯 err 帧：虽无正常 runner 仍需建表（注册点要写表）。位置铁律：在
// vararg 重绑之后、体语句之前，否则具名 rest 在清理闭包里不可见。候选：runner
// 内联 vs 具名 local function；选具名，return 改写处可复用。不变量：indent 用
// 参不用栈（调用方显式给层）。排查现场：清理闭包读不到 rest 参数先查 prelude
// 顺序，多值丢失先查 runner 是否丢 ... 透传。
func (e *emitter) emitDeferPrelude(ind int) {
	fr := e.deferTop()
	if fr == nil || (!fr.hasDefer && !fr.hasErr) {
		return
	}
	saved := e.indent
	e.indent = ind
	if fr.hasDefer {
		e.pad()
		e.write("local " + fr.listVar + " = {}")
		e.nl()
		e.pad()
		e.write("local function " + fr.runVar + "(...) for __i = #" + fr.listVar + ", 1, -1 do if not " + fr.listVar + "[__i].err then " + fr.listVar + "[__i].fn() end end return ... end")
		e.nl()
	} else {
		// 排查看 Span/诊断，如 `local x=1` 错圈整句。
		e.pad()
		e.write("local " + fr.listVar + " = {}")
		e.nl()
	}
	if fr.hasErr {
		e.pad()
		e.write("local function " + fr.errVar + "() for __i = #" + fr.listVar + ", 1, -1 do " + fr.listVar + "[__i].fn() end end")
		e.nl()
	}
	e.indent = saved
}

// 排查看 Span/诊断，如 `local x=1` 错圈整句。
// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：trail 只守“自然滑出”这扇门——有显式 return 的函数已在 emitReturn
// 里改写为 runner 包裹，此处须让路，否则清理跑两遍。障碍是空函数
// （无语句无 return）：trail 即全部清理，不可因“无事”跳过。候选：统一在 trail
// 跑全部 vs return/trail 分工；选分工，return 保多值透传、trail 保裸滑出。
// 不变量：fr.top == b 才跑（防串帧），无 Ret 才跑。排查现场：清理跑两次先查
// return 改写与 trail 是否同时命中，无 return 却不清理先查 top 比对。
func (e *emitter) emitDeferTrail(b *ast.Block, ind int) {
	fr := e.deferTop()
	if fr == nil || fr.top != b || !fr.hasDefer {
		return
	}
	if b.Ret != nil {
		return
	}
	saved := e.indent
	e.indent = ind
	e.pad()
	e.write(fr.runVar + "()")
	e.nl()
	e.indent = saved
}

// 排查看 Span/诊断，如 `local x=1` 错圈整句。
// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：emitReturn 是三岔路口——`return error(...)` 走 err 跑道（先跑
// err runner 再抛），带值普通 return 走 runner 包裹（保多值），无 defer 帧
// 走裸 return。障碍是 error 识别：须认 checker 标的 ErrorCall（见 isErrorCall），
// 名字叫 error 的普通函数不可误伤；障碍二是零值 return 也要跑 runner（`return`
// 裸跳仍须清理）。候选：重写为 goto 收敛 vs 各 return 点包裹；选后者，兼容 5.1
// 无 goto。不变量：有 hasDefer 的值返回恒经 runner。排查现场：errdefer 未触发
// 先查 ErrorCall 标记，清理后多值变单值先查包裹括号。
func (e *emitter) emitReturn(ret *ast.ReturnStmt) {
	e.mark(ret, "")
	e.pad()
	fr := e.deferTop()
	if fr != nil && fr.hasErr && len(ret.Exprs) >= 1 && isErrorCall(ret.Exprs[0]) {
		// `return error(...)`: errdefers (+all defers) run, then throw.
		e.write("do " + fr.errVar + "(); ")
		e.emitExpr(ret.Exprs[0])
		e.write(" end")
		e.nl()
		return
	}
	e.write("return")
	if fr != nil && fr.hasDefer && len(ret.Exprs) > 0 {
		e.write(" " + fr.runVar + "(")
		for i, x := range ret.Exprs {
			if i > 0 {
				e.write(", ")
			}
			e.emitExpr(x)
		}
		e.write(")")
	} else if fr != nil && fr.hasDefer {
		e.write(" " + fr.runVar + "()")
	} else if len(ret.Exprs) > 0 {
		e.write(" ")
		for i, x := range ret.Exprs {
			if i > 0 {
				e.write(", ")
			}
			e.emitExpr(x)
		}
	}
	e.nl()
}

// isErrorCall reports checker-marked builtin `error(...)` calls.
//
// 设计者视角：isErrorCall 是 errdefer 的“点火判据”——只认 checker 打标的
// ErrorCall，不认名（局部遮蔽 error 后名同实异）。障碍是发射器无符号表：
// 若按名判，`local error = print; error("x")` 会误触 errdefer。取舍：信任
// checker 标记，发射零分析。不变量：非 CallExpr 恒 false。排查现场：errdefer
// 误触发先查 AST 的 ErrorCall 是否被 parser/checker 误标。
func isErrorCall(x ast.Expr) bool {
	ce, ok := x.(*ast.CallExpr)
	return ok && ce.ErrorCall
}
