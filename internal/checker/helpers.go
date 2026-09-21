package checker

import (
	"strconv"
	"strings"

	"github.com/typelua/typelua/internal/types"
)

// Small type predicates, field/index lookup shared by checking.

// ---------- helpers ----------

func isAnyLike(t types.Type) bool {
	return t.Kind() == types.KAny || t.Kind() == types.KUnknown
}

// unwrapGeneric returns the constraint for `T extends C`, or Any for unconstrained params
// (permissive to avoid false positives on generic code).
func unwrapGeneric(t types.Type) (types.Type, bool) {
	if g, ok := t.(*types.GenericParam); ok {
		if g.Constraint != nil {
			return g.Constraint, true
		}
		return types.Any, true
	}
	return t, false
}

func isNumberLike(t types.Type) bool {
	if u, ok := unwrapGeneric(t); ok {
		return isNumberLike(u)
	}
	switch x := t.(type) {
	case *types.Primitive:
		return x.K == types.KNumber || x.K == types.KInteger || x.K == types.KFloat
	case *types.Literal:
		return x.Base == types.Number || x.Base == types.Integer || x.Base == types.Float
	case *types.Union:
		for _, m := range x.Types {
			if !isNumberLike(m) {
				return false
			}
		}
		return true
	case *types.Enum:
		return isNumberLike(x.Underlying)
	}
	return isAnyLike(t)
}

func isIntegerLike(t types.Type) bool {
	if u, ok := unwrapGeneric(t); ok {
		return isIntegerLike(u)
	}
	switch x := t.(type) {
	case *types.Primitive:
		return x.K == types.KInteger
	case *types.Literal:
		return x.Base == types.Integer
	case *types.Union:
		for _, m := range x.Types {
			if !isIntegerLike(m) {
				return false
			}
		}
		return true
	}
	return isAnyLike(t)
}

func isConcatable(t types.Type) bool {
	if isAnyLike(t) {
		return true
	}
	if u, ok := unwrapGeneric(t); ok {
		return isConcatable(u)
	}
	switch x := t.(type) {
	case *types.Primitive:
		return x.K == types.KString || x.K == types.KNumber || x.K == types.KInteger || x.K == types.KFloat
	case *types.Literal:
		return x.Base == types.String || x.Base == types.Number || x.Base == types.Integer || x.Base == types.Float
	case *types.Union:
		for _, m := range x.Types {
			if !isConcatable(m) {
				return false
			}
		}
		return true
	case *types.Enum:
		return isConcatable(x.Underlying)
	}
	return false
}

func isComparable(l, r types.Type) bool {
	if isAnyLike(l) || isAnyLike(r) {
		return true
	}
	if _, ok := l.(*types.GenericParam); ok {
		return true
	}
	if _, ok := r.(*types.GenericParam); ok {
		return true
	}
	// Numbers compare with numbers, strings with strings.
	if isNumberLike(l) && isNumberLike(r) {
		return true
	}
	if isStringLike(l) && isStringLike(r) {
		return true
	}
	// Literals of same base.
	if l.String() == r.String() {
		return true
	}
	return false
}

func isStringLike(t types.Type) bool {
	if u, ok := unwrapGeneric(t); ok {
		return isStringLike(u)
	}
	switch x := t.(type) {
	case *types.Primitive:
		return x.K == types.KString
	case *types.Literal:
		return x.Base == types.String
	case *types.Union:
		for _, m := range x.Types {
			if !isStringLike(m) {
				return false
			}
		}
		return true
	case *types.Enum:
		return isStringLike(x.Underlying)
	}
	return isAnyLike(t)
}

func isLengthable(t types.Type) bool {
	if isAnyLike(t) {
		return true
	}
	if u, ok := unwrapGeneric(t); ok {
		return isLengthable(u)
	}
	switch x := t.(type) {
	case *types.Primitive:
		return x.K == types.KString || x.K == types.KTable
	case *types.Array, *types.Map, *types.Object, *types.Tuple:
		return true
	case *types.Literal:
		return x.Base == types.String
	case *types.Union:
		for _, m := range x.Types {
			if !isLengthable(m) {
				return false
			}
		}
		return true
	}
	return false
}

func lookupField(ot types.Type, name string) (types.Field, bool) {
	switch x := ot.(type) {
	case *types.Union:
		// Distribute: every member must declare the field (nil and
		// field-less members fail the whole access, like TS).
		var ts []types.Type
		opt, ro := false, false
		acc := types.Public
		var owner *types.ClassDef
		for _, m := range x.Types {
			f, ok := lookupField(m, name)
			if !ok {
				return types.Field{}, false
			}
			ts = append(ts, f.Type)
			if f.Optional {
				opt = true
			}
			if f.Readonly {
				ro = true
			}
			if f.Access == types.Private || (f.Access == types.Protected && acc == types.Public) {
				acc = f.Access
				owner = f.Owner
			} else if owner == nil {
				owner = f.Owner
			}
		}
		return types.Field{Type: types.NewUnion(ts), Optional: opt, Readonly: ro, Access: acc, Owner: owner}, true
	case *types.Object:
		if f, ok := x.Fields[name]; ok {
			return f, true
		}
		// Indexer with string key?
		for _, ix := range x.Indexers {
			if ix.Key.Kind() == types.KString || ix.Key.Kind() == types.KAny {
				return types.Field{Type: ix.Value}, true
			}
			if lit, ok := ix.Key.(*types.Literal); ok && lit.Base == types.String {
				return types.Field{Type: ix.Value}, true
			}
		}
		// Enum runtime tables are Objects; handled above.
		return types.Field{}, false
	case *types.Enum:
		// Enum value access returns member literal.
		if m, ok := x.Members[name]; ok {
			return types.Field{Type: m}, true
		}
		return types.Field{}, false
	case *types.Map:
		// `m.field` where m: map<K,V>: allowed if K accepts strings.
		if types.IsAssignable(types.String, x.Key, types.Options{StrictNilChecks: true}) || isAnyLike(x.Key) {
			return types.Field{Type: x.Value}, true
		}
		return types.Field{}, false
	case *types.Class:
		// Instance members (flattened to include inherited, minus privates).
		if f, ok := x.Fields[name]; ok {
			return types.Field{Type: f.Type, Optional: f.Optional, Readonly: f.Readonly, Access: f.Access, Owner: f.Owner}, true
		}
		return types.Field{}, false
	case *types.ClassValue:
		if f, ok := x.Statics[name]; ok {
			return types.Field{Type: f.Type, Optional: f.Optional, Readonly: f.Readonly, Access: f.Access, Owner: f.Owner}, true
		}
		if name == "new" {
			if x.Constructor == nil {
				return types.Field{Type: types.Any, Access: x.CtorAccess, Owner: classDefOf(x)}, true
			}
			return types.Field{Type: &types.Func{Params: x.Constructor.Params, HasVarArg: x.Constructor.HasVarArg, VarArg: x.Constructor.VarArg, Returns: []types.Type{x.Class}, TypeParams: x.Class.TypeParams}, Access: x.CtorAccess, Owner: classDefOf(x)}, true
		}
		return types.Field{}, false
	case *types.Primitive:
		if x.K == types.KTable {
			return types.Field{Type: types.Any}, true
		}
	}
	return types.Field{}, false
}

func classDefOf(v *types.ClassValue) *types.ClassDef {
	if v == nil || v.Class == nil {
		return nil
	}
	return v.Class.Def
}

func indexResult(ot, kt types.Type) types.Type {
	if isAnyLike(ot) || isAnyLike(kt) {
		if isAnyLike(ot) {
			return types.Any
		}
		// ot concrete, kt any => best effort.
		switch x := ot.(type) {
		case *types.Array:
			return x.Elem
		case *types.Map:
			return x.Value
		case *types.Object:
			if len(x.Indexers) > 0 {
				return x.Indexers[0].Value
			}
			return types.Any
		case *types.Tuple:
			return types.NewUnion(x.Elems)
		case *types.Primitive:
			if x.K == types.KString {
				// string[i]? Lua 5.4 no, but allow any? Return string? Actually string indexing not valid; return nil.
				return nil
			}
			if x.K == types.KTable {
				return types.Any
			}
		}
		return types.Any
	}
	switch x := ot.(type) {
	case *types.Union:
		var rs []types.Type
		for _, m := range x.Types {
			r := indexResult(m, kt)
			if r == nil {
				return nil
			}
			rs = append(rs, r)
		}
		return types.NewUnion(rs)
	case *types.Array:
		if isIntegerLike(kt) || isNumberLike(kt) {
			return x.Elem
		}
		return nil
	case *types.Map:
		return x.Value
	case *types.Tuple:
		if lit, ok := kt.(*types.Literal); ok {
			if idx, err := strconv.Atoi(lit.Value); err == nil {
				if idx >= 1 && idx <= len(x.Elems) {
					return x.Elems[idx-1]
				}
			}
		}
		return nil
	case *types.Object:
		// Literal string/number keys map to fields.
		if lit, ok := kt.(*types.Literal); ok {
			key := strings.Trim(lit.Value, `"'`)
			// Number literal for array-like indexers.
			if lit.Base == types.Number || lit.Base == types.Integer || lit.Base == types.Float {
				for _, ix := range x.Indexers {
					if ix.Key.Kind() == types.KInteger || ix.Key.Kind() == types.KNumber {
						return ix.Value
					}
				}
				if f, ok := x.Fields[key]; ok {
					return f.Type
				}
				return nil
			}
			if f, ok := x.Fields[key]; ok {
				if f.Optional {
					return types.NewUnion([]types.Type{f.Type, types.Nil})
				}
				return f.Type
			}
			for _, ix := range x.Indexers {
				if ix.Key.Kind() == types.KString || ix.Key.Kind() == types.KAny {
					return ix.Value
				}
			}
			return nil
		}
		for _, ix := range x.Indexers {
			return ix.Value
		}
		return nil
	case *types.Primitive:
		if x.K == types.KTable {
			return types.Any
		}
		if x.K == types.KString {
			return nil
		}
	}
	return nil
}
