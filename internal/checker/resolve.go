package checker

import (
	"strconv"
	"strings"

	"github.com/typelua/typelua/internal/ast"
	"github.com/typelua/typelua/internal/token"
	"github.com/typelua/typelua/internal/types"
)

// Type-node resolution (named, table, function, conditional, mapped, keyof, ...).

// ---------- type resolution ----------

func (c *Checker) resolveType(n ast.TypeNode) types.Type {
	return c.resolveTypeWithSub(n, map[string]types.Type{})
}

func (c *Checker) resolveTypeWithSub(n ast.TypeNode, sub map[string]types.Type) types.Type {
	if n == nil {
		// Recovery artifact (parser already reported); never crash.
		return types.Any
	}
	switch t := n.(type) {
	case *ast.NamedType:
		return c.resolveNamed(t, sub)
	case *ast.LiteralType:
		return c.resolveLiteral(t)
	case *ast.ArrayType:
		return &types.Array{Elem: c.resolveTypeWithSub(t.Elem, sub)}
	case *ast.UnionType:
		var ms []types.Type
		for _, m := range t.Types {
			ms = append(ms, c.resolveTypeWithSub(m, sub))
		}
		return types.NewUnion(ms)
	case *ast.IntersectionType:
		var ms []types.Type
		for _, m := range t.Types {
			ms = append(ms, c.resolveTypeWithSub(m, sub))
		}
		return types.NewIntersection(ms)
	case *ast.OptionalType:
		e := c.resolveTypeWithSub(t.Elem, sub)
		return types.NewUnion([]types.Type{e, types.Nil})
	case *ast.ParenType:
		return c.resolveTypeWithSub(t.Elem, sub)
	case *ast.TupleType:
		var es []types.Type
		for _, e := range t.Elems {
			es = append(es, c.resolveTypeWithSub(e, sub))
		}
		return &types.Tuple{Elems: es}
	case *ast.TableType:
		return c.resolveTableType(t, sub)
	case *ast.FuncType:
		return c.resolveFuncType(t, sub)
	case *ast.TypeofType:
		return c.resolveTypeof(t)
	case *ast.KeyofType:
		e := c.resolveTypeWithSub(t.Elem, sub)
		return c.resolveKeyof(e, t.Span())
	case *ast.MappedType:
		return c.resolveMapped(t, sub)
	case *ast.IndexedAccessType:
		return c.resolveIndexedAccess(t, sub)
	case *ast.InferType:
		if c.extendsDepth == 0 {
			c.errorf("TL3601", t.Span(), "'infer' is only allowed in the extends clause of a conditional type")
			return types.Any
		}
		return &types.InferParam{Name: t.Name.Name}
	case *ast.ConditionalType:
		return c.resolveConditional(t, sub)
	default:
		c.errorf("TL3100", n.Span(), "unsupported type node %T", n)
		return types.Any
	}
}

func (c *Checker) resolveNamed(t *ast.NamedType, sub map[string]types.Type) types.Type {
	// Primitive lookup (unqualified only).
	if len(t.Args) == 0 {
		switch t.Name {
		case "any":
			return types.Any
		case "unknown":
			return types.Unknown
		case "never":
			return types.Never
		case "void":
			return types.Void
		case "nil":
			return types.Nil
		case "boolean":
			return types.Boolean
		case "number":
			return types.Number
		case "integer":
			return types.Integer
		case "float":
			return types.Float
		case "string":
			return types.String
		case "table":
			return types.Table
		case "thread":
			return types.Thread
		case "userdata":
			return types.Userdata
		case "function":
			return types.Function
		}
	}
	// Qualified names: not supported in v1 (modules).
	if strings.Contains(t.Name, ".") && t.Name != "..." {
		c.errorf("TL3101", t.Span(), "qualified type '%s' is not supported", t.Name)
		return types.Any
	}
	// `...T` rest marker (tuple patterns, vararg positions).
	if t.Name == "..." {
		if len(t.Args) != 1 {
			c.errorf("TL3102", t.Span(), "'...' expects 1 type argument, got %d", len(t.Args))
			return types.Any
		}
		return &types.Ref{Name: "...", Args: []types.Type{c.resolveTypeWithSub(t.Args[0], sub)}}
	}
	// Generic param substitution.
	if s, ok := sub[t.Name]; ok && len(t.Args) == 0 {
		return s
	}
	if gp := c.lookupTParam(t.Name); gp != nil && len(t.Args) == 0 {
		if s, ok := sub[t.Name]; ok {
			return s
		}
		// Return a copy to avoid aliasing.
		return &types.GenericParam{Name: gp.Name, Constraint: gp.Constraint, Default: gp.Default}
	}
	// Special generics: map<K,V>, Array<T>, table<K,V>?
	if t.Name == "map" || t.Name == "table" {
		if len(t.Args) == 0 {
			return types.Table
		}
		if len(t.Args) != 2 {
			c.errorf("TL3102", t.Span(), "'%s' expects 2 type arguments, got %d", t.Name, len(t.Args))
			return types.Table
		}
		return &types.Map{Key: c.resolveTypeWithSub(t.Args[0], sub), Value: c.resolveTypeWithSub(t.Args[1], sub)}
	}
	if t.Name == "Array" || t.Name == "array" {
		if len(t.Args) != 1 {
			c.errorf("TL3102", t.Span(), "'Array' expects 1 type argument, got %d", len(t.Args))
			return types.Any
		}
		return &types.Array{Elem: c.resolveTypeWithSub(t.Args[0], sub)}
	}
	// User type.
	e := c.cur().lookupType(t.Name)
	if e == nil {
		c.errorf("TL3103", t.Span(), "undefined type '%s'", t.Name)
		return types.Any
	}
	c.recordTypeUse(t.Span(), e)
	// Self-reference while a class definition is being built: bind to the
	// shell so recursive types (e.g. `next: Box<T> | nil`) keep working.
	// The shell is filled in place, so later reads see complete members.
	if e.classDecl != nil && e.resolving && e.classShell != nil {
		var sargs []types.Type
		for _, a := range t.Args {
			sargs = append(sargs, c.resolveTypeWithSub(a, sub))
		}
		if isIdentityClassArgs(e, sargs) {
			return e.classShell
		}
		inst := &types.Class{Def: e.classDef, Name: e.name, Args: sargs, Params: classParamNames(e.params), Fields: map[string]types.ClassField{}}
		sub2 := c.classSubFromTypes(e, sargs, t.Span())
		e.classPatches = append(e.classPatches, func() {
			if full, ok := types.Substitute(e.classShell, sub2).(*types.Class); ok {
				inst.Fields = full.Fields
				inst.Base = full.Base
			}
		})
		return inst
	}
	var args []types.Type
	for _, a := range t.Args {
		args = append(args, c.resolveTypeWithSub(a, sub))
	}
	return c.resolveTypeEntryWithArgs(e, args, t.Span())
}

func (c *Checker) resolveLiteral(t *ast.LiteralType) types.Type {
	v := strings.TrimSpace(t.Value)
	if v == "true" || v == "false" {
		return &types.Literal{Value: v, Base: types.Boolean}
	}
	if v == "nil" {
		return types.Nil
	}
	if strings.HasPrefix(v, "\"") || strings.HasPrefix(v, "'") || strings.HasPrefix(v, "[") {
		return &types.Literal{Value: v, Base: types.String}
	}
	// Number literal.
	return &types.Literal{Value: v, Base: types.Number}
}

func (c *Checker) resolveTableType(t *ast.TableType, sub map[string]types.Type) types.Type {
	// Push dummy tparams? Table types don't introduce params.
	obj := &types.Object{Fields: map[string]types.Field{}}
	for _, f := range t.Fields {
		if f.IsIndexer {
			obj.Indexers = append(obj.Indexers, types.Indexer{Key: c.resolveTypeWithSub(f.IndexKey, sub), Value: c.resolveTypeWithSub(f.IndexVal, sub)})
			continue
		}
		var ft types.Type
		if f.IsMethod && f.MethodSig != nil {
			ft = c.resolveTypeWithSub(f.MethodSig, sub)
		} else {
			ft = c.resolveTypeWithSub(f.Type, sub)
		}
		obj.Fields[f.Name] = types.Field{Type: ft, Optional: f.Optional, Readonly: f.Readonly}
	}
	return obj
}

func (c *Checker) resolveFuncType(t *ast.FuncType, sub map[string]types.Type) types.Type {
	c.pushTParams(t.Generics)
	defer c.popTParams()
	var ps []types.FuncParam
	for _, p := range t.Params {
		var pt types.Type = types.Any
		if p.Type != nil {
			pt = c.resolveTypeWithSub(p.Type, sub)
		}
		name := ""
		if p.Name != nil {
			name = p.Name.Name
		}
		if p.IsVarArg {
			// Vararg param in func type: treat as vararg, not regular param.
			continue
		}
		ps = append(ps, types.FuncParam{Name: name, Type: pt, Optional: p.Optional})
	}
	var va types.Type
	hasVA := t.HasVarArg
	if t.VarArgType != nil {
		va = c.resolveTypeWithSub(t.VarArgType, sub)
		hasVA = true
		if arr, ok := va.(*types.Array); ok {
			va = arr.Elem
		}
	}
	// Also check Params for IsVarArg (from tryParse).
	for _, p := range t.Params {
		if p.IsVarArg {
			hasVA = true
			if p.Type != nil {
				va = c.resolveTypeWithSub(p.Type, sub)
				if arr, ok := va.(*types.Array); ok {
					va = arr.Elem
				}
			} else if va == nil {
				va = types.Any
			}
		}
	}
	var rs []types.Type
	for _, r := range t.Returns {
		rs = append(rs, c.resolveTypeWithSub(r, sub))
	}
	tps := make([]*types.GenericParam, len(t.Generics))
	for i, g := range t.Generics {
		tp := &types.GenericParam{Name: g.Name.Name}
		if g.Constraint != nil {
			tp.Constraint = c.resolveTypeWithSub(g.Constraint, sub)
		}
		if g.Default != nil {
			tp.Default = c.resolveTypeWithSub(g.Default, sub)
		}
		tps[i] = tp
	}
	return &types.Func{Params: ps, HasVarArg: hasVA, VarArg: va, Returns: rs, TypeParams: tps}
}

// resolveConditional resolves `Check extends Extends ? True : False`.
// Distributive evaluation mirrors TS: a naked type-parameter check
// distributes over unions once instantiated; anything still generic
// stays deferred (permissive in assignability until then).
func (c *Checker) resolveConditional(n *ast.ConditionalType, sub map[string]types.Type) types.Type {
	check := c.resolveTypeWithSub(n.Check, sub)
	c.extendsDepth++
	ext := c.resolveTypeWithSub(n.Extends, sub)
	c.extendsDepth--
	// `infer U` names bind in the true branch only (like TS).
	sub2 := make(map[string]types.Type, len(sub)+2)
	for k, v := range sub {
		sub2[k] = v
	}
	for name := range collectInferNames(n.Extends, nil) {
		sub2[name] = &types.InferParam{Name: name}
	}
	trueB := c.resolveTypeWithSub(n.True, sub2)
	falseB := c.resolveTypeWithSub(n.False, sub)
	distributive := isNakedTypeParam(n.Check, c, sub)
	cond := &types.Conditional{Check: check, Extends: ext, TrueBranch: trueB, FalseBranch: falseB, Distributive: distributive}
	if r, ok := types.TryEvalConditional(cond, c.opt.toTypes()); ok {
		return r
	}
	return cond
}

// isNakedTypeParam reports `T` (bare parameter reference, no args).
func isNakedTypeParam(n ast.TypeNode, c *Checker, sub map[string]types.Type) bool {
	nt, ok := n.(*ast.NamedType)
	if !ok || len(nt.Args) > 0 || strings.Contains(nt.Name, ".") {
		return false
	}
	if _, ok := sub[nt.Name]; ok {
		return true
	}
	return c.lookupTParam(nt.Name) != nil
}

// resolveMapped resolves `{ [K in Keys]?: V }`, expanding concrete
// domains eagerly and deferring the rest.
func (c *Checker) resolveMapped(n *ast.MappedType, sub map[string]types.Type) types.Type {
	nd := len(c.diags)
	constraint := c.resolveTypeWithSub(n.Constraint, sub)
	// The key variable is visible inside the value type (shadows outer).
	c.pushTParams([]*ast.GenericParam{{Name: n.Param}})
	value := c.resolveTypeWithSub(n.Value, sub)
	c.popTParams()
	m := &types.Mapped{Param: n.Param.Name, Constraint: constraint, Optional: n.Optional, OptMinus: n.OptMinus, Readonly: n.Readonly, RoMinus: n.RoMinus, Value: value}
	// Homomorphic base: `{[K in keyof T]...}` inherits T's field modifiers
	// unless an explicit `?`/`-?`/`readonly`/`-readonly` overrides them.
	// Only when nothing errored so far, so the re-resolution stays silent.
	if kn, ok := n.Constraint.(*ast.KeyofType); ok && len(c.diags) == nd {
		if base := c.resolveTypeWithSub(kn.Elem, sub); base != nil {
			if o, ok := base.(*types.Object); ok {
				m.Homomorphic = o
			}
		}
	}
	if r, ok := types.TryEvalMapped(m, c.opt.toTypes()); ok {
		return r
	}
	// Only report unmappable concrete domains; Any/Unknown arise from
	// unfilled generic parameters (definition check) or dynamic code,
	// both of which stay deferred.
	if types.IsConcreteType(constraint) && !isAnyLike(constraint) {
		c.errorf("TL3602", n.Span(), "cannot map over type '%s'", constraint.String())
		return types.Any
	}
	return m
}

// resolveIndexedAccess resolves `T[K]`.
func (c *Checker) resolveIndexedAccess(n *ast.IndexedAccessType, sub map[string]types.Type) types.Type {
	obj := c.resolveTypeWithSub(n.Obj, sub)
	index := c.resolveTypeWithSub(n.Index, sub)
	if r, ok, errStr := types.TryEvalIndexed(obj, index, c.opt.toTypes()); ok {
		if errStr != "" {
			c.errorf("TL3603", n.Span(), "%s", errStr)
			return types.Any
		}
		return r
	}
	return &types.IndexedAccess{Obj: obj, Index: index}
}

func (c *Checker) resolveTypeof(t *ast.TypeofType) types.Type {
	if id, ok := t.Expr.(*ast.Ident); ok {
		if s := c.cur().lookupVar(id.Name); s != nil {
			return s.Type
		}
		c.errorf("TL3104", t.Span(), "typeof: undefined variable '%s'", id.Name)
		return types.Any
	}
	// General expr: check it.
	return c.checkExpr(t.Expr)
}

func (c *Checker) resolveKeyof(e types.Type, sp token.Span) types.Type {
	switch x := e.(type) {
	case *types.Object:
		var ms []types.Type
		for name := range x.Fields {
			ms = append(ms, &types.Literal{Value: strconv.Quote(name), Base: types.String})
		}
		for _, ix := range x.Indexers {
			// keyof of indexer: key type itself.
			ms = append(ms, ix.Key)
		}
		if len(ms) == 0 {
			return types.Never
		}
		return types.NewUnion(ms)
	case *types.Tuple:
		var ms []types.Type
		for i := range x.Elems {
			ms = append(ms, &types.Literal{Value: strconv.Itoa(i + 1), Base: types.Number})
		}
		return types.NewUnion(ms)
	case *types.Array:
		return types.Number
	case *types.Map:
		return x.Key
	case *types.Class:
		var ms []types.Type
		for name, f := range x.Fields {
			if f.Access != types.Public {
				continue
			}
			ms = append(ms, &types.Literal{Value: strconv.Quote(name), Base: types.String})
		}
		if len(ms) == 0 {
			return types.Never
		}
		return types.NewUnion(ms)
	default:
		if isAnyLike(e) {
			return types.Any
		}
		c.errorf("TL3105", sp, "keyof not supported for '%s'", e.String())
		return types.Any
	}
}

func (c *Checker) resolveTypeEntry(e *typeEntry, sub map[string]types.Type) types.Type {
	return c.resolveTypeEntryWithArgs(e, nil, token.Span{})
}
