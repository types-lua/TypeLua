package checker

import (
	"github.com/typelua/typelua/internal/ast"
	"github.com/typelua/typelua/internal/diag"
	"github.com/typelua/typelua/internal/token"
	"github.com/typelua/typelua/internal/types"
)

// Class body checking, instantiation, and super calls.

func (c *Checker) checkClass(n *ast.ClassDecl) {
	e := c.cur().lookupType(n.Name.Name)
	if e == nil || e.classDecl != n {
		return // duplicate already reported
	}
	def := c.ensureClassDef(e)
	if def == nil {
		c.recordDefEntry(n.Name.Span(), c.bindVar(n.Name.Name, &Symbol{Name: n.Name.Name, Kind: symVar, Type: types.Any, Span: n.Span()}), e)
		return
	}
	if existing := c.cur().vars[n.Name.Name]; existing != nil && !existing.IsBuiltin {
		c.errorf(diag.RedefinitionOfLocal, n.Name.Span(), "redefinition of local '%s'", n.Name.Name)
	}
	c.recordDefEntry(n.Name.Span(), c.bindVar(n.Name.Name, &Symbol{Name: n.Name.Name, Kind: symVar, Type: def.val, Span: n.Span()}), e)
	for _, m := range n.Members {
		if m.Name == nil || m.Kind == ast.ClassConstructor {
			continue
		}
		kind := "field"
		if m.Kind == ast.ClassMethod {
			kind = "method"
		}
		c.recordMemberDef(m.Name.Span(), e, m.Name.Name, kind)
	}
	if !n.Declared {
		c.checkClassBodies(e, n, def)
	}
}

// checkClassBodies is pass 2: field initializers (inferring unannotated
// fields) and method/constructor bodies with self/super in scope.
func (c *Checker) checkClassBodies(e *typeEntry, n *ast.ClassDecl, def *classBuild) {
	c.pushTParams(n.Generics)
	defer c.popTParams()

	baseInst := def.inst.Base
	var baseVal *types.ClassValue
	if n.Extends != nil {
		// Already resolved during build; re-derive cheaply for context.
		if nt, ok := n.Extends.(*ast.NamedType); ok {
			if be := c.cur().lookupType(nt.Name); be != nil && be.classDecl != nil {
				if bd := c.ensureClassDef(be); bd != nil {
					sub := c.classSubFromNodes(be, nt.Args, n.Extends.Span())
					if v, ok := types.Substitute(bd.val, sub).(*types.ClassValue); ok {
						baseVal = v
					}
				}
			}
		}
	}

	// Field initializers (statics have no self; instance fields do).
	for _, m := range n.Members {
		if m.Kind != ast.ClassField || m.Init == nil {
			continue
		}
		sig := def.sigs[m]
		if sig == nil {
			continue
		}
		if m.IsStatic {
			got := c.checkExpr(m.Init)
			if sig.fieldType != nil {
				if !types.IsAssignable(got, sig.fieldType, c.opt.toTypes()) {
					c.errorf(diag.TypeNotAssignable, m.Init.Span(), "type '%s' is not assignable to type '%s'", got.String(), sig.fieldType.String())
				}
			} else {
				ft := widenInferred(got)
				sig.fieldType = ft
				if entry, ok := def.val.Statics[m.Name.Name]; ok {
					entry.Type = ft
					def.val.Statics[m.Name.Name] = entry
				}
			}
			continue
		}
		c.push()
		c.cur().vars["self"] = &Symbol{Name: "self", Kind: symParam, Type: def.inst, Span: m.Span()}
		c.classStack = append(c.classStack, &classCtx{def: def.inst.Def, name: n.Name.Name, inst: def.inst, base: baseInst, baseVal: baseVal})
		got := c.checkExpr(m.Init)
		want := sig.fieldType
		if want == nil && baseInst != nil {
			// Overriding an inherited field: the base type governs.
			if bf, ok := baseInst.Fields[m.Name.Name]; ok && bf.Access != types.Private && bf.Type != nil {
				want = bf.Type
			}
		}
		if want != nil {
			if !types.IsAssignable(got, want, c.opt.toTypes()) {
				c.errorf(diag.TypeNotAssignable, m.Init.Span(), "type '%s' is not assignable to type '%s'", got.String(), want.String())
			}
		} else {
			ft := widenInferred(got)
			sig.fieldType = ft
			def.inst.Fields[m.Name.Name] = types.ClassField{Type: ft, Readonly: m.IsReadonly, Access: astVisibility(m.Visibility), Owner: def.inst.Def}
		}
		c.classStack = c.classStack[:len(c.classStack)-1]
		c.pop()
	}

	// Method and constructor bodies.
	for _, m := range n.Members {
		if m.Kind == ast.ClassField || m.Body == nil {
			continue
		}
		sig := def.sigs[m]
		if sig == nil || sig.funcType == nil {
			continue
		}
		c.checkClassMethodBody(n, m, sig.funcType, def, baseInst, baseVal)
	}

	// Constructor rules (explicit constructors only; synthesized ones
	// forward to base by construction).
	var ctor *ast.ClassMember
	for _, m := range n.Members {
		if m.Kind == ast.ClassConstructor && m.Body != nil {
			ctor = m
			break
		}
	}
	required := []string{}
	for _, m := range n.Members {
		if m.Kind != ast.ClassField || m.IsStatic || m.Init != nil {
			continue
		}
		if sig := def.sigs[m]; sig != nil && sig.fieldType == types.Nil {
			continue // `x: nil` is trivially satisfied
		}
		required = append(required, m.Name.Name)
	}
	if ctor == nil {
		for _, name := range required {
			c.errorf(diag.FieldNotInitialized, n.Name.Span(), "field '%s' needs an initializer or an explicit constructor", name)
		}
	} else {
		assigned := map[string]bool{}
		collectSelfAssigns(ctor.Body, assigned)
		for _, name := range required {
			if !assigned[name] {
				c.errorf(diag.FieldNotInitialized, ctor.Span(), "constructor does not assign required field '%s'", name)
			}
		}
		if baseInst != nil && baseNeedsConstruction(e, baseInst, baseVal) {
			if !blockHasSuperCall(ctor.Body) {
				c.errorf(diag.DerivedCtorMustCallSuper, ctor.Span(), "constructor of derived class '%s' must call 'super(...)'", n.Name.Name)
			} else if idx := topLevelSuperIndex(ctor.Body); idx >= 0 {
				for i, st := range ctor.Body.Stmts {
					if i >= idx {
						break
					}
					if stmtUsesSelf(st) {
						c.errorf(diag.SelfBeforeSuper, st.Span(), "cannot use 'self' before 'super(...)'")
						break
					}
				}
			}
		}
	}
}

// baseNeedsConstruction reports whether a derived constructor must call super().
func baseNeedsConstruction(e *typeEntry, baseInst *types.Class, baseVal *types.ClassValue) bool {
	_ = e
	if baseVal != nil && baseVal.Constructor != nil {
		for _, p := range baseVal.Constructor.Params {
			if !p.Optional && !types.ContainsNil(p.Type) {
				return true
			}
		}
		if baseVal.Constructor.HasVarArg {
			return false
		}
		// Explicit non-trivial constructor still needs the call so its body runs.
		return len(baseVal.Constructor.Params) > 0
	}
	return false
}

// checkClassMethodBody checks one method/constructor body with self/super.
func (c *Checker) checkClassMethodBody(n *ast.ClassDecl, m *ast.ClassMember, ft *types.Func, def *classBuild, baseInst *types.Class, baseVal *types.ClassValue) {
	isCtor := m.Kind == ast.ClassConstructor
	c.pushTParams(n.Generics)
	defer c.popTParams()
	c.pushTParams(m.Generics)
	defer c.popTParams()
	c.push()
	defer c.pop()
	c.labelStack = append(c.labelStack, map[string]token.Span{})
	defer func() { c.labelStack = c.labelStack[:len(c.labelStack)-1] }()
	c.gotoList = append(c.gotoList, nil)
	defer func() { c.gotoList = c.gotoList[:len(c.gotoList)-1] }()

	for _, p := range m.Params {
		if p.Name.Name == "self" || p.Name.Name == "super" {
			c.errorf(diag.ReservedParamName, p.Name.Span(), "parameter cannot be named '%s' in a method", p.Name.Name)
		}
		var t types.Type = types.Any
		for _, fp := range ft.Params {
			if fp.Name == p.Name.Name {
				t = fp.Type
				break
			}
		}
		c.recordDef(p.Name.Span(), c.bindVar(p.Name.Name, &Symbol{Name: p.Name.Name, Kind: symParam, Type: t, Span: p.Name.Span()}))
	}
	if !m.IsStatic {
		c.cur().vars["self"] = &Symbol{Name: "self", Kind: symParam, Type: def.inst, Span: m.Span()}
	}
	if m.HasVarArg {
		if m.VarArgName != nil {
			restT := types.Type(&types.Array{Elem: types.Any})
			if ft.VarArg != nil {
				restT = &types.Array{Elem: ft.VarArg}
			}
			c.recordDef(m.VarArgName.Span(), c.bindVar(m.VarArgName.Name, &Symbol{Name: m.VarArgName.Name, Kind: symParam, Type: restT, Span: m.VarArgName.Span()}))
		}
		c.varargStack = append(c.varargStack, ft.VarArg)
		if ft.VarArg == nil {
			c.varargStack[len(c.varargStack)-1] = types.Any
		}
	} else {
		c.varargStack = append(c.varargStack, nil)
	}
	defer func() { c.varargStack = c.varargStack[:len(c.varargStack)-1] }()

	if isCtor {
		c.retStack = append(c.retStack, []types.Type{})
	} else if m.ReturnType != nil {
		c.retStack = append(c.retStack, ft.Returns)
	} else {
		c.retStack = append(c.retStack, nil)
	}
	defer func() { c.retStack = c.retStack[:len(c.retStack)-1] }()
	c.inferStack = append(c.inferStack, nil)
	defer func() { c.inferStack = c.inferStack[:len(c.inferStack)-1] }()
	c.asyncStack = append(c.asyncStack, m.IsAsync)
	defer func() { c.asyncStack = c.asyncStack[:len(c.asyncStack)-1] }()

	c.classStack = append(c.classStack, &classCtx{def: def.inst.Def, name: n.Name.Name, inst: def.inst, base: baseInst, baseVal: baseVal, inStatic: m.IsStatic, inConstructor: isCtor, retDepth: len(c.retStack)})
	defer func() { c.classStack = c.classStack[:len(c.classStack)-1] }()

	c.checkBlock(m.Body)
	gotos := c.gotoList[len(c.gotoList)-1]
	labels := c.labelStack[len(c.labelStack)-1]
	for _, g := range gotos {
		if _, ok := labels[g.name]; !ok {
			c.errorf(diag.UndefinedLabel, g.span, "undefined label '%s'", g.name)
		}
	}
}

// checkClassInstantiate checks `Point(...)` / `Point.new(...)`.
func (c *Checker) checkClassInstantiate(cv *types.ClassValue, args []ast.Expr, span token.Span, explicit []types.Type) []types.Type {
	name := "class"
	if cv.Class != nil && cv.Class.Name != "" {
		name = "'" + cv.Class.Name + "'"
	}
	if cv.Abstract {
		c.errorf(diag.InstantiateAbstract, span, "cannot instantiate abstract class %s", name)
		// Don't cascade arity noise on top of the root cause.
		for _, a := range args {
			c.checkExpr(a)
		}
		if cv.Class != nil {
			return []types.Type{cv.Class}
		}
		return []types.Type{types.Any}
	}
	if cv.CtorAccess != types.Public {
		cur := c.currentClass()
		allowed := false
		if cur != nil && cv.Class != nil {
			if cv.CtorAccess == types.Private && cur.def == cv.Class.Def {
				allowed = true
			}
			if cv.CtorAccess == types.Protected && (cur.def == cv.Class.Def || isSubclassOf(cur.inst, cv.Class.Def)) {
				allowed = true
			}
		}
		if !allowed {
			c.errorf(diag.CtorAccessViolation, span, "constructor of %s is %s", name, cv.CtorAccess)
		}
	}
	if cv.Constructor == nil {
		for _, a := range args {
			c.checkExpr(a)
		}
		if cv.Class != nil {
			return []types.Type{cv.Class}
		}
		return []types.Type{types.Any}
	}
	fn := &types.Func{Params: cv.Constructor.Params, HasVarArg: cv.Constructor.HasVarArg, VarArg: cv.Constructor.VarArg, Returns: []types.Type{cv.Class}, TypeParams: cv.Class.TypeParams}
	return c.checkCall(span, fn, args, span, explicit)
}

// checkSuperCall checks `super(...)` (base constructor on self).
func (c *Checker) checkSuperCall(n *ast.CallExpr) []types.Type {
	if len(n.TypeArgs) > 0 {
		c.errorf(diag.GenericArgsInvalid, n.Span(), "explicit type arguments are not allowed on 'super(...)'")
	}
	ctx := c.currentClass()
	if ctx == nil || ctx.base == nil || len(c.retStack) != ctx.retDepth {
		c.errorf(diag.SuperOutsideDerivedMethod, n.Span(), "'super' can only be used inside methods of a derived class")
		for _, a := range n.Args {
			c.checkExpr(a)
		}
		return []types.Type{types.Any}
	}
	if ctx.inStatic {
		c.errorf(diag.SuperMisuse, n.Span(), "'super' cannot be used in a static method (use the base class name)")
		for _, a := range n.Args {
			c.checkExpr(a)
		}
		return []types.Type{types.Any}
	}
	if !ctx.inConstructor {
		c.errorf(diag.SuperMisuse, n.Span(), "'super(...)' can only be called in a constructor (use 'super:method()' in methods)")
		for _, a := range n.Args {
			c.checkExpr(a)
		}
		return []types.Type{types.Any}
	}
	baseCtor := ctx.baseVal.Constructor
	if baseCtor == nil {
		for _, a := range n.Args {
			c.checkExpr(a)
		}
		return []types.Type{types.Void}
	}
	c.checkCall(n.Span(), baseCtor, n.Args, n.Span(), nil)
	return []types.Type{types.Void}
}

// checkSuperMethod checks `super:method(...)` (base method on self).
func (c *Checker) checkSuperMethod(n *ast.MethodCallExpr) []types.Type {
	ctx := c.currentClass()
	if ctx == nil || ctx.base == nil || len(c.retStack) != ctx.retDepth {
		c.errorf(diag.SuperOutsideDerivedMethod, n.Span(), "'super' can only be used inside methods of a derived class")
		for _, a := range n.Args {
			c.checkExpr(a)
		}
		return []types.Type{types.Any}
	}
	if ctx.inStatic {
		c.errorf(diag.SuperMisuse, n.Span(), "'super' cannot be used in a static method (use the base class name)")
		for _, a := range n.Args {
			c.checkExpr(a)
		}
		return []types.Type{types.Any}
	}
	// Walk the base chain for the method (instance methods only).
	for b := ctx.base; b != nil; b = b.Base {
		if f, ok := b.Fields[n.Method.Name]; ok && f.IsMethod {
			if f.Access == types.Private {
				c.errorf(diag.AccessViolation, n.Span(), "method '%s' is private to class '%s'", n.Method.Name, b.Name)
				return []types.Type{types.Any}
			}
			if mt, ok := f.Type.(*types.Func); ok {
				return c.checkCall(n.Span(), mt, n.Args, n.Span(), c.resolveTypeArgs(n.TypeArgs))
			}
			for _, a := range n.Args {
				c.checkExpr(a)
			}
			return []types.Type{types.Any}
		}
	}
	// Static with the same name? Point the user at it.
	if ctx.baseVal != nil {
		if _, ok := ctx.baseVal.Statics[n.Method.Name]; ok {
			c.errorf(diag.StaticViaInstance, n.Span(), "'%s' is static; call it on the base class by name", n.Method.Name)
			for _, a := range n.Args {
				c.checkExpr(a)
			}
			return []types.Type{types.Any}
		}
	}
	c.errorf(diag.NoMethod, n.Span(), "type '%s' has no method '%s'", ctx.base.String(), n.Method.Name)
	for _, a := range n.Args {
		c.checkExpr(a)
	}
	return []types.Type{types.Any}
}
