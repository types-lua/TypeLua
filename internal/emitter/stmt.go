package emitter

import (
	"github.com/typelua/typelua/internal/ast"
)

// Statement emission (locals, control flow, enums, functions).
//
// 设计者视角：本文件是语句的“总配电盘”——40+ 分支各管一路，擦除类
// （TypeAlias/Interface/Macro）直接清 pending 隐身，defer/spawn/select 三
// 特种语句各托给专文件。障碍是 declare function 与空函数同形：parser 皆给空
// 体，发射层无法区分；约定由 compiler 预过滤 Declared，此处只管发射有效函数。
// 不变量：每分支出口恒换行（nl），pending 在擦除分支必清。排查现场：类型声明
// 漏进输出先查 compiler 是否未过滤，缩进塌方先查 emitBlock 的 saved 恢复。

func (e *emitter) emitStmt(st ast.Stmt) {
	if st == nil {
		return
	}
	e.mark(st, stmtName(st))
	switch n := st.(type) {
	case *ast.EmptyStmt:
		// Skip (avoid clutter), but keep `;` if it was intentional? Drop.
		e.pending = nil
	case *ast.ExprStmt:
		if isErrorCall(n.E) {
			if fr := e.deferTop(); fr != nil && fr.hasErr {
				// Throwing out: run errdefers (+all defers), then throw.
				e.pad()
				e.write("do " + fr.errVar + "(); ")
				e.emitExpr(n.E)
				e.write(" end")
				e.nl()
				break
			}
		}
		e.pad()
		e.emitExpr(n.E)
		e.nl()
	case *ast.DeferStmt:
		fr := e.deferTop()
		if fr == nil || (!fr.hasDefer && !fr.hasErr) {
			// Defensive (prescan always frames these): run immediately.
			e.pad()
			e.emitExpr(n.Call)
			e.nl()
			break
		}
		e.pad()
		e.write(fr.listVar + "[#" + fr.listVar + " + 1] = { fn = function() ")
		e.emitExpr(n.Call)
		e.write(" end")
		if n.IsErr {
			e.write(", err = true")
		}
		e.write(" }")
		e.nl()
	case *ast.SpawnStmt:
		e.emitSpawn(n)
	case *ast.SelectStmt:
		e.emitSelect(n)
	case *ast.LocalAssign:
		e.pad()
		e.write("local ")
		for i, name := range n.Names {
			if i > 0 {
				e.write(", ")
			}
			e.write(name.Name)
			if e.opt.Target != "5.1" && i < len(n.Attrs) && n.Attrs[i] != "" {
				e.write(" <" + n.Attrs[i] + ">")
			}
		}
		if len(n.Values) > 0 {
			e.write(" = ")
			for i, v := range n.Values {
				if i > 0 {
					e.write(", ")
				}
				e.emitExpr(v)
			}
		}
		e.nl()
	case *ast.ImportStmt:
		// Desugar: `local Name = require("path")` (bare require form).
		e.pad()
		if n.Name != nil {
			e.write("local " + n.Name.Name + " = ")
		}
		e.write("require(")
		e.emitExpr(n.Path)
		e.write(")")
		e.nl()
	case *ast.LocalFuncDef:
		e.pad()
		e.write("local function " + n.Name.Name + "(")
		e.emitParams(n.Func)
		e.write(")")
		e.nl()
		e.emitVarArgPrelude(n.Func.Params, n.Func.HasVarArg, n.Func.VarArgName, e.indent+1)
		e.pushDeferFrame(n.Func.Body)
		e.emitDeferPrelude(e.indent + 1)
		e.emitBlock(n.Func.Body, e.indent+1)
		e.emitDeferTrail(n.Func.Body, e.indent+1)
		e.popDeferFrame()
		e.pad()
		e.write("end")
		e.nl()
	case *ast.FuncDefStmt:
		// `declare function` has empty body and must not emit.
		if n.Func != nil && n.Func.Body != nil && len(n.Func.Body.Stmts) == 0 && n.Func.Body.Ret == nil {
			// Heuristic: empty body could be user `function foo() end` (valid) or `declare function` (no emit).
			// Parser marks declare with empty block but same as empty function. We cannot distinguish here.
			// Convention: emit empty functions (they are valid Lua); declare functions should be filtered by compiler
			// before emit (it skips Declared decls). This path is for non-declare only.
		}
		e.pad()
		e.write("function ")
		e.write(funcNameString(n.Name))
		e.write("(")
		params := n.Func
		if params != nil {
			// For colon methods, first param is implicit self at runtime only if body uses self?
			// Our checker does not insert self; Lua `: ` call passes self automatically.
			// Emit params as written (without self), preserving `:` in name.
			e.emitParams(params)
		}
		e.write(")")
		e.nl()
		if params != nil {
			e.emitVarArgPrelude(params.Params, params.HasVarArg, params.VarArgName, e.indent+1)
			e.pushDeferFrame(params.Body)
			e.emitDeferPrelude(e.indent + 1)
			e.emitBlock(params.Body, e.indent+1)
			e.emitDeferTrail(params.Body, e.indent+1)
			e.popDeferFrame()
		}
		e.pad()
		e.write("end")
		e.nl()
	case *ast.AssignStmt:
		e.pad()
		for i, t := range n.Targets {
			if i > 0 {
				e.write(", ")
			}
			e.emitExpr(t)
		}
		e.write(" = ")
		for i, v := range n.Values {
			if i > 0 {
				e.write(", ")
			}
			e.emitExpr(v)
		}
		e.nl()
	case *ast.DoBlock:
		e.pad()
		e.write("do")
		e.nl()
		e.emitBlock(n.Block, e.indent+1)
		e.pad()
		e.write("end")
		e.nl()
	case *ast.IfStmt:
		e.pad()
		e.write("if ")
		e.emitExpr(n.Cond)
		e.write(" then")
		e.nl()
		e.emitBlock(n.Then, e.indent+1)
		for _, ei := range n.Elseifs {
			e.pad()
			e.write("elseif ")
			e.emitExpr(ei.Cond)
			e.write(" then")
			e.nl()
			e.emitBlock(ei.Block, e.indent+1)
		}
		if n.Else != nil {
			e.pad()
			e.write("else")
			e.nl()
			e.emitBlock(n.Else, e.indent+1)
		}
		e.pad()
		e.write("end")
		e.nl()
	case *ast.WhileStmt:
		e.pad()
		e.write("while ")
		e.emitExpr(n.Cond)
		e.write(" do")
		e.nl()
		e.emitBlock(n.Block, e.indent+1)
		e.pad()
		e.write("end")
		e.nl()
	case *ast.RepeatStmt:
		e.pad()
		e.write("repeat")
		e.nl()
		e.emitBlock(n.Block, e.indent+1)
		e.pad()
		e.write("until ")
		e.emitExpr(n.Cond)
		e.nl()
	case *ast.NumericFor:
		e.pad()
		e.write("for " + n.Var.Name + " = ")
		e.emitExpr(n.Start)
		e.write(", ")
		e.emitExpr(n.End)
		if n.Step != nil {
			e.write(", ")
			e.emitExpr(n.Step)
		}
		e.write(" do")
		e.nl()
		e.emitBlock(n.Block, e.indent+1)
		e.pad()
		e.write("end")
		e.nl()
	case *ast.GenericFor:
		e.pad()
		e.write("for ")
		for i, name := range n.Names {
			if i > 0 {
				e.write(", ")
			}
			e.write(name.Name)
		}
		e.write(" in ")
		for i, x := range n.Exprs {
			if i > 0 {
				e.write(", ")
			}
			e.emitExpr(x)
		}
		e.write(" do")
		e.nl()
		e.emitBlock(n.Block, e.indent+1)
		e.pad()
		e.write("end")
		e.nl()
	case *ast.BreakStmt:
		e.pad()
		e.write("break")
		e.nl()
	case *ast.GotoStmt:
		e.pad()
		e.write("goto " + n.Label.Name)
		e.nl()
	case *ast.LabelStmt:
		e.pad()
		e.write("::" + n.Name.Name + "::")
		e.nl()
	case *ast.TypeAliasDecl, *ast.InterfaceDecl, *ast.MacroDef:
		// Type-only / compile-time: erased.
		e.pending = nil
	case *ast.EnumDecl:
		e.emitEnum(n)
	case *ast.ClassDecl:
		e.emitClass(n)
	default:
		e.pad()
		e.write("-- unsupported: %T")
		e.nl()
	}
}

func funcNameString(n *ast.FuncName) string {
	// 设计者视角：funcNameString 把 FuncName 拼回 `a.b.c:m` 全名，好比门牌重组——
	// Prefix 点连、Method 冒号接。障碍是 nil 名（匿名）：返 "?" 占位防 panic，
	// 调用方照写 `function ?` 虽怪但不崩（上游 checker 已报错）。候选：报错 vs
	// 占位；选占位，发射永不抛。不变量：nil 恒返 "?"。排查现场：输出含 `function ?`
	// 先查 parser 是否丢名，冒号丢失先查 Method 是否为 nil。
	if n == nil {
		return "?"
	}
	s := ""
	for i, p := range n.Prefix {
		if i > 0 {
			s += "."
		}
		s += p.Name
	}
	if n.Method != nil {
		s += ":" + n.Method.Name
	}
	return s
}

func (e *emitter) emitParams(fn *ast.FuncLit) {
	// 设计者视角：emitParams 只抄“名 + ...”——类型擦除后签名即名录。障碍是冒号
	// 方法的 self：checker 不插 self，Lua 靠 `:` 隐传，此处照原样抄（不加 self）
	// 恰与 `function a:m(...)` 互补。候选：显式补 self vs 照抄；选照抄，少一事。
	// 不变量：输出无类型残留。排查现场：self 重复/缺失先查定义处是否手写 self。
	for i, p := range fn.Params {
		if i > 0 {
			e.write(", ")
		}
		e.write(p.Name.Name)
	}
	if fn.HasVarArg {
		if len(fn.Params) > 0 {
			e.write(", ")
		}
		e.write("...")
	}
}

// emitVarArgPrelude materializes a named rest parameter (`...nums`).
// Lua drops the name, so `local nums = {...}` rebinds it for the body.
//
// 设计者视角：emitVarArgPrelude 是具名 rest 的“挂牌处”——Lua 的 ... 无名，
// 此处首行 `local nums = {...}` 把名字挂回去。障碍是位置：必须在 defer prelude
// 之前（清理闭包要捕获 nums），在 indent 目标层。候选：改 AST 插节点 vs 发射时
// 直写；选直写，不脏 AST。不变量：无名 rest（vaName==nil）零输出。排查现场：
// nums 未定义先查 HasVarArg/VarArgName 是否配对，捕获不到先查 prelude 顺序。
func (e *emitter) emitVarArgPrelude(params []*ast.Param, hasVA bool, vaName *ast.Ident, ind int) {
	if !hasVA || vaName == nil {
		return
	}
	_ = params
	saved := e.indent
	e.indent = ind
	e.pad()
	e.write("local " + vaName.Name + " = {...}")
	e.nl()
	e.indent = saved
}

func (e *emitter) emitEnum(n *ast.EnumDecl) {
	// 设计者视角：emitEnum 是枚举的“拍扁机”——`enum Color{Red="r"}` 拍成
	// `Color = {Red="r"}` 单表。障碍是自增值跟随：数字字面量更新 lastAuto，
	// 遇字符串/复杂式即关 autoActive（后继无值者吐 nil 而非臆测）。候选：全
	// 编译期求值 vs 仅数字跟随；选后者，字符串后自增语义本就模糊。不变量：
	// 输出恒单行表构造。排查现场：自增值跳号先查 Value 是否为 NumberLit，
	// nil 成员先查 autoActive 是否已关闭。
	// `enum Color { Red = "red" }` => `Color = { Red = "red", ... }`
	// Auto values: incrementing integers from 0.
	e.pad()
	e.write(n.Name.Name + " = {")
	lastAuto := -1
	autoActive := true
	for i, m := range n.Members {
		if i > 0 {
			e.write(", ")
		}
		e.write(m.Name.Name + " = ")
		if m.Value == nil {
			if !autoActive {
				e.write("nil")
			} else {
				lastAuto++
				e.write(itoa(lastAuto))
			}
		} else {
			// Try to track auto counter for numeric literals.
			if num, ok := m.Value.(*ast.NumberLit); ok {
				if v, err := parseInt(num.Literal); err == nil {
					lastAuto = v
				} else {
					autoActive = false
				}
			} else if _, ok := m.Value.(*ast.StringLit); ok {
				autoActive = false
			} else {
				autoActive = false
			}
			e.emitExpr(m.Value)
		}
	}
	e.write("}")
	e.nl()
}
