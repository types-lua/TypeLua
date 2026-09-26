package types

// Conditional is `Check extends Extends ? True : False`. While Check still
// mentions type parameters it stays deferred; Distributive mirrors TS
// (naked type-param checks distribute over unions).
type Conditional struct {
	Check        Type
	Extends      Type
	TrueBranch   Type
	FalseBranch  Type
	Distributive bool
}

func (t *Conditional) Kind() Kind { return KConditional }
func (t *Conditional) String() string { return t.stringSeen(map[Type]bool{}) }
func (t *Conditional) stringSeen(seen map[Type]bool) string {
	return typeString(t.Check, seen) + " extends " + typeString(t.Extends, seen) + " ? " + typeString(t.TrueBranch, seen) + " : " + typeString(t.FalseBranch, seen)
}

// TryEvalConditional evaluates c when its check type is concrete.
func TryEvalConditional(c *Conditional, opt Options) (Type, bool) {
	if !IsConcreteType(c.Check) {
		return c, false
	}
	return EvalConditional(c.Check, c.Extends, c.TrueBranch, c.FalseBranch, c.Distributive, opt), true
}

// EvalConditional evaluates a conditional with a concrete check type.
func EvalConditional(check, extends, trueB, falseB Type, distributive bool, opt Options) Type {
	if distributive {
		if u, ok := check.(*Union); ok {
			rs := make([]Type, 0, len(u.Types))
			for _, m := range u.Types {
				rs = append(rs, evalOne(m, extends, trueB, falseB, opt))
			}
			return NewUnion(rs)
		}
	}
	return evalOne(check, extends, trueB, falseB, opt)
}

func evalOne(check, extends, trueB, falseB Type, opt Options) Type {
	bindings := map[string]Type{}
	if matchInfer(check, extends, bindings, map[visit]bool{}, opt) {
		if len(bindings) == 0 {
			return trueB
		}
		return Substitute(trueB, bindings)
	}
	return falseB
}

// matchInfer matches a concrete source against a pattern, collecting infer
// bindings. The seen map makes matching coinductive over recursive types.
func matchInfer(source, pattern Type, bindings map[string]Type, seen map[visit]bool, opt Options) bool {
	if source == nil || pattern == nil {
		return source == nil && pattern == nil
	}
	// `infer U` binds the whole source (joining conflicts as unions).
	if ip, ok := pattern.(*InferParam); ok {
		if prev, ok := bindings[ip.Name]; ok {
			if prev == source {
				return true
			}
			if prev.String() != source.String() {
				bindings[ip.Name] = NewUnion([]Type{prev, source})
			}
			return true
		}
		bindings[ip.Name] = source
		return true
	}
	// Unconstrained unknowns match anything without binding.
	if pattern.Kind() == KAny {
		return true
	}
	if pattern.Kind() == KUnknown {
		return true
	}
	if source.Kind() == KAny || source.Kind() == KUnknown {
		return true
	}
	if pattern.Kind() == KNever {
		return source.Kind() == KNever
	}
	if source.Kind() == KNever {
		return true
	}
	key := visit{source, pattern}
	if seen[key] {
		return true
	}
	seen[key] = true

	// Bare type parameters only match themselves here (concrete sources
	// never satisfy an unbound parameter).
	if _, ok := pattern.(*GenericParam); ok {
		// Fast path first (pointer identity), then structural fallback.
		// GenericParam copies print differently per copy, so compare names.
		if sg, ok := source.(*GenericParam); ok && sg.Name == pattern.(*GenericParam).Name {
			return true
		}
		return source.String() == pattern.String()
	}
	// Nested conditionals in pattern position: evaluate when possible.
	if pc, ok := pattern.(*Conditional); ok {
		if r, ok := TryEvalConditional(pc, opt); ok {
			return matchInfer(source, r, bindings, seen, opt)
		}
		return false
	}
	// Mapped and indexed patterns evaluate when concrete.
	if pm, ok := pattern.(*Mapped); ok {
		if r, ok := TryEvalMapped(pm, opt); ok {
			return matchInfer(source, r, bindings, seen, opt)
		}
		return false
	}
	if pi, ok := pattern.(*IndexedAccess); ok {
		if r, ok, _ := TryEvalIndexed(pi.Obj, pi.Index, opt); ok {
			return matchInfer(source, r, bindings, seen, opt)
		}
		return false
	}

	// Literals match identical literals or via their base.
	if sl, ok := source.(*Literal); ok {
		if pl, ok := pattern.(*Literal); ok {
			return sl.Value == pl.Value
		}
		return matchInfer(sl.Base, pattern, bindings, seen, opt)
	}
	if _, ok := pattern.(*Literal); ok {
		return false
	}

	// Primitives (with the integer/float <: number lattice).
	if isPrim(source) && isPrim(pattern) {
		return primAssignable(source.(*Primitive), pattern.(*Primitive))
	}
	if isPrim(pattern) {
		return false
	}

	// Unions: source union requires all members; pattern union needs one.
	if su, ok := source.(*Union); ok {
		for _, m := range su.Types {
			if !matchInfer(m, pattern, bindings, seen, opt) {
				return false
			}
		}
		return true
	}
	if pu, ok := pattern.(*Union); ok {
		for _, m := range pu.Types {
			// Tentative bindings per member: keep the first that matches.
			probe := copyBindings(bindings)
			if matchInfer(source, m, probe, seen, opt) {
				for k, v := range probe {
					bindings[k] = v
				}
				return true
			}
		}
		return false
	}

	// Intersections: source needs one side; pattern needs all sides.
	if si, ok := source.(*Intersection); ok {
		for _, m := range si.Types {
			if matchInfer(m, pattern, bindings, seen, opt) {
				return true
			}
		}
		return false
	}
	if pi, ok := pattern.(*Intersection); ok {
		for _, m := range pi.Types {
			if !matchInfer(source, m, bindings, seen, opt) {
				return false
			}
		}
		return true
	}

	// Optionals and parens are already normalized (unions); void/nil below.
	if pattern.Kind() == KNil || pattern.Kind() == KVoid {
		return source.Kind() == KNil || source.Kind() == KVoid
	}
	if source.Kind() == KNil || source.Kind() == KVoid {
		return false
	}

	// Arrays: tuples spread element-wise into the element pattern.
	if pa, ok := pattern.(*Array); ok {
		switch s := source.(type) {
		case *Array:
			return matchInfer(s.Elem, pa.Elem, bindings, seen, opt)
		case *Tuple:
			for _, e := range s.Elems {
				if !matchInfer(e, pa.Elem, bindings, seen, opt) {
					return false
				}
			}
			return true
		}
		return false
	}

	// Tuples: positional, with trailing `...Rest` matching the remainder.
	if pt, ok := pattern.(*Tuple); ok {
		switch s := source.(type) {
		case *Tuple:
			return matchTupleElems(s.Elems, pt.Elems, bindings, seen, opt)
		case *Array:
			for _, pe := range pt.Elems {
				if isRestElem(pe) {
					continue
				}
				if !matchInfer(s.Elem, restTarget(pe), bindings, seen, opt) {
					return false
				}
			}
			return true
		}
		return false
	}

	// Functions: returns covariantly, params pairwise source-to-pattern.
	if pf, ok := pattern.(*Func); ok {
		sf, ok := source.(*Func)
		if !ok {
			return false
		}
		n := len(pf.Params)
		if len(sf.Params) < n {
			n = len(sf.Params)
		}
		for i := 0; i < n; i++ {
			if !matchInfer(sf.Params[i].Type, pf.Params[i].Type, bindings, seen, opt) {
				return false
			}
		}
		if len(pf.Returns) == 0 {
			return true
		}
		if len(sf.Returns) == 0 {
			if sf.HasVarArg && sf.VarArg != nil {
				for _, pr := range pf.Returns {
					if !matchInfer(sf.VarArg, pr, bindings, seen, opt) {
						return false
					}
				}
				return true
			}
			return everyNil(pf.Returns)
		}
		for i, pr := range pf.Returns {
			var actual Type = Nil
			if i < len(sf.Returns) {
				actual = sf.Returns[i]
			} else if sf.HasVarArg && sf.VarArg != nil {
				actual = sf.VarArg
			}
			if !matchInfer(actual, pr, bindings, seen, opt) {
				return false
			}
		}
		return true
	}

	// Objects/classes/maps: every required pattern field must match.
	if po, ok := pattern.(*Object); ok {
		return matchObjectFields(source, po.Fields, bindings, seen, opt)
	}
	if pm, ok := pattern.(*Map); ok {
		sm, ok := source.(*Map)
		if !ok {
			return false
		}
		return matchInfer(sm.Key, pm.Key, bindings, seen, opt) &&
			matchInfer(sm.Value, pm.Value, bindings, seen, opt)
	}
	if _, ok := pattern.(*Class); ok {
		// Nominal patterns match along the source chain; bindings flow
		// through the first matching ancestor's arguments.
		sc, ok := source.(*Class)
		if !ok {
			return false
		}
		return matchClassPattern(sc, pattern.(*Class), bindings, seen, opt)
	}

	// Enum pattern: source must be one of its members.
	if pe, ok := pattern.(*Enum); ok {
		for _, m := range pe.Members {
			if matchInfer(source, m, bindings, seen, opt) {
				return true
			}
		}
		return false
	}

	if source == pattern {
		return true
	}
	return source.String() == pattern.String()
}

func copyBindings(b map[string]Type) map[string]Type {
	if len(b) == 0 {
		return map[string]Type{}
	}
	out := make(map[string]Type, len(b))
	for k, v := range b {
		out[k] = v
	}
	return out
}

func everyNil(rs []Type) bool {
	for _, r := range rs {
		if r.Kind() != KNil {
			return false
		}
	}
	return true
}

// isRestElem recognizes the `...T` tuple pattern element.
func isRestElem(t Type) bool {
	nr, ok := t.(*Ref)
	if !ok {
		return false
	}
	return nr.Name == "..." && len(nr.Args) == 1
}

// restTarget unwraps `...T` (or returns t unchanged). When T is an array
// type (the `...rest: unknown[]` tuple idiom), elements match against its
// element type, mirroring function varargs.
func restTarget(t Type) Type {
	if nr, ok := t.(*Ref); ok && nr.Name == "..." && len(nr.Args) == 1 {
		if arr, ok := nr.Args[0].(*Array); ok {
			return arr.Elem
		}
		return nr.Args[0]
	}
	return t
}

func matchTupleElems(src, pat []Type, bindings map[string]Type, seen map[visit]bool, opt Options) bool {
	i := 0
	for ; i < len(pat); i++ {
		if isRestElem(pat[i]) {
			for ; i < len(src); i++ {
				if !matchInfer(src[i], restTarget(pat[i]), bindings, seen, opt) {
					return false
				}
			}
			return true
		}
		if i >= len(src) {
			return false
		}
		if !matchInfer(src[i], pat[i], bindings, seen, opt) {
			return false
		}
	}
	return i == len(src)
}

// matchObjectFields matches a source against required pattern fields.
// Sources may be objects, class instances (public view), or string-keyed maps.
func matchObjectFields(source Type, fields map[string]Field, bindings map[string]Type, seen map[visit]bool, opt Options) bool {
	for name, pf := range fields {
		var st Type
		switch s := source.(type) {
		case *Object:
			f, ok := s.Fields[name]
			if !ok {
				covered := false
				for _, ix := range s.Indexers {
					if ix.Key.Kind() == KString || ix.Key.Kind() == KAny {
						st = ix.Value
						covered = true
						break
					}
				}
				if !covered {
					if pf.Optional {
						continue
					}
					return false
				}
			} else {
				st = f.Type
			}
		case *Class:
			f, ok := s.Fields[name]
			if !ok || f.Access != Public {
				if pf.Optional {
					continue
				}
				return false
			}
			st = f.Type
		case *Map:
			if s.Key.Kind() != KString && s.Key.Kind() != KAny {
				if pf.Optional {
					continue
				}
				return false
			}
			st = s.Value
		default:
			if pf.Optional {
				continue
			}
			return false
		}
		if !matchInfer(st, pf.Type, bindings, seen, opt) {
			return false
		}
	}
	return true
}

// matchClassPattern matches nominally: the source must be the same class or
// a subclass, with compatible type arguments flowing into infer positions.
func matchClassPattern(source, pattern *Class, bindings map[string]Type, seen map[visit]bool, opt Options) bool {
	visited := map[*Class]bool{}
	for c := source; c != nil; c = c.Base {
		if visited[c] {
			return false
		}
		visited[c] = true
		same := c.Def != nil && pattern.Def != nil && c.Def == pattern.Def
		if !same && c.Def == nil && pattern.Def == nil && c.Name != "" && c.Name == pattern.Name {
			same = true
		}
		if !same {
			continue
		}
		if len(c.Args) != len(pattern.Args) {
			return len(c.Args) == 0 && len(pattern.Args) == 0
		}
		for i := range c.Args {
			if !matchInfer(c.Args[i], pattern.Args[i], bindings, seen, opt) {
				return false
			}
		}
		return true
	}
	return false
}
