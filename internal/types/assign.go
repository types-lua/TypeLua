package types

// ---------- subtyping ----------

type Options struct {
	StrictNilChecks bool
}

// visit identifies one coinductive subtyping hypothesis by pointer identity.
// All Type implementations are pointers, so interface equality is pointer
// equality. Using identity (instead of the old %p:String rendering) avoids
// a full recursive String() on every recursion step; distinct pointers with
// identical structure simply miss the cache and recompute, which is always
// sound (hypotheses only ever conclude "true" for cycles).
type visit struct {
	from Type
	to   Type
}

// IsAssignable reports whether `from` can be assigned to `to`.
func IsAssignable(from, to Type, opt Options) bool {
	return assignable(from, to, opt, map[visit]bool{})
}

func assignable(from, to Type, opt Options, seen map[visit]bool) bool {
	if from == nil || to == nil {
		return true
	}
	// Fast paths.
	if to.Kind() == KAny {
		return true
	}
	if from.Kind() == KAny {
		return true
	}
	if from.Kind() == KNever {
		return true
	}
	if to.Kind() == KUnknown {
		return true
	}
	if from.Kind() == KUnknown {
		return false
	}
	if to.Kind() == KNever {
		return from.Kind() == KNever
	}
	key := visit{from, to}
	if seen[key] {
		return true
	}
	seen[key] = true

	// Source union: every member must be assignable (must come before target-union
	// fast path, otherwise `A|B <: A|B` fails when checked member-wise).
	if u, ok := from.(*Union); ok {
		for _, m := range u.Types {
			if !assignable(m, to, opt, seen) {
				return false
			}
		}
		return true
	}
	// Target union: from must match one member (nil handling included).
	if u, ok := to.(*Union); ok {
		for _, m := range u.Types {
			if assignable(from, m, opt, seen) {
				return true
			}
		}
		// Special: single-member after filtering? handled above.
		return false
	}
	// Target intersection: must match all.
	if it, ok := to.(*Intersection); ok {
		for _, m := range it.Types {
			if !assignable(from, m, opt, seen) {
				return false
			}
		}
		return true
	}
	// Source intersection: match if any member matches.
	if it, ok := from.(*Intersection); ok {
		for _, m := range it.Types {
			if assignable(m, to, opt, seen) {
				return true
			}
		}
		return false
	}

	// Conditionals with a concrete check evaluate on the spot.
	if fc, ok := from.(*Conditional); ok {
		if r, ok := TryEvalConditional(fc, opt); ok {
			return assignable(r, to, opt, seen)
		}
	}
	if tc, ok := to.(*Conditional); ok {
		if r, ok := TryEvalConditional(tc, opt); ok {
			return assignable(from, r, opt, seen)
		}
	}
	// Still-deferred conditionals live in generic positions that cannot
	// be refuted until instantiation: stay permissive, like Unknown.
	if _, ok := from.(*Conditional); ok {
		return true
	}
	if _, ok := to.(*Conditional); ok {
		return true
	}
	// Deferred mapped and indexed-access types behave the same way.
	if fm, ok := from.(*Mapped); ok {
		if r, ok := TryEvalMapped(fm, opt); ok {
			return assignable(r, to, opt, seen)
		}
		return true
	}
	if tm, ok := to.(*Mapped); ok {
		if r, ok := TryEvalMapped(tm, opt); ok {
			return assignable(from, r, opt, seen)
		}
		return true
	}
	if fi, ok := from.(*IndexedAccess); ok {
		if r, ok, _ := TryEvalIndexed(fi.Obj, fi.Index, opt); ok {
			return assignable(r, to, opt, seen)
		}
		return true
	}
	if ti, ok := to.(*IndexedAccess); ok {
		if r, ok, _ := TryEvalIndexed(ti.Obj, ti.Index, opt); ok {
			return assignable(from, r, opt, seen)
		}
		return true
	}
	// Unbound `infer` placeholders only survive via misuse (reported as
	// TL3601 at the source); stay permissive here.
	if _, ok := from.(*InferParam); ok {
		return to.Kind() == KAny || to.Kind() == KUnknown || from == to || from.String() == to.String()
	}
	if _, ok := to.(*InferParam); ok {
		return true
	}

	// Nil / void.
	if from.Kind() == KNil {
		if to.Kind() == KNil || to.Kind() == KVoid {
			return true
		}
		if !opt.StrictNilChecks {
			return true
		}
		return false
	}
	if to.Kind() == KNil {
		return from.Kind() == KNil
	}
	if to.Kind() == KVoid {
		return from.Kind() == KVoid || from.Kind() == KNil
	}
	if from.Kind() == KVoid {
		return to.Kind() == KVoid
	}

	// Literals.
	if fl, ok := from.(*Literal); ok {
		if tl, ok := to.(*Literal); ok {
			return fl.Value == tl.Value
		}
		// Literal to base.
		if isBaseOf(fl.Base, to) {
			return true
		}
		// Literal string "red" to enum? handled via enum below (to is enum).
		if te, ok := to.(*Enum); ok {
			for _, m := range te.Members {
				if lit, ok := m.(*Literal); ok && lit.Value == fl.Value {
					return true
				}
			}
			return false
		}
		return false
	}
	if _, ok := to.(*Literal); ok {
		// Non-literal to literal: only identical literal matches (handled above).
		return false
	}

	// Primitives & numeric lattice.
	if isPrim(from) && isPrim(to) {
		return primAssignable(from.(*Primitive), to.(*Primitive))
	}
	// Integer/float literals? Already handled as Literal.

	// Generic param target: must satisfy constraint.
	if gp, ok := to.(*GenericParam); ok {
		if gp.Constraint != nil {
			return assignable(from, gp.Constraint, opt, seen)
		}
		return true
	}
	if fg, ok := from.(*GenericParam); ok {
		// Same parameter is trivially assignable (constraints may print
		// differently per copy, so compare names, not strings).
		if tg, ok := to.(*GenericParam); ok && tg.Name == fg.Name {
			return true
		}
		// A bare type param is assignable where its constraint is.
		if fg.Constraint != nil {
			return assignable(fg.Constraint, to, opt, seen)
		}
		return from == to || from.String() == to.String()
	}

	// Enum.
	if fe, ok := from.(*Enum); ok {
		if te, ok := to.(*Enum); ok {
			return fe.Name == te.Name
		}
		// Enum to underlying/base: all members must be assignable.
		for _, m := range fe.Members {
			if !assignable(m, to, opt, seen) {
				return false
			}
		}
		return true
	}
	if te, ok := to.(*Enum); ok {
		// Source must be one of the member literals (or the enum itself).
		for _, m := range te.Members {
			if assignable(from, m, opt, seen) {
				return true
			}
		}
		return false
	}

	// Classes (nominal instance types).
	if fc, ok := from.(*Class); ok {
		switch tt := to.(type) {
		case *Class:
			return classAssignable(fc, tt, opt, seen)
		case *Object:
			return classSatisfiesObject(fc, tt, opt, seen)
		case *Primitive:
			return tt.K == KTable
		}
		return false
	}
	if _, ok := to.(*Class); ok {
		return false
	}
	// Class values only alias the same class.
	if fv, ok := from.(*ClassValue); ok {
		if tv, ok := to.(*ClassValue); ok {
			return fv.Class != nil && tv.Class != nil &&
				fv.Class.Def != nil && fv.Class.Def == tv.Class.Def &&
				classArgsAssignable(fv.Class.Args, tv.Class.Args, opt, seen)
		}
		return false
	}
	if _, ok := to.(*ClassValue); ok {
		return false
	}

	// Chan (invariant: element types must match both ways, modulo any).
	if fc, ok := from.(*Chan); ok {
		if tc, ok := to.(*Chan); ok {
			return assignable(fc.Elem, tc.Elem, opt, seen) && assignable(tc.Elem, fc.Elem, opt, seen)
		}
		return false
	}
	if _, ok := to.(*Chan); ok {
		return false
	}

	// Array.
	if fa, ok := from.(*Array); ok {
		switch tt := to.(type) {
		case *Array:
			return assignable(fa.Elem, tt.Elem, opt, seen)
		case *Primitive:
			return tt.K == KTable
		case *Object:
			return objectAcceptsArray(tt, fa, opt, seen)
		case *Map:
			// arrayáci as map<integer, elem>?
			return assignable(Integer, tt.Key, opt, seen) && assignable(fa.Elem, tt.Value, opt, seen)
		}
		return false
	}
	if ta, ok := to.(*Array); ok {
		if fa, ok := from.(*Tuple); ok {
			for _, e := range fa.Elems {
				if !assignable(e, ta.Elem, opt, seen) {
					return false
				}
			}
			return true
		}
		if fo, ok := from.(*Object); ok {
			// `{}` is a valid empty array at runtime.
			return len(fo.Fields) == 0 && len(fo.Indexers) == 0
		}
		return false
	}

	// Map.
	if fm, ok := from.(*Map); ok {
		switch tt := to.(type) {
		case *Map:
			return assignable(fm.Key, tt.Key, opt, seen) && assignable(fm.Value, tt.Value, opt, seen)
		case *Primitive:
			return tt.K == KTable
		case *Object:
			// map to object: object indexers/fields must be satisfied by map's value?
			// Conservative: only if object has compatible indexer.
			for _, ix := range tt.Indexers {
				if !assignable(fm.Key, ix.Key, opt, seen) || !assignable(fm.Value, ix.Value, opt, seen) {
					return false
				}
			}
			for _, f := range tt.Fields {
				if f.Optional {
					continue
				}
				// Required field must be covered by map value? Need key compat: skip strict check.
				_ = f
			}
			return len(tt.Indexers) > 0 || len(tt.Fields) == 0
		}
		return false
	}

	// Tuple.
	if ft, ok := from.(*Tuple); ok {
		switch tt := to.(type) {
		case *Tuple:
			if len(ft.Elems) != len(tt.Elems) {
				return false
			}
			for i := range ft.Elems {
				if !assignable(ft.Elems[i], tt.Elems[i], opt, seen) {
					return false
				}
			}
			return true
		case *Array:
			for _, e := range ft.Elems {
				if !assignable(e, tt.Elem, opt, seen) {
					return false
				}
			}
			return true
		case *Primitive:
			return tt.K == KTable
		case *Object:
			return objectAcceptsArray(tt, &Array{Elem: unionOf(ft.Elems)}, opt, seen)
		}
		return false
	}
	if _, ok := to.(*Tuple); ok {
		return false
	}

	// Functions.
	if ff, ok := from.(*Func); ok {
		switch tt := to.(type) {
		case *Func:
			return funcAssignable(ff, tt, opt, seen)
		case *Primitive:
			return tt.K == KFunction
		}
		return false
	}
	if _, ok := to.(*Func); ok {
		return false
	}

	// Objects.
	if fo, ok := from.(*Object); ok {
		switch tt := to.(type) {
		case *Object:
			return objectAssignable(fo, tt, opt, seen)
		case *Primitive:
			return tt.K == KTable
		case *Map:
			// object to map: all fields must fit value type, keys fit key type.
			strKey := String
			// If map key is number/integer, object with only array-like? conservative false.
			for name, f := range fo.Fields {
				_ = name
				if !assignable(f.Type, tt.Value, opt, seen) {
					return false
				}
				if !assignable(strKey, tt.Key, opt, seen) {
					// Allow string|number keys etc.
					if !assignable(strKey, tt.Key, opt, seen) {
						return false
					}
				}
			}
			return true
		}
		return false
	}
	if _, ok := to.(*Object); ok {
		return false
	}

	// Ref should have been resolved; compare by name.
	if fr, ok := from.(*Ref); ok {
		if tr, ok := to.(*Ref); ok {
			if fr.Name != tr.Name || len(fr.Args) != len(tr.Args) {
				return false
			}
			for i := range fr.Args {
				if !assignable(fr.Args[i], tr.Args[i], opt, seen) {
					return false
				}
			}
			return true
		}
		return false
	}

	if from == to {
		return true
	}
	return from.String() == to.String()
}

func isPrim(t Type) bool {
	_, ok := t.(*Primitive)
	return ok
}

func isBaseOf(base, to Type) bool {
	if base == nil {
		return false
	}
	return assignable(base, to, Options{StrictNilChecks: true}, map[visit]bool{})
}

func primAssignable(from, to *Primitive) bool {
	if from.K == to.K {
		return true
	}
	// integer/float <: number
	if (from.K == KInteger || from.K == KFloat) && to.K == KNumber {
		return true
	}
	return false
}

// classArgsAssignable checks generic arguments covariantly.
func classArgsAssignable(from, to []Type, opt Options, seen map[visit]bool) bool {
	if len(from) != len(to) {
		return false
	}
	for i := range from {
		if !assignable(from[i], to[i], opt, seen) {
			return false
		}
	}
	return true
}

// classAssignable walks the inheritance chain: a subclass instance is
// assignable to its ancestors (with compatible type arguments).
func classAssignable(from, to *Class, opt Options, seen map[visit]bool) bool {
	visited := map[*Class]bool{}
	for c := from; c != nil; c = c.Base {
		if visited[c] {
			return false
		}
		visited[c] = true
		if c.Def != nil && to.Def != nil && c.Def == to.Def {
			return classArgsAssignable(c.Args, to.Args, opt, seen)
		}
		if c.Def == nil && to.Def == nil && c.Name != "" && c.Name == to.Name {
			return classArgsAssignable(c.Args, to.Args, opt, seen)
		}
	}
	return false
}

// classObjectView exposes public instance members structurally.
func classObjectView(c *Class) *Object {
	o := &Object{Fields: map[string]Field{}}
	for n, f := range c.Fields {
		if f.Access != Public {
			continue
		}
		o.Fields[n] = Field{Type: f.Type, Optional: f.Optional, Readonly: f.Readonly, Access: Public}
	}
	return o
}

func classSatisfiesObject(from *Class, to *Object, opt Options, seen map[visit]bool) bool {
	return objectAssignable(classObjectView(from), to, opt, seen)
}

func unionOf(elems []Type) Type {
	if len(elems) == 0 {
		return Never
	}
	return NewUnion(elems)
}

func objectAcceptsArray(obj *Object, arr *Array, opt Options, seen map[visit]bool) bool {
	// Array satisfies object if object has no required named fields and indexer accepts integer->elem.
	for _, f := range obj.Fields {
		if !f.Optional {
			return false
		}
	}
	if len(obj.Indexers) == 0 {
		return len(obj.Fields) == 0
	}
	for _, ix := range obj.Indexers {
		if !assignable(Integer, ix.Key, opt, seen) {
			return false
		}
		if !assignable(arr.Elem, ix.Value, opt, seen) {
			return false
		}
	}
	return true
}

func objectAssignable(from, to *Object, opt Options, seen map[visit]bool) bool {
	// An empty table literal satisfies anything without required fields.
	if len(from.Fields) == 0 && len(from.Indexers) == 0 {
		for _, tf := range to.Fields {
			if !tf.Optional {
				return false
			}
		}
		return true
	}
	// Every required field in `to` must exist compatibly in `from`.
	for name, tf := range to.Fields {
		ff, ok := from.Fields[name]
		if !ok {
			// Try indexers of from.
			covered := false
			for _, ix := range from.Indexers {
				if assignable(String, ix.Key, opt, seen) && assignable(ix.Value, tf.Type, opt, seen) {
					covered = true
					break
				}
			}
			if tf.Optional {
				continue
			}
			if covered {
				continue
			}
			return false
		}
		if !assignable(ff.Type, tf.Type, opt, seen) {
			return false
		}
	}
	// Every indexer in `to` must be satisfied.
	for _, tix := range to.Indexers {
		satisfied := false
		for _, fix := range from.Indexers {
			if assignable(fix.Key, tix.Key, opt, seen) && assignable(fix.Value, tix.Value, opt, seen) {
				satisfied = true
				break
			}
		}
		if satisfied {
			continue
		}
		// Named fields of from can satisfy a string indexer if all fit.
		if tix.Key.Kind() == KString || tix.Key.Kind() == KAny {
			allFit := len(from.Fields) > 0
			for _, f := range from.Fields {
				if !assignable(f.Type, tix.Value, opt, seen) {
					allFit = false
					break
				}
			}
			if allFit {
				continue
			}
		}
		return false
	}
	return true
}

func funcAssignable(from, to *Func, opt Options, seen map[visit]bool) bool {
	// Arity: source must accept at least as many args as target requires.
	// Count required target params.
	reqTo := 0
	for _, p := range to.Params {
		if !p.Optional {
			reqTo++
		}
	}
	maxFrom := len(from.Params)
	if !from.HasVarArg && len(to.Params) > maxFrom {
		// Target may pass more args than source accepts: check extras are optional? No, source fixed.
		// Allow extra target params only if source has vararg (handled) else fail when target passes more.
		// Actually assignability: target callable with N args must be callable on source.
		// If target has more params (even optional), source must accept them or have vararg.
		if len(to.Params) > len(from.Params) {
			return false
		}
	}
	if len(from.Params) < reqTo && !from.HasVarArg {
		// Source requires more? Wait: source params < target required => source can't handle target's required args? No.
		// Example: source (a: number) target (a: number, b: string): target caller passes 2 args, source takes 1 -> extra ignored in Lua! Lua allows extra args.
		// In Lua, extra args are silently dropped. So arity is flexible: fewer source params is OK.
		// Only fail if source requires more args than target guarantees.
	}
	reqFrom := 0
	for _, p := range from.Params {
		if !p.Optional {
			reqFrom++
		}
	}
	// Target must supply at least reqFrom args (its required count, or its total if no vararg?).
	// If target has vararg, it can supply arbitrarily many.
	maxTo := len(to.Params)
	if !to.HasVarArg && reqFrom > maxTo {
		return false
	}
	// Contravariant params for overlapping positions.
	n := len(to.Params)
	if len(from.Params) < n {
		n = len(from.Params)
	}
	for i := 0; i < n; i++ {
		// to.Param <: from.Param
		if !assignable(to.Params[i].Type, from.Params[i].Type, opt, seen) {
			return false
		}
	}
	// If target has extra params beyond source, source must have vararg compatible.
	if len(to.Params) > len(from.Params) {
		if !from.HasVarArg {
			// Lua drops extras, so OK as long as extras are optional? Actually caller through target type passes them, source ignores. Safe.
			// So OK.
		} else {
			for i := len(from.Params); i < len(to.Params); i++ {
				if from.VarArg == nil {
					continue
				}
				if !assignable(to.Params[i].Type, from.VarArg, opt, seen) {
					return false
				}
			}
		}
	}
	// If source has extra required params beyond target, target caller won't supply them -> fail unless source extras optional/vararg-fillable.
	if len(from.Params) > len(to.Params) {
		for i := len(to.Params); i < len(from.Params); i++ {
			if !from.Params[i].Optional {
				if !to.HasVarArg {
					return false
				}
				if to.VarArg == nil {
					return false
				}
				if !assignable(to.VarArg, from.Params[i].Type, opt, seen) {
					return false
				}
			}
		}
	}
	// Covariant returns: source returns must cover target returns.
	if len(to.Returns) == 0 {
		return true
	}
	if len(from.Returns) == 0 {
		// Source returns void but target expects values: only OK if target expects void/nil? Already len>0, so fail unless target allows nil?
		// void ~ nil: treat missing as nil.
		for _, tr := range to.Returns {
			if !assignable(Nil, tr, opt, seen) {
				return false
			}
		}
		return true
	}
	for i, tr := range to.Returns {
		var fr Type = Nil // missing extras are nil in Lua.
		if i < len(from.Returns) {
			fr = from.Returns[i]
		} else if from.HasVarArg && from.VarArg != nil {
			fr = from.VarArg
		}
		if !assignable(fr, tr, opt, seen) {
			return false
		}
	}
	return true
}
