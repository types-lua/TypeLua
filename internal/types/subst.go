package types

import (
	"sort"
	"strings"
)

// Substitute replaces generic params by args.
func Substitute(t Type, sub map[string]Type) Type {
	return substitute(t, sub, &substCtx{memo: map[subKey]Type{}, fp: subFingerprint(sub)})
}

type subKey struct {
	ptr any
	fp  string
}

type substCtx struct {
	memo map[subKey]Type
	fp   string
}

func subFingerprint(sub map[string]Type) string {
	names := make([]string, 0, len(sub))
	for n := range sub {
		names = append(names, n)
	}
	sort.Strings(names)
	var sb strings.Builder
	for _, n := range names {
		sb.WriteString(n)
		sb.WriteByte('=')
		sb.WriteString(sub[n].String())
		sb.WriteByte(';')
	}
	return sb.String()
}

// substituteGenericParam substitutes inside a generic declaration
// (constraint and default), preserving the parameter name.
func substituteGenericParam(gp *GenericParam, sub map[string]Type, ctx *substCtx) *GenericParam {
	if gp == nil {
		return nil
	}
	out := &GenericParam{Name: gp.Name}
	if gp.Constraint != nil {
		out.Constraint = substitute(gp.Constraint, sub, ctx)
	}
	if gp.Default != nil {
		out.Default = substitute(gp.Default, sub, ctx)
	}
	return out
}

func substitute(t Type, sub map[string]Type, ctx *substCtx) Type {
	switch x := t.(type) {
	case *GenericParam:
		if s, ok := sub[x.Name]; ok {
			return s
		}
		return x
	case *Union:
		ns := make([]Type, len(x.Types))
		for i, m := range x.Types {
			ns[i] = substitute(m, sub, ctx)
		}
		return NewUnion(ns)
	case *Intersection:
		ns := make([]Type, len(x.Types))
		for i, m := range x.Types {
			ns[i] = substitute(m, sub, ctx)
		}
		return NewIntersection(ns)
	case *Array:
		return &Array{Elem: substitute(x.Elem, sub, ctx)}
	case *Chan:
		return &Chan{Elem: substitute(x.Elem, sub, ctx)}
	case *Map:
		return &Map{Key: substitute(x.Key, sub, ctx), Value: substitute(x.Value, sub, ctx)}
	case *Tuple:
		ns := make([]Type, len(x.Elems))
		for i, e := range x.Elems {
			ns[i] = substitute(e, sub, ctx)
		}
		return &Tuple{Elems: ns}
	case *Func:
		ps := make([]FuncParam, len(x.Params))
		for i, p := range x.Params {
			ps[i] = FuncParam{Name: p.Name, Type: substitute(p.Type, sub, ctx), Optional: p.Optional}
		}
		var va Type
		if x.VarArg != nil {
			va = substitute(x.VarArg, sub, ctx)
		}
		rs := make([]Type, len(x.Returns))
		for i, r := range x.Returns {
			rs[i] = substitute(r, sub, ctx)
		}
		tps := make([]*GenericParam, len(x.TypeParams))
		for i, gp := range x.TypeParams {
			tps[i] = substituteGenericParam(gp, sub, ctx)
		}
		return &Func{Params: ps, HasVarArg: x.HasVarArg, VarArg: va, Returns: rs, TypeParams: tps}
	case *Object:
		// Shared (possibly cyclic) objects substitute once; the copy is
		// filled in place so recursive references terminate.
		key := subKey{ptr: x, fp: ctx.fp}
		if out, ok := ctx.memo[key]; ok {
			return out
		}
		out := &Object{Name: x.Name, Fields: map[string]Field{}, Indexers: nil}
		ctx.memo[key] = out
		for n, f := range x.Fields {
			out.Fields[n] = Field{Type: substitute(f.Type, sub, ctx), Optional: f.Optional, Readonly: f.Readonly, Access: f.Access, Owner: f.Owner}
		}
		for _, ix := range x.Indexers {
			out.Indexers = append(out.Indexers, Indexer{Key: substitute(ix.Key, sub, ctx), Value: substitute(ix.Value, sub, ctx)})
		}
		return out
	case *Class:
		// Recursive classes (e.g. `next: Chain<T>`) would diverge under
		// naive substitution; build the copy once and fill it in place.
		key := subKey{ptr: x, fp: ctx.fp}
		if out, ok := ctx.memo[key]; ok {
			return out
		}
		na := make([]Type, len(x.Args))
		for i, a := range x.Args {
			na[i] = substitute(a, sub, ctx)
		}
		out := &Class{Def: x.Def, Name: x.Name, Args: na, Params: x.Params, Fields: map[string]ClassField{}, Base: nil, IsAbstract: x.IsAbstract}
		ctx.memo[key] = out
		for n, f := range x.Fields {
			out.Fields[n] = ClassField{Type: substitute(f.Type, sub, ctx), Optional: f.Optional, Readonly: f.Readonly, Access: f.Access, IsMethod: f.IsMethod, Abstract: f.Abstract, Owner: f.Owner}
		}
		out.TypeParams = make([]*GenericParam, len(x.TypeParams))
		for i, gp := range x.TypeParams {
			out.TypeParams[i] = substituteGenericParam(gp, sub, ctx)
		}
		if x.Base != nil {
			if b, ok := substitute(x.Base, sub, ctx).(*Class); ok {
				out.Base = b
			} else {
				out.Base = x.Base
			}
		}
		return out
	case *ClassValue:
		var nc *Class
		if x.Class != nil {
			if c, ok := substitute(x.Class, sub, ctx).(*Class); ok {
				nc = c
			} else {
				nc = x.Class
			}
		}
		var ctor *Func
		if x.Constructor != nil {
			if f, ok := substitute(x.Constructor, sub, ctx).(*Func); ok {
				ctor = f
			} else {
				ctor = x.Constructor
			}
		}
		ns := map[string]ClassField{}
		for n, f := range x.Statics {
			ns[n] = ClassField{Type: substitute(f.Type, sub, ctx), Optional: f.Optional, Readonly: f.Readonly, Access: f.Access, IsMethod: f.IsMethod, Abstract: f.Abstract, Owner: f.Owner}
		}
		return &ClassValue{Class: nc, Constructor: ctor, CtorAccess: x.CtorAccess, Statics: ns, Abstract: x.Abstract}
	case *Ref:
		na := make([]Type, len(x.Args))
		for i, a := range x.Args {
			na[i] = substitute(a, sub, ctx)
		}
		return &Ref{Name: x.Name, Args: na}
	case *InferParam:
		if s, ok := sub[x.Name]; ok {
			return s
		}
		return x
	case *Mapped:
		m2 := &Mapped{
			Param:       x.Param,
			Constraint:  substitute(x.Constraint, sub, ctx),
			Optional:    x.Optional,
			OptMinus:    x.OptMinus,
			Readonly:    x.Readonly,
			RoMinus:     x.RoMinus,
			Homomorphic: substitute(x.Homomorphic, sub, ctx),
			Value:       substitute(x.Value, sub, ctx),
		}
		if r, ok := evalMapped(m2, Options{StrictNilChecks: true}); ok {
			return r
		}
		return m2
	case *IndexedAccess:
		obj := substitute(x.Obj, sub, ctx)
		index := substitute(x.Index, sub, ctx)
		if r, ok, _ := evalIndexedAccess(obj, index, Options{StrictNilChecks: true}); ok {
			return r
		}
		return &IndexedAccess{Obj: obj, Index: index}
	case *Conditional:
		check := substitute(x.Check, sub, ctx)
		ext := substitute(x.Extends, sub, ctx)
		trueB := substitute(x.TrueBranch, sub, ctx)
		falseB := substitute(x.FalseBranch, sub, ctx)
		if IsConcreteType(check) {
			// Strict nil checks match the checker's default; documented.
			return EvalConditional(check, ext, trueB, falseB, x.Distributive, Options{StrictNilChecks: true})
		}
		return &Conditional{Check: check, Extends: ext, TrueBranch: trueB, FalseBranch: falseB, Distributive: x.Distributive}
	case *Enum:
		return x
	case *Literal:
		return x
	case *Primitive:
		return x
	}
	return t
}
