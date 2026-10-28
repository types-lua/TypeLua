package emitter

import (
	"github.com/typelua/typelua/internal/ast"
)

// 表达式发射与运算符拼写。
//
// 设计者视角：本文件是“直译员”——除 Cast/NonNull/Await 三擦除外，其余几乎一字
// 一译，保证运行时语义与手写 Lua 一致。障碍是 Lua 语法暗礁：函数字面量直接
// 调用须套括号、!= 须译 ~=、not 后须空格。取舍：不做常量折叠/美化，块状一律
// 多行（除小函数单 return 内联），把可读性让给正确性。不变量：nil 输入吐 nil
// （防崩），未知节点吐 nil（永不抛错）。

func itoa(i int) string {
	// 设计者视角：itoa 是零依赖的“小字排”——enum 自增值转字面量，不引 strconv
	// 以减依赖。障碍是 0 与负数：0 须特判（循环不跑），负数先取绝对值再补号。
	// 不变量：itoa(parseInt(s)) 往返一致（十进制无下划线）。排查现场：枚举值
	// 错乱先查 parseInt/itoa 是否配对使用。
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
	// 设计者视角：parseInt 是 enum 自增计数器的“跟随器”——读已写字面量回计数，
	// 容忍前导 +/- 与下划线分隔（1_000）。障碍是十六进制/小数：非纯十进制即
	// 报 errNotInt，调用方随即关 autoActive 转吐 nil/显式值，避免自增值与
	// Lua 语义分叉。候选：strconv 全功能 vs 手写十进制；选手写，语义收敛。
	// 不变量：错则非 nil，调用方必须处理。排查现场：自增值跳号先查字面量是否
	// 含非十进制字符致计数器停摆。
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
//
// 设计者视角：errString 是 parseInt 的“哨兵错”——单值类型省去 errors.New，
// 调用方只判 err==nil 与否。障碍是哨兵须包级唯一：var errNotInt 单例，对比
// 用 == 即判。不变量：Error() 恒返原文。排查现场：误吞他错先查是否把非
// errNotInt 也当计数停摆处理。
func (x errString) Error() string { return string(x) }

func (e *emitter) emitExpr(x ast.Expr) {
	// 设计者视角：emitExpr 是表达式的“大分发”——30+ 节点类型各走一支，擦除类
	// （Cast/NonNull/Await）直透内层，super/super:m 借 curClass 改写基调，
	// FuncLit 调用套括号防语法错。障碍是小函数体内联与 defer/vararg 前导的
	// 互斥：需前导时强制多行，否则单 return 可 `return ... end` 同行。候选：
	// 表驱动 vs switch；选 switch，各支长度悬殊、表装不下。不变量：永不写
	// 类型残留；nil 输入不 panic。排查现场：super 未改写先查 curClass，调用栈
	// 溢出先查 ParenExpr 是否无界递归。
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
	// 设计者视角：isSmallBody 是内联的“门禁”——仅无语句块可同行（含单 return
	// 由上层再判），有任一语句即多行。障碍是误判多语句为小体会把分号逻辑挤
	// 成一行难读；故判据极严（len==0）。候选：更激进内联 vs 保守；选保守，
	// 内联只为可读性、不为性能。不变量：nil 返 true（空体）。排查现场：函数体
	// 换行异常先查 needsPrelude 是否强制多行覆盖此处。
	if b == nil {
		return true
	}
	return len(b.Stmts) == 0
}

func binOpString(k interface{ String() string }) string {
	// 设计者视角：binOpString 是方言的“译名表”——parser 容忍 !=，Lua 只认 ~=，
	// 此处统一翻译，其余原样。障碍是接口参数：传 token.Kind 之类 Stringer，
	// 不绑具体类型以容未来操作符。候选：parser 层归一 vs 发射层译；选发射层，
	// AST 保留原文利于诊断。不变量：!= 恒出 ~=。排查现场：输出含 != 先查此
	// 翻译是否被绕过（如手拼字符串）。
	s := k.String()
	// `!=` 解析为 `~=` 的别名；始终发射 Lua 形式。
	if s == "!=" {
		return "~="
	}
	return s
}

// unOpString 是单目符的直通管：not/-/~/ # 原样输出，空格由调用方按 `not` 特判补。
//
// 设计者视角：保留此函数为对称扩展点（若未来收敛操作符只改一处）。障碍是 not
// 后缺空格会拼成 `notx` 语法错，故 emitExpr 内特判补空格。不变量：恒等翻译。
// 排查现场：`notx` 粘连先查空格特判是否被删。
func unOpString(k interface{ String() string }) string { return k.String() }
