package emitter

import (
	"github.com/typelua/typelua/internal/ast"
)

// 表达式发射与运算符拼写。

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
	// 去掉下划线。
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

// Error 返回 errString 的文本。
func (x errString) Error() string { return string(x) }

func (e *emitter) emitExpr(x ast.Expr) {
	if x == nil {
		e.write("nil")
		return
	}
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
		// 小函数体同行？为简单起见一律块状。
		// 单返回内联以提高可读性，除非具名 rest
		// 参数需要重绑前导（强制多行）。
		needsPrelude := n.HasVarArg && n.VarArgName != nil
		if isSmallBody(n.Body) && !needsPrelude {
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
			e.emitVarArgPrelude(n.Params, n.HasVarArg, n.VarArgName, e.indent+1)
			e.pushDeferFrame(n.Body)
			e.emitDeferPrelude(e.indent + 1)
			e.emitBlock(n.Body, e.indent+1)
			e.emitDeferTrail(n.Body, e.indent+1)
			e.popDeferFrame()
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
		// 派生方法内的 `super(...)` 基于基类构造。
		if id, ok := n.Func.(*ast.Ident); ok && id.Name == "super" {
			if cc := e.curClass(); cc != nil && cc.base != "" {
				e.write(cc.base + ".constructor(self")
				for _, a := range n.Args {
					e.write(", ")
					e.emitExpr(a)
				}
				e.write(")")
				break
			}
		}
		// Lua 要求函数字面量调用前加括号：
		// `(function() end)()`，而非 `function() end()`。
		if _, ok := n.Func.(*ast.FuncLit); ok {
			e.write("(")
			e.emitExpr(n.Func)
			e.write(")")
		} else {
			e.emitExpr(n.Func)
		}
		e.write("(")
		for i, a := range n.Args {
			if i > 0 {
				e.write(", ")
			}
			e.emitExpr(a)
		}
		e.write(")")
	case *ast.MethodCallExpr:
		// `super:m(...)` 以当前 self 分发到基类。
		if id, ok := n.Obj.(*ast.Ident); ok && id.Name == "super" {
			if cc := e.curClass(); cc != nil && cc.base != "" {
				e.write(cc.base + "." + n.Method.Name + "(self")
				for _, a := range n.Args {
					e.write(", ")
					e.emitExpr(a)
				}
				e.write(")")
				break
			}
		}
		if _, ok := n.Obj.(*ast.FuncLit); ok {
			e.write("(")
			e.emitExpr(n.Obj)
			e.write(")")
		} else {
			e.emitExpr(n.Obj)
		}
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
		// `not x` 需要空格；`-x`、`#x`、`~x` 不需要。
		if n.Op.String() == "not" {
			e.write(" ")
		}
		e.emitExpr(n.X)
	case *ast.CastExpr:
		// 擦除。
		e.emitExpr(n.E)
	case *ast.NonNullExpr:
		e.emitExpr(n.E)
	case *ast.AwaitExpr:
		// 擦除：内层调用在调度器中自行 yield。
		e.emitExpr(n.X)
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
	// `!=` 解析为 `~=` 的别名；始终发射 Lua 形式。
	if s == "!=" {
		return "~="
	}
	return s
}

func unOpString(k interface{ String() string }) string { return k.String() }
