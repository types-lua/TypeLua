package types

import (
	"fmt"
	"hash/fnv"
	"sort"
)

// Substitute replaces generic params by args.
//
// 设计者视角：Substitute 是泛型实例化的“打印机”：拿 `Foo<T>` 的图纸与
// `{T: string}` 的墨盒，印出一份全具体的类型。障碍有二：空替换最热（evalOne
// 无 infer 时天天空转），递归类型会无限复印。取舍：空表直接返回原指针
// （零分配快路），非空则建 substCtx（memo+指纹）后委托 substitute；
// Mapped/IndexedAccess/Conditional 在替换后若变具体就地求值，避免残留延迟形。
// 不变量：空 sub 返回同一指针；非空返回新值，原类型不可变。排查现场：若替换
// 后仍含 GenericParam，检查 sub 的 Name 是否对齐；若栈溢出，检查 memo 是否命中。
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
//
// 设计者视角：memo 的 key 是 (类型指针, 替换指纹)，指纹必须便宜又稳。障碍是
// 旧方案用 String() 做指纹——每次 Substitute 都要全树序列化，深泛型链直接
// 把实例化拖垮。候选：String 文本 vs (名+实参指针)。选后者：名排序后喂 fnv，
// 实参只取 `%p` 指针身份，O(名数) 完成；指针不同但结构相同仅 miss 重算，永远
// sound（memo 只加速不改语义）。不变量：同 sub 内容必同指纹（名已排序）。
// 排查现场：若 memo 命中率低，先看实参是否每次新建指针；若误命中，检查 fp
// 是否漏了某参数名。
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
//
// 设计者视角：泛型声明 `T extends C = D` 的 C/D 里也可能藏着待换的 T（如
// `T extends Box<T>`），实例化时必须深入替换，否则约束停留在旧世界。保留
// Name 不换（形参名即绑定键），只换 Constraint/Default；nil 原样保留。
// 排查现场：若约束里的类型参数没被换掉，检查此处是否被跳过。
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

// substitute 是替换的递归内核，memo 按 (指针, 指纹) 去重，循环类型先占位后填。
//
// 设计者视角：它是“复印机滚筒”——Union/Array/Map/Tuple/Func/Object/Class 等
// 逐节点复印，GenericParam/InferParam 命中 sub 即换纸。障碍是递归类型
// （`next: Chain<T>`）朴素复印会无限生子；对策是 Object/Class 先建空壳入 memo
// 再填字段，让回边命中壳而终止。候选：深拷贝全树 vs memo 共享；选 memo 共享，
// 同形共享指针亦省内存。Mapped/IndexedAccess/Conditional 换完若变具体就地求值
// （evalMapped/evalIndexedAccess/EvalConditional），否则保留延迟形。不变量：
// Enum/Literal/Primitive 直接返回原指针（叶不可换）。排查现场：若替换后出现
// 半新半旧的混合体，检查 memo key 的 fp 是否跨 sub 复用；若延迟类型该求值未求值，
// 检查 IsConcreteType 守门。
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
