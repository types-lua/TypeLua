package checker

import (
	"strings"

	"github.com/typelua/typelua/internal/ast"
	"github.com/typelua/typelua/internal/token"
	"github.com/typelua/typelua/internal/types"
)

// Class definitions: building, inheritance, overrides, visibility.

// ---------- classes ----------

func classParamNames(gs []*ast.GenericParam) []string {
	var out []string
	for _, g := range gs {
		out = append(out, g.Name.Name)
	}
	return out
}

// isIdentityClassArgs reports whether resolved args are exactly the entry's
// own type parameters (self-reference to the current instantiation).
func isIdentityClassArgs(e *typeEntry, sargs []types.Type) bool {
	if len(sargs) != len(e.params) {
		return len(sargs) == 0 && len(e.params) == 0
	}
	for i, p := range e.params {
		gp, ok := sargs[i].(*types.GenericParam)
		if !ok || gp.Name != p.Name.Name {
			return false
		}
	}
	return true
}

// classSubFromTypes builds the param->arg substitution with arity,
// default and constraint diagnostics.
func (c *Checker) classSubFromTypes(e *typeEntry, tys []types.Type, span token.Span) map[string]types.Type {
	sub := map[string]types.Type{}
	if len(tys) > len(e.params) {
		c.errorf("TL3107", span, "type '%s' expects %d argument(s), got %d", e.name, len(e.params), len(tys))
	}
	for i, p := range e.params {
		var a types.Type
		if i < len(tys) {
			a = tys[i]
		} else if p.Default != nil {
			a = c.resolveType(p.Default)
		} else {
			a = types.Unknown
		}
		if p.Constraint != nil {
			con := c.resolveType(p.Constraint)
			if !types.IsAssignable(a, con, c.opt.toTypes()) {
				c.errorf("TL3108", span, "type argument '%s' does not satisfy constraint '%s'", a.String(), con.String())
			}
		}
		sub[p.Name.Name] = a
	}
	return sub
}

func (c *Checker) classSubFromNodes(e *typeEntry, nodes []ast.TypeNode, span token.Span) map[string]types.Type {
	var tys []types.Type
	for _, n := range nodes {
		tys = append(tys, c.resolveType(n))
	}
	return c.classSubFromTypes(e, tys, span)
}

func (c *Checker) currentClass() *classCtx {
	if len(c.classStack) == 0 {
		return nil
	}
	return c.classStack[len(c.classStack)-1]
}

// checkAccess enforces private/protected visibility. Public (or non-class)
// members are always accessible.
func (c *Checker) checkAccess(acc types.Access, owner *types.ClassDef, sp token.Span, what string) bool {
	if acc == types.Public || owner == nil {
		return true
	}
	cur := c.currentClass()
	if cur == nil {
		c.errorf("TL3310", sp, "%s is %s and cannot be accessed outside the class body", what, acc)
		return false
	}
	if acc == types.Private {
		if cur.def == owner {
			return true
		}
		c.errorf("TL3310", sp, "%s is private to class '%s'", what, owner.Name)
		return false
	}
	if cur.def == owner || isSubclassOf(cur.inst, owner) {
		return true
	}
	c.errorf("TL3310", sp, "%s is protected and cannot be accessed here", what)
	return false
}

func isSubclassOf(inst *types.Class, owner *types.ClassDef) bool {
	seen := map[*types.Class]bool{}
	for cl := inst; cl != nil; cl = cl.Base {
		if seen[cl] {
			return false
		}
		seen[cl] = true
		if cl.Def == owner {
			return true
		}
	}
	return false
}

// ensureClassDef builds (once) the checked definition form of a class:
// instance shape with generic placeholders intact for call-site inference.
func (c *Checker) ensureClassDef(e *typeEntry) *classBuild {
	if e.classBuild != nil {
		return e.classBuild
	}
	if e.classDecl == nil || e.resolving {
		return nil
	}
	e.resolving = true
	if e.classDef == nil {
		e.classDef = &types.ClassDef{Name: e.name}
	}
	b := c.buildClassDef(e)
	e.resolving = false
	for _, p := range e.classPatches {
		p()
	}
	e.classPatches = nil
	if b != nil {
		e.classBuild = b
	}
	return b
}

// resolveBaseRef resolves an `extends` clause to an instantiated base
// instance plus its (substituted) class value for statics/constructor.
func (c *Checker) resolveBaseRef(node ast.TypeNode, selfName string) (*types.Class, *types.ClassValue) {
	nt, ok := node.(*ast.NamedType)
	if !ok || strings.Contains(nt.Name, ".") {
		c.errorf("TL3131", node.Span(), "extends must name a class (e.g. `extends Animal`)")
		return nil, nil
	}
	e := c.cur().lookupType(nt.Name)
	if e == nil || e.classDecl == nil {
		bt := c.resolveType(node)
		if _, ok := bt.(*types.Class); !ok && !isAnyLike(bt) {
			c.errorf("TL3131", node.Span(), "class can only extend another class, got '%s'", bt.String())
		}
		return nil, nil
	}
	def := c.ensureClassDef(e)
	if def == nil {
		c.errorf("TL3132", node.Span(), "cyclic inheritance involving '%s'", nt.Name)
		return nil, nil
	}
	sub := c.classSubFromNodes(e, nt.Args, node.Span())
	inst, _ := types.Substitute(def.inst, sub).(*types.Class)
	val, _ := types.Substitute(def.val, sub).(*types.ClassValue)
	if inst.Def == e.classDef && e.name == selfName && len(nt.Args) == 0 {
		// Direct self-extension is reported by the caller chain walk;
		// keep the reference so the walk can see it.
	}
	return inst, val
}

// classMemberSig is the resolved signature of one member (pass 1 output).
type classMemberSig struct {
	member    *ast.ClassMember
	fieldType types.Type // fields: declared type (nil => infer in pass 2)
	funcType  *types.Func
}

func astVisibility(v ast.Visibility) types.Access {
	switch v {
	case ast.Private:
		return types.Private
	case ast.Protected:
		return types.Protected
	}
	return types.Public
}

// buildClassDef resolves signatures (no bodies): base chain, flattened
// members, statics, constructor, override and implements checks.
func (c *Checker) buildClassDef(e *typeEntry) *classBuild {
	n := e.classDecl
	def := e.classDef
	c.pushTParams(n.Generics)
	defer c.popTParams()

	shell := &types.Class{Def: def, Name: n.Name.Name, Params: classParamNames(n.Generics), Fields: map[string]types.ClassField{}, IsAbstract: n.Abstract}
	// Definition-form args are the params themselves so substitution
	// (`Box.new(v)` inside `static wrap<U>`) tracks instantiations.
	for _, pn := range shell.Params {
		shell.Args = append(shell.Args, &types.GenericParam{Name: pn})
	}
	// Resolved declarations for explicit-argument checking on `new`.
	for _, g := range n.Generics {
		tp := &types.GenericParam{Name: g.Name.Name}
		if g.Constraint != nil {
			tp.Constraint = c.resolveType(g.Constraint)
		}
		if g.Default != nil {
			tp.Default = c.resolveType(g.Default)
		}
		shell.TypeParams = append(shell.TypeParams, tp)
	}
	e.classShell = shell

	// Duplicate and reserved member names.
	seen := map[string]bool{}
	processed := map[string]bool{}
	var ctorMember *ast.ClassMember
	for _, m := range n.Members {
		if seen[m.Name.Name] {
			c.errorf("TL3320", m.Span(), "duplicate class member '%s'", m.Name.Name)
			continue
		}
		seen[m.Name.Name] = true
		if m.Kind == ast.ClassConstructor {
			if ctorMember != nil {
				c.errorf("TL3321", m.Span(), "duplicate constructor")
				continue
			}
			ctorMember = m
		}
		if m.Name.Name == "new" || m.Name.Name == "__index" {
			c.errorf("TL3322", m.Name.Span(), "'%s' is reserved by the class runtime", m.Name.Name)
			continue
		}
		if m.Kind == ast.ClassField && m.Name.Name == "constructor" {
			c.errorf("TL3322", m.Name.Span(), "'constructor' must be declared as a method")
			continue
		}
		if m.Kind == ast.ClassConstructor && m.IsStatic {
			c.errorf("TL3323", m.Span(), "constructor cannot be 'static'")
		}
		if m.Kind == ast.ClassConstructor && m.IsReadonly {
			c.errorf("TL3323", m.Span(), "constructor cannot be 'readonly'")
		}
		if m.Kind == ast.ClassConstructor && m.IsAbstract {
			c.errorf("TL3323", m.Span(), "constructor cannot be 'abstract'")
		}
		if m.Kind == ast.ClassMethod && m.IsReadonly {
			c.errorf("TL3323", m.Span(), "methods cannot be 'readonly'")
		}
		if m.IsAbstract && m.Kind == ast.ClassField {
			// Parser already reports TL2037; don't cascade.
		} else if m.IsAbstract && !n.Abstract && !n.Declared {
			c.errorf("TL3324", m.Span(), "abstract member '%s' requires an 'abstract class'", m.Name.Name)
		}
		if m.IsAbstract && m.Visibility == ast.Private {
			c.errorf("TL3324", m.Span(), "abstract member '%s' cannot be private", m.Name.Name)
		}
		processed[m.Name.Name] = true
	}

	// Base class.
	var baseInst *types.Class
	var baseVal *types.ClassValue
	if n.Extends != nil {
		baseInst, baseVal = c.resolveBaseRef(n.Extends, n.Name.Name)
		// Indirect cycles: walk with a visited set so a malformed chain
		// can never hang the checker.
		seenChain := map[*types.Class]bool{shell: true}
		for b := baseInst; b != nil; b = b.Base {
			if seenChain[b] {
				c.errorf("TL3130", n.Extends.Span(), "cyclic inheritance involving class '%s'", n.Name.Name)
				baseInst, baseVal = nil, nil
				break
			}
			seenChain[b] = true
			if b.Def == def {
				c.errorf("TL3130", n.Extends.Span(), "class '%s' cannot extend itself", n.Name.Name)
				baseInst, baseVal = nil, nil
				break
			}
		}
	}
	shell.Base = baseInst
	if baseInst != nil {
		for name, f := range baseInst.Fields {
			if f.Access == types.Private {
				continue
			}
			shell.Fields[name] = f
		}
	}
	if baseVal != nil && baseVal.CtorAccess == types.Private {
		c.errorf("TL3325", n.Extends.Span(), "cannot extend class '%s' with a private constructor", baseInst.Name)
	}

	statics := map[string]types.ClassField{}
	if baseVal != nil {
		for name, f := range baseVal.Statics {
			if f.Access == types.Private {
				continue
			}
			statics[name] = f
		}
	}

	sigs := map[*ast.ClassMember]*classMemberSig{}
	ownInstance := map[string]types.ClassField{}
	ownStatics := map[string]types.ClassField{}

	memberFunc := func(m *ast.ClassMember) *types.Func {
		c.pushTParams(m.Generics)
		defer c.popTParams()
		var params []types.FuncParam
		for _, p := range m.Params {
			var t types.Type = types.Any
			if p.Type != nil {
				t = c.resolveType(p.Type)
			} else if c.opt.NoImplicitAny {
				c.errorf("TL3040", p.Span(), "parameter '%s' implicitly has 'any' type", p.Name.Name)
			}
			params = append(params, types.FuncParam{Name: p.Name.Name, Type: t})
		}
		var va types.Type
		if m.HasVarArg {
			if m.VarArgType != nil {
				va = c.resolveType(m.VarArgType)
				if arr, ok := va.(*types.Array); ok {
					va = arr.Elem
				}
			} else {
				va = types.Any
			}
		}
		var rets []types.Type
		if m.ReturnType != nil {
			rets = flattenReturns(c.resolveType(m.ReturnType))
			if rets == nil {
				rets = []types.Type{}
			}
		} else {
			rets = []types.Type{types.Any}
		}
		tps := make([]*types.GenericParam, len(m.Generics))
		for i, g := range m.Generics {
			tp := &types.GenericParam{Name: g.Name.Name}
			if g.Constraint != nil {
				tp.Constraint = c.resolveType(g.Constraint)
			}
			if g.Default != nil {
				tp.Default = c.resolveType(g.Default)
			}
			tps[i] = tp
		}
		return &types.Func{Params: params, HasVarArg: m.HasVarArg, VarArg: va, Returns: rets, TypeParams: tps, IsAsync: m.IsAsync}
	}

	for _, m := range n.Members {
		if !processed[m.Name.Name] {
			continue
		}
		// Only the first declaration of a name counts (dupes reported above).
		if _, done := sigs[m]; done {
			continue
		}
		// Mark first occurrence: find it by pointer identity against members.
		first := true
		for _, o := range n.Members {
			if o == m {
				break
			}
			if o.Name.Name == m.Name.Name {
				first = false
				break
			}
		}
		if !first {
			continue
		}
		acc := astVisibility(m.Visibility)
		switch m.Kind {
		case ast.ClassField:
			var ft types.Type
			if m.FieldType != nil {
				ft = c.resolveType(m.FieldType)
			}
			cf := types.ClassField{Type: ft, Readonly: m.IsReadonly, Access: acc, Owner: def}
			sigs[m] = &classMemberSig{member: m, fieldType: ft}
			if m.IsStatic {
				statics[m.Name.Name] = cf
				ownStatics[m.Name.Name] = cf
			} else {
				shell.Fields[m.Name.Name] = cf
				ownInstance[m.Name.Name] = cf
			}
		case ast.ClassMethod:
			ft := memberFunc(m)
			cf := types.ClassField{Type: ft, Access: acc, IsMethod: true, Abstract: m.IsAbstract, Owner: def}
			sigs[m] = &classMemberSig{member: m, funcType: ft}
			if m.IsStatic {
				statics[m.Name.Name] = cf
				ownStatics[m.Name.Name] = cf
			} else {
				shell.Fields[m.Name.Name] = cf
				ownInstance[m.Name.Name] = cf
			}
		case ast.ClassConstructor:
			sigs[m] = &classMemberSig{member: m, funcType: memberFunc(m)}
		}
	}

	// Override compatibility against the direct base.
	if baseInst != nil {
		for name, sub := range ownInstance {
			if bf, ok := baseInst.Fields[name]; ok && bf.Access != types.Private {
				checkMemberOverride(c, ownMemberSpan(n, name, false), name, sub, bf)
			}
		}
	}
	if baseVal != nil {
		for name, sub := range ownStatics {
			if bf, ok := baseVal.Statics[name]; ok && bf.Access != types.Private {
				checkMemberOverride(c, ownMemberSpan(n, name, true), name, sub, bf)
			}
		}
	}

	// Abstract members must be implemented by concrete classes.
	// (Present-but-incompatible members are already reported by the
	// override check above, so only missing/still-abstract ones remain.)
	if !n.Abstract && !n.Declared && baseInst != nil {
		for name, bf := range baseInst.Fields {
			if !bf.Abstract || bf.Access == types.Private {
				continue
			}
			sub, ok := ownInstance[name]
			if !ok || sub.Abstract {
				c.errorf("TL3351", n.Name.Span(), "class '%s' must implement abstract member '%s'", n.Name.Name, name)
			}
		}
	}

	// Implements: structural check of public members.
	for _, impl := range n.Implements {
		t := c.resolveType(impl)
		io, ok := t.(*types.Object)
		if !ok {
			if !isAnyLike(t) {
				c.errorf("TL3352", impl.Span(), "class can only implement an interface or object type, got '%s'", t.String())
			}
			continue
		}
		if !types.IsAssignable(shell.ObjectView(), io, c.opt.toTypes()) {
			c.errorf("TL3352", impl.Span(), "class '%s' does not implement '%s'", n.Name.Name, t.String())
		}
	}

	// Constructor value (always present so `Base.constructor` exists).
	var ctorFunc *types.Func
	ctorAccess := types.Public
	if ctorMember != nil {
		ctorFunc = sigs[ctorMember].funcType
		ctorAccess = astVisibility(ctorMember.Visibility)
		// Constructor params are the value; returns are unused.
		ctorFunc = &types.Func{Params: ctorFunc.Params, HasVarArg: ctorFunc.HasVarArg, VarArg: ctorFunc.VarArg}
	} else if n.Declared {
		ctorFunc = &types.Func{HasVarArg: true, VarArg: types.Any}
	} else if baseVal != nil && baseVal.Constructor != nil {
		ctorFunc = baseVal.Constructor
		ctorAccess = baseVal.CtorAccess
	} else {
		ctorFunc = &types.Func{}
	}

	val := &types.ClassValue{Class: shell, Constructor: ctorFunc, CtorAccess: ctorAccess, Statics: statics, Abstract: n.Abstract}
	return &classBuild{inst: shell, val: val, sigs: sigs}
}

func accessRank(a types.Access) int {
	switch a {
	case types.Public:
		return 2
	case types.Protected:
		return 1
	}
	return 0
}

func checkMemberOverride(c *Checker, sp token.Span, name string, sub, base types.ClassField) {
	kind := "field"
	if sub.IsMethod {
		kind = "method"
	}
	if sub.IsMethod != base.IsMethod {
		c.errorf("TL3350", sp, "cannot override %s '%s' with a %s", fieldKind(base), name, kind)
		return
	}
	if sub.Type != nil && base.Type != nil && !types.IsAssignable(sub.Type, base.Type, c.opt.toTypes()) {
		c.errorf("TL3350", sp, "%s '%s' is incompatible with the base declaration", kind, name)
	}
	if accessRank(sub.Access) < accessRank(base.Access) {
		c.errorf("TL3350", sp, "%s '%s' cannot reduce visibility to %s", kind, name, sub.Access)
	}
}

// ownMemberSpan finds the declaration span of an own member for diagnostics.
func ownMemberSpan(n *ast.ClassDecl, name string, isStatic bool) token.Span {
	for _, m := range n.Members {
		if m.Name.Name == name && m.IsStatic == isStatic && m.Kind != ast.ClassConstructor {
			return m.Span()
		}
	}
	return n.Span()
}

func fieldKind(f types.ClassField) string {
	if f.IsMethod {
		return "method"
	}
	return "field"
}
