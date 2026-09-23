package emitter

import (
	"github.com/typelua/typelua/internal/ast"
)

// Statement emission (locals, control flow, enums, functions).

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
