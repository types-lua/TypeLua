package emitter

import (
	"strings"

	"github.com/typelua/typelua/internal/ast"
)

// Options for code generation.
type Options struct {
	// Target is "5.4" (default) or "5.1". Under 5.1, `<const>`/`<close>` are stripped.
	Target string
}

func DefaultOptions() Options { return Options{Target: "5.4"} }

// EmitFile renders a parsed file to Lua source (type erasure).
func EmitFile(f *ast.File, opt Options) string {
	e := &emitter{opt: opt}
	e.emitBlock(f.Block, 0)
	out := e.buf.String()
	if out == "" || strings.HasSuffix(out, "\n") {
		return out
	}
	return out + "\n"
}

type emitter struct {
	opt    Options
	buf    strings.Builder
	indent int
}

func (e *emitter) write(s string) { e.buf.WriteString(s) }
func (e *emitter) nl()            { e.buf.WriteString("\n") }
func (e *emitter) pad() {
	for i := 0; i < e.indent; i++ {
		e.buf.WriteString("    ")
	}
}

func (e *emitter) emitBlock(b *ast.Block, ind int) {
	if b == nil {
		return
	}
	saved := e.indent
	e.indent = ind
	for _, st := range b.Stmts {
		e.emitStmt(st)
	}
	if b.Ret != nil {
		e.pad()
		e.write("return")
		if len(b.Ret.Exprs) > 0 {
			e.write(" ")
			for i, x := range b.Ret.Exprs {
				if i > 0 {
					e.write(", ")
				}
				e.emitExpr(x)
			}
		}
		e.nl()
	}
	e.indent = saved
}

func (e *emitter) emitStmt(st ast.Stmt) {
	switch n := st.(type) {
	case *ast.EmptyStmt:
		// Skip (avoid clutter), but keep `;` if it was intentional? Drop.
	case *ast.ExprStmt:
		e.pad()
		e.emitExpr(n.E)
		e.nl()
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
	case *ast.LocalFuncDef:
		e.pad()
		e.write("local function " + n.Name.Name + "(")
		e.emitParams(n.Func)
		e.write(")")
		e.nl()
		e.emitBlock(n.Func.Body, e.indent+1)
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
			e.emitBlock(params.Body, e.indent+1)
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
	case *ast.TypeAliasDecl, *ast.InterfaceDecl:
		// Type-only: erased.
	case *ast.EnumDecl:
		e.emitEnum(n)
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

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	neg := false
	if i < 0 {
		neg = true
		i = -i
	}
	var b [32]byte
	pos := len(b)
	for i > 0 {
		pos--
		b[pos] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		pos--
		b[pos] = '-'
	}
	return string(b[pos:])
}

func parseInt(s string) (int, error) {
	neg := false
	if len(s) > 0 && s[0] == '-' {
		neg = true
		s = s[1:]
	} else if len(s) > 0 && s[0] == '+' {
		s = s[1:]
	}
	// Strip underscores.
	clean := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		if s[i] == '_' {
			continue
		}
		clean = append(clean, s[i])
	}
	n := 0
	for _, ch := range clean {
		if ch < '0' || ch > '9' {
			return 0, errNotInt
		}
		n = n*10 + int(ch-'0')
	}
	if neg {
		n = -n
	}
	return n, nil
}

var errNotInt = errString("not int")

type errString string

func (x errString) Error() string { return string(x) }

func (e *emitter) emitExpr(x ast.Expr) {
	switch n := x.(type) {
	case *ast.Ident:
		e.write(n.Name)
	case *ast.NumberLit:
		e.write(n.Literal)
	case *ast.StringLit:
		e.write(n.Literal)
	case *ast.BoolLit:
		if n.Value {
			e.write("true")
		} else {
			e.write("false")
		}
	case *ast.NilLit:
		e.write("nil")
	case *ast.VarArg:
		e.write("...")
	case *ast.ParenExpr:
		e.write("(")
		e.emitExpr(n.E)
		e.write(")")
	case *ast.TableLit:
		e.write("{")
		for i, f := range n.Fields {
			if i > 0 {
				e.write(", ")
			}
			switch f.Kind {
			case ast.NamedField:
				e.write(f.Key.Name + " = ")
				e.emitExpr(f.Val)
			case ast.IndexedField:
				e.write("[")
				e.emitExpr(f.KeyX)
				e.write("] = ")
				e.emitExpr(f.Val)
			default:
				e.emitExpr(f.Val)
			}
		}
		e.write("}")
	case *ast.FuncLit:
		e.write("function(")
		e.emitParams(n)
		e.write(")")
		// Body on same line if tiny? Always block for simplicity.
		// Inline single-return for readability: if body is just `return x`, emit inline? Keep multiline via temp? Simpler: emit with indent 0 newlines? Use current indent+1 with manual newlines.
		// To avoid indent complexity for inline funcs, emit compact when body small.
		if isSmallBody(n.Body) {
			if n.Body.Ret != nil && len(n.Body.Ret.Exprs) > 0 {
				e.write(" return ")
				for i, x := range n.Body.Ret.Exprs {
					if i > 0 {
						e.write(", ")
					}
					e.emitExpr(x)
				}
				e.write(" end")
			} else if len(n.Body.Stmts) == 0 && (n.Body.Ret == nil || len(n.Body.Ret.Exprs) == 0) {
				e.write(" end")
			} else {
				e.write(" end")
			}
		} else {
			e.nl()
			e.emitBlock(n.Body, e.indent+1)
			e.pad()
			e.write("end")
		}
	case *ast.IndexExpr:
		e.emitExpr(n.Obj)
		e.write("[")
		e.emitExpr(n.Key)
		e.write("]")
	case *ast.FieldExpr:
		e.emitExpr(n.Obj)
		e.write("." + n.Field.Name)
	case *ast.CallExpr:
		e.emitExpr(n.Func)
		e.write("(")
		for i, a := range n.Args {
			if i > 0 {
				e.write(", ")
			}
			e.emitExpr(a)
		}
		e.write(")")
	case *ast.MethodCallExpr:
		e.emitExpr(n.Obj)
		e.write(":" + n.Method.Name + "(")
		for i, a := range n.Args {
			if i > 0 {
				e.write(", ")
			}
			e.emitExpr(a)
		}
		e.write(")")
	case *ast.BinOpExpr:
		e.emitExpr(n.L)
		e.write(" " + binOpString(n.Op) + " ")
		e.emitExpr(n.R)
	case *ast.UnOpExpr:
		e.write(unOpString(n.Op))
		// `not x` needs space; `-x`, `#x`, `~x` don't.
		if n.Op.String() == "not" {
			e.write(" ")
		}
		e.emitExpr(n.X)
	case *ast.CastExpr:
		// Erased.
		e.emitExpr(n.E)
	case *ast.NonNullExpr:
		e.emitExpr(n.E)
	default:
		e.write("nil")
	}
}

func isSmallBody(b *ast.Block) bool {
	if b == nil {
		return true
	}
	return len(b.Stmts) == 0
}

func binOpString(k interface{ String() string }) string {
	s := k.String()
	// `!=` is parsed as alias for `~=`; always emit Lua form.
	if s == "!=" {
		return "~="
	}
	return s
}

func unOpString(k interface{ String() string }) string { return k.String() }
