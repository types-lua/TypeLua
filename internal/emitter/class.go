package emitter

import (
	"github.com/typelua/typelua/internal/ast"
)

// Class runtime emission (metatables, inheritance, constructors).

// classBaseName extracts the runtime table for `extends` (type args erased).
// Non-name bases were already rejected by the checker; emit defensively.
func classBaseName(t ast.TypeNode) string {
	nt, ok := t.(*ast.NamedType)
	if !ok {
		return ""
	}
	return nt.Name
}

// emitClass renders a class to a 5.1-compatible metatable idiom:
//
//	local Dog = setmetatable({}, {__index = Animal})
//	Dog.__index = Dog
//	Dog.count = 0
//	function Dog.new(...)
//	    local self = setmetatable({}, Dog)
//	    self:constructor(...)
//	    return self
//	end
//	setmetatable(Dog, {__call = function(_, ...) return Dog.new(...) end})
//	function Dog:constructor(name) ... end
//
// Field initializers run inside the constructor (after super(), like TS);
// every class gets a constructor so `Base.constructor` always exists.
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

	// Static field initializers (the table already exists).
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

	// Constructor entry point.
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
	// metatable preserves __index, and the closure no longer sits inside
	// the class-defining statement (robust across implementations).
	e.pad()
	e.write("getmetatable(" + name + ").__call = function(_, ...) return " + name + ".new(...) end")
	e.nl()

	// Constructor (explicit or synthesized).
	var ctor *ast.ClassMember
	for _, m := range n.Members {
		if m.Kind == ast.ClassConstructor && m.Body != nil {
			ctor = m
			break
		}
	}
	// Own non-static field initializers, in declaration order.
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
		// Own initializers run right after the top-level super() call,
		// mirroring TS field-initialization order.
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

	// Methods.
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
	for _, m := range inits {
		e.pad()
		e.mark(m, "")
		e.write("self." + m.Name.Name + " = ")
		e.emitExpr(m.Init)
		e.nl()
	}
}

func (e *emitter) emitClassParams(m *ast.ClassMember) {
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
