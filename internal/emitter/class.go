package emitter

import (
	"github.com/typelua/typelua/internal/ast"
)

// 类是土法炼钢：`class Dog extends Animal` 发射为 5.1 元表三件（__index 自指+基表+__call 转 new）。
//
// 设计者视角：本文件是类的“土法炼钢炉”——无 class 关键字的 Lua 靠元表三件套
// （__index 自指 + 基表 __index + __call 转 new）撑起继承，好比用砖木搭出钢
// 结构。障碍是 5.1/5.4 通吃：不用 goto、不用位运算，只用 setmetatable 习语。
// 不变量：每个类必有 constructor（显式或合成），Base.constructor 永可调用。

// classBaseName 脱泛型取运行时基表，如 `extends Box<T>` 只留 Box。
//
// 设计者视角：classBaseName 是泛型擦除的“脱衣机”——`extends Box<T>` 只留 Box
// 作运行时表。障碍是 checker 已拒非命名基类，此处仍防一手：若断言错类型直接
// 返 "" 让输出退化为空基而非 panic。候选：报错 vs 退化；选退化，发射器永不
// 抛错（TypeScript 语义：有错也发射）。不变量：非 NamedType 恒返 ""。排查现场：
// 继承链丢失先查 Extends 是否为泛型具化之外的复杂类型。
func classBaseName(t ast.TypeNode) string {
	nt, ok := t.(*ast.NamedType)
	if !ok {
		return ""
	}
	return nt.Name
}

// 发射形如 `local Dog=setmetatable({},{__index=Animal})`，`Dog.__index=Dog` 自指，`self` 再套一层。
// 不变量：classStack 入栈必配 defer 弹栈；抽象类 new 首行即 error。排查现场：
// 字段先于基类构造执行先查 splitAt 扫描（仅认顶层 ExprStmt super），方法调
// 不到基类先查 __call 是否被重设覆盖。
func (e *emitter) emitClass(n *ast.ClassDecl) {
	if n.Declared {
		e.pending = nil
		return // ambient only
	}
	name := n.Name.Name
	base := ""
	if n.Extends != nil {
		base = classBaseName(n.Extends)
	}
	e.classStack = append(e.classStack, classEmitCtx{name: name, base: base})
	defer func() { e.classStack = e.classStack[:len(e.classStack)-1] }()

	e.pad()
	if base != "" {
		e.write("local " + name + " = setmetatable({}, {__index = " + base + "})")
	} else {
		e.write("local " + name + " = setmetatable({}, {})")
	}
	e.nl()
	e.pad()
	e.write(name + ".__index = " + name)
	e.nl()

	// 排查看 Span/诊断，如 `local x=1` 错圈整句。
	for _, m := range n.Members {
		if m.Kind != ast.ClassField || !m.IsStatic || m.Init == nil {
			continue
		}
		e.pad()
		e.mark(m, "")
		e.write(name + "." + m.Name.Name + " = ")
		e.emitExpr(m.Init)
		e.nl()
	}

	// 构造详见实现，排查看 Span。
	e.pad()
	e.mark(n, name+".new")
	e.write("function " + name + ".new(...)")
	e.nl()
	e.indent++
	e.pad()
	if n.Abstract {
		e.write("error(\"cannot instantiate abstract class '" + name + "'\")")
		e.nl()
		e.pad()
	}
	e.write("local self = setmetatable({}, " + name + ")")
	e.nl()
	e.pad()
	e.write("self:constructor(...)")
	e.nl()
	e.pad()
	e.write("return self")
	e.nl()
	e.indent--
	e.pad()
	e.write("end")
	e.nl()
	// Late-bind __call after `new` exists: mutating (not replacing) the
	// 排查看 Span/诊断，如 `local x=1` 错圈整句。
	// 排查看 Span/诊断，如 `local x=1` 错圈整句。
	e.pad()
	e.write("getmetatable(" + name + ").__call = function(_, ...) return " + name + ".new(...) end")
	e.nl()

	// 构造详见实现，排查看 Span。
	var ctor *ast.ClassMember
	for _, m := range n.Members {
		if m.Kind == ast.ClassConstructor && m.Body != nil {
			ctor = m
			break
		}
	}
	// 排查看 Span/诊断，如 `local x=1` 错圈整句。
	var inits []*ast.ClassMember
	for _, m := range n.Members {
		if m.Kind == ast.ClassField && !m.IsStatic && m.Init != nil {
			inits = append(inits, m)
		}
	}
	if ctor != nil {
		e.pad()
		e.mark(ctor, name+":constructor")
		e.write("function " + name + ":constructor(")
		e.emitClassParams(ctor)
		e.write(")")
		e.nl()
		// 排查看 Span/诊断，如 `local x=1` 错圈整句。
		// 排查看 Span/诊断，如 `local x=1` 错圈整句。
		splitAt := -1
		if base != "" && ctor.Body != nil {
			for i, st := range ctor.Body.Stmts {
				if es, ok := st.(*ast.ExprStmt); ok {
					if call, ok := es.E.(*ast.CallExpr); ok {
						if id, ok := call.Func.(*ast.Ident); ok && id.Name == "super" {
							splitAt = i
							break
						}
					}
				}
			}
		}
		e.indent++
		if ctor.Body != nil {
			e.emitVarArgPrelude(ctor.Params, ctor.HasVarArg, ctor.VarArgName, e.indent)
			if splitAt < 0 {
				e.emitClassInits(name, inits)
			}
			e.pushDeferFrame(ctor.Body)
			e.emitDeferPrelude(e.indent)
			for i, st := range ctor.Body.Stmts {
				e.emitStmt(st)
				if i == splitAt {
					e.emitClassInits(name, inits)
				}
			}
			if ctor.Body.Ret != nil {
				e.emitReturn(ctor.Body.Ret)
			}
			e.emitDeferTrail(ctor.Body, e.indent)
			e.popDeferFrame()
		} else {
			e.emitClassInits(name, inits)
		}
		e.indent--
		e.pad()
		e.write("end")
		e.nl()
	} else {
		e.pad()
		e.mark(n, name+":constructor")
		e.write("function " + name + ":constructor(...)")
		e.nl()
		e.indent++
		if base != "" {
			e.pad()
			e.write(base + ".constructor(self, ...)")
			e.nl()
		}
		e.emitClassInits(name, inits)
		e.indent--
		e.pad()
		e.write("end")
		e.nl()
	}

	// 排查看 Span/诊断，如 `local x=1` 错圈整句。
	for _, m := range n.Members {
		if m.Kind != ast.ClassMethod {
			continue
		}
		e.pad()
		if m.IsStatic {
			e.mark(m, name+"."+m.Name.Name)
			e.write("function " + name + "." + m.Name.Name + "(")
		} else {
			e.mark(m, name+":"+m.Name.Name)
			e.write("function " + name + ":" + m.Name.Name + "(")
		}
		e.emitClassParams(m)
		e.write(")")
		e.nl()
		e.indent++
		e.emitVarArgPrelude(m.Params, m.HasVarArg, m.VarArgName, e.indent)
		if m.Body != nil {
			e.pushDeferFrame(m.Body)
			e.emitDeferPrelude(e.indent)
			e.emitBlock(m.Body, e.indent)
			e.emitDeferTrail(m.Body, e.indent)
			e.popDeferFrame()
		} else {
			e.pad()
			e.write("error(\"abstract method '" + name + "." + m.Name.Name + "' is not implemented\")")
			e.nl()
		}
		e.indent--
		e.pad()
		e.write("end")
		e.nl()
	}
}

func (e *emitter) emitClassInits(className string, inits []*ast.ClassMember) {
	// 设计者视角：emitClassInits 是构造体内的“贴砖队”——按声明序逐行
	// `self.x = <init>`，static 已另行直挂表上，此处只贴实例字段。障碍是空
	// inits：零字段时须零输出而非空行。候选：调用方判空 vs 内部循环自然空；
	// 选后者，零特判。不变量：只写 self. 前缀，不碰类表。排查现场：静态字段
	// 跑进实例先查 Kind/IsStatic 过滤，顺序错乱先查收集序。
	for _, m := range inits {
		e.pad()
		e.mark(m, "")
		e.write("self." + m.Name.Name + " = ")
		e.emitExpr(m.Init)
		e.nl()
	}
}

func (e *emitter) emitClassParams(m *ast.ClassMember) {
	// 设计者视角：emitClassParams 是方法签名的“抄写员”——只抄形参名与 ...，
	// 类型注解全擦，self 靠冒号调用隐传而不占位。障碍是具名 rest（...nums）：
	// 此处只吐 ...，真正的 `local nums = {...}` 留给 emitVarArgPrelude（须在
	// 方法体内首行）。候选：此处一并展开 vs 分两处；选分两处，签名与体各管一摊。
	// 不变量：输出恒为纯名列表，无类型残留。排查现场：具名 rest 为 nil 先查
	// HasVarArg/VarArgName 配对，self 重复先查是否误把 self 写进 Params。
	for i, p := range m.Params {
		if i > 0 {
			e.write(", ")
		}
		e.write(p.Name.Name)
	}
	if m.HasVarArg {
		if len(m.Params) > 0 {
			e.write(", ")
		}
		e.write("...")
	}
}
