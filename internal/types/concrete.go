package types

// ---------- conditional types ----------

// IsConcreteType reports whether a type is free of unbound type parameters
// (and deferred conditionals), i.e. ready for conditional evaluation.
func IsConcreteType(t Type) bool {
	return !hasTypeParams(t, map[Type]bool{})
}

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
