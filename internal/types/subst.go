package types

import (
	"fmt"
	"hash/fnv"
	"sort"
)

// Substitute replaces generic params by args.
func Substitute(t Type, sub map[string]Type) Type {
	// Fast path: empty substitution is identity (avoids fingerprint + memo
	// allocation on the very hot no-op path, e.g. evalOne with no infers).
	if len(sub) == 0 {
		return t
	}
	return substitute(t, sub, &substCtx{memo: map[subKey]Type{}, fp: subFingerprint(sub)})
}

type subKey struct {
	ptr Type
	fp  uint64
}

type substCtx struct {
	memo map[subKey]Type
	fp   uint64
}

// subFingerprint hashes a substitution by parameter names + argument pointer
// identity (not structural String rendering). Distinct pointers with identical
// structure merely miss the memo and recompute, which is always sound; the
// old String-based fingerprint paid a full recursive serialization per
// Substitute call.
func subFingerprint(sub map[string]Type) uint64 {
	names := make([]string, 0, len(sub))
	for n := range sub {
		names = append(names, n)
	}
	sort.Strings(names)
	h := fnv.New64a()
	for _, n := range names {
		h.Write([]byte(n))
		h.Write([]byte{0})
		// %p prints pointer identity without touching the type structure.
		fmt.Fprintf(h, "%p;", sub[n])
	}
	return h.Sum64()
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
		key := subKey{ptr: x, fp: ctx.fp}
		if out, ok := ctx.memo[key]; ok {
			return out
		}
		ns := make([]Type, len(x.Types))
		for i, m := range x.Types {
			ns[i] = substitute(m, sub, ctx)
		}
		out := NewUnion(ns)
		ctx.memo[key] = out
		return out
	case *Intersection:
		key := subKey{ptr: x, fp: ctx.fp}
		if out, ok := ctx.memo[key]; ok {
			return out
		}
		ns := make([]Type, len(x.Types))
		for i, m := range x.Types {
			ns[i] = substitute(m, sub, ctx)
		}
		out := NewIntersection(ns)
		ctx.memo[key] = out
		return out
	case *Array:
		key := subKey{ptr: x, fp: ctx.fp}
		if out, ok := ctx.memo[key]; ok {
			return out
		}
		out := &Array{Elem: substitute(x.Elem, sub, ctx)}
		ctx.memo[key] = out
		return out
	case *Chan:
		key := subKey{ptr: x, fp: ctx.fp}
		if out, ok := ctx.memo[key]; ok {
			return out
		}
		out := &Chan{Elem: substitute(x.Elem, sub, ctx)}
		ctx.memo[key] = out
		return out
	case *Map:
		key := subKey{ptr: x, fp: ctx.fp}
		if out, ok := ctx.memo[key]; ok {
			return out
		}
		out := &Map{Key: substitute(x.Key, sub, ctx), Value: substitute(x.Value, sub, ctx)}
		ctx.memo[key] = out
		return out
	case *Tuple:
		key := subKey{ptr: x, fp: ctx.fp}
		if out, ok := ctx.memo[key]; ok {
			return out
		}
		ns := make([]Type, len(x.Elems))
		for i, e := range x.Elems {
			ns[i] = substitute(e, sub, ctx)
		}
		out := &Tuple{Elems: ns}
		ctx.memo[key] = out
		return out
	case *Func:
		key := subKey{ptr: x, fp: ctx.fp}
		if out, ok := ctx.memo[key]; ok {
			return out
		}
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
		out := &Func{Params: ps, HasVarArg: x.HasVarArg, VarArg: va, Returns: rs, TypeParams: tps, IsAsync: x.IsAsync}
		ctx.memo[key] = out
		return out
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
		key := subKey{ptr: x, fp: ctx.fp}
		if out, ok := ctx.memo[key]; ok {
			return out
		}
		na := make([]Type, len(x.Args))
		for i, a := range x.Args {
			na[i] = substitute(a, sub, ctx)
		}
		out := &Ref{Name: x.Name, Args: na}
		ctx.memo[key] = out
		return out
	case *InferParam:
		if s, ok := sub[x.Name]; ok {
			return s
		}
		return x
	case *Mapped:
		key := subKey{ptr: x, fp: ctx.fp}
		if out, ok := ctx.memo[key]; ok {
			return out
		}
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
			ctx.memo[key] = r
			return r
		}
		ctx.memo[key] = m2
		return m2
	case *IndexedAccess:
		key := subKey{ptr: x, fp: ctx.fp}
		if out, ok := ctx.memo[key]; ok {
			return out
		}
		obj := substitute(x.Obj, sub, ctx)
		index := substitute(x.Index, sub, ctx)
		if r, ok, _ := evalIndexedAccess(obj, index, Options{StrictNilChecks: true}); ok {
			ctx.memo[key] = r
			return r
		}
		out := &IndexedAccess{Obj: obj, Index: index}
		ctx.memo[key] = out
		return out
	case *Conditional:
		key := subKey{ptr: x, fp: ctx.fp}
		if out, ok := ctx.memo[key]; ok {
			return out
		}
		check := substitute(x.Check, sub, ctx)
		ext := substitute(x.Extends, sub, ctx)
		trueB := substitute(x.TrueBranch, sub, ctx)
		falseB := substitute(x.FalseBranch, sub, ctx)
		if IsConcreteType(check) {
			// Strict nil checks match the checker's default; documented.
			out := EvalConditional(check, ext, trueB, falseB, x.Distributive, Options{StrictNilChecks: true})
			ctx.memo[key] = out
			return out
		}
		out := &Conditional{Check: check, Extends: ext, TrueBranch: trueB, FalseBranch: falseB, Distributive: x.Distributive}
		ctx.memo[key] = out
		return out
	case *Enum:
		return x
	case *Literal:
		return x
	case *Primitive:
		return x
	}
	return t
}
