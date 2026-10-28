package types

// ---------- conditional types ----------

// IsConcreteType reports whether a type is free of unbound type parameters
// (and deferred conditionals), i.e. ready for conditional evaluation.
//
// 设计者视角：条件类型 `T extends U ? X : Y` 只有在 T 具体时才能求值；若 T
// 仍含泛型参数/未实例化的 Ref/deferred Conditional，求值会把泛型函数的条件
// 结果过早固化。候选：递归遍历所有节点 vs 快速路径先行。选快速路径：Primitive/
// Literal/Enum 等叶节点直接 true，GenericParam/InferParam/Conditional/Ref 直接
// false，仅复合节点落回 hasTypeParams；这样热路径避免分配 visited set。
// 不变量：nil 视为具体（空类型无参数）；Literal/Enum 的 base/underlying 为
// 原语时直接 true，避免为原语 base 递归。排查现场：若条件类型该求值却延迟，
// 检查 hasTypeParams 是否把某节点误判为含参数。
func IsConcreteType(t Type) bool {
	if t == nil {
		return true
	}
	// Fast paths without a visited-set allocation: leaves and
	// always-deferred nodes decide immediately; only potentially cyclic or
	// composite nodes fall through to the full traversal.
	switch x := t.(type) {
	case *GenericParam, *InferParam, *Conditional, *Ref:
		return false
	case *Primitive:
		return true
	case *Literal:
		if x.Base == nil {
			return true
		}
		// Literal bases are always primitives in practice; recurse without
		// a set (no cycle possible through a primitive base).
		if _, ok := x.Base.(*Primitive); ok {
			return true
		}
		return !hasTypeParams(x.Base, map[Type]bool{})
	case *Enum:
		if x.Underlying == nil {
			return true
		}
		if _, ok := x.Underlying.(*Primitive); ok {
			return true
		}
		return !hasTypeParams(x.Underlying, map[Type]bool{})
	}
	return !hasTypeParams(t, map[Type]bool{})
}

// hasTypeParams 递归判断 t 是否含未绑定的类型参数/延迟类型，seen 用于指针身份
// 终止循环（Class/Object/Union/Intersection 可能自引用）。
//
// 设计者视角：复合类型可能循环引用（如 `interface Node { next: Node<T> }`），
// 朴素递归会栈溢出。候选：全局 visited vs 按类型族 visited；选按族 visited，
// 仅对可能自引用的指针类型（Class/ClassValue/Object/Union/Intersection）入栈，
// 叶节点与容器不入栈，避免漏判或误判。GenericParam/InferParam/Conditional/Ref
// 任一出现即 true。排查现场：若递归类型误报"含参数"，检查 seen 的入栈条件
// 是否把某类型族遗漏。
func hasTypeParams(t Type, seen map[Type]bool) bool {
	if t == nil {
		return false
	}
	// Pointers (classes, possibly cyclic) terminate via seen.
	switch t.(type) {
	case *Class, *ClassValue, *Object, *Union, *Intersection:
		if seen[t] {
			return false
		}
		seen[t] = true
	}
	switch x := t.(type) {
	case *GenericParam, *InferParam, *Conditional, *Ref:
		return true
	case *Union:
		for _, m := range x.Types {
			if hasTypeParams(m, seen) {
				return true
			}
		}
	case *Intersection:
		for _, m := range x.Types {
			if hasTypeParams(m, seen) {
				return true
			}
		}
	case *Array:
		return hasTypeParams(x.Elem, seen)
	case *Chan:
		return hasTypeParams(x.Elem, seen)
	case *Map:
		return hasTypeParams(x.Key, seen) || hasTypeParams(x.Value, seen)
	case *Tuple:
		for _, e := range x.Elems {
			if hasTypeParams(e, seen) {
				return true
			}
		}
	case *Func:
		for _, p := range x.Params {
			if hasTypeParams(p.Type, seen) {
				return true
			}
		}
		if x.VarArg != nil && hasTypeParams(x.VarArg, seen) {
			return true
		}
		for _, r := range x.Returns {
			if hasTypeParams(r, seen) {
				return true
			}
		}
		for _, gp := range x.TypeParams {
			if gp != nil && (hasTypeParams(gp.Constraint, seen) || hasTypeParams(gp.Default, seen)) {
				return true
			}
		}
	case *Object:
		for _, f := range x.Fields {
			if hasTypeParams(f.Type, seen) {
				return true
			}
		}
		for _, ix := range x.Indexers {
			if hasTypeParams(ix.Key, seen) || hasTypeParams(ix.Value, seen) {
				return true
			}
		}
	case *Class:
		for _, a := range x.Args {
			if hasTypeParams(a, seen) {
				return true
			}
		}
		for _, f := range x.Fields {
			if hasTypeParams(f.Type, seen) {
				return true
			}
		}
		if x.Base != nil && hasTypeParams(x.Base, seen) {
			return true
		}
		for _, gp := range x.TypeParams {
			if gp != nil && (hasTypeParams(gp.Constraint, seen) || hasTypeParams(gp.Default, seen)) {
				return true
			}
		}
	case *ClassValue:
		if x.Class != nil && hasTypeParams(x.Class, seen) {
			return true
		}
		if x.Constructor != nil && hasTypeParams(x.Constructor, seen) {
			return true
		}
		for _, f := range x.Statics {
			if hasTypeParams(f.Type, seen) {
				return true
			}
		}
	case *Literal:
		return hasTypeParams(x.Base, seen)
	case *Mapped:
		return hasTypeParams(x.Constraint, seen) || hasTypeParams(x.Value, seen) ||
			(x.Homomorphic != nil && hasTypeParams(x.Homomorphic, seen))
	case *IndexedAccess:
		return hasTypeParams(x.Obj, seen) || hasTypeParams(x.Index, seen)
	}
	return false
}
