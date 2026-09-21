package types

import (
	"strconv"
)

// Mapped is `{ [K in Keys]?: V }`. Concrete domains expand eagerly
// (see evalMapped); anything still generic stays deferred.
// Homomorphic (non-nil) is the `keyof` base object whose field modifiers
// are inherited unless overridden by an explicit `?`/`-?`/`readonly`/
// `-readonly` marker; nil means absolute (markers decide alone).
type Mapped struct {
	Param        string
	Constraint   Type
	Optional     bool // `?` or `+?`
	OptMinus     bool // `-?`
	Readonly     bool // `readonly` or `+readonly`
	RoMinus      bool // `-readonly`
	Homomorphic  Type
	Value        Type
}

func (t *Mapped) Kind() Kind { return KMapped }
func (t *Mapped) String() string { return t.stringSeen(map[Type]bool{}) }
func (t *Mapped) stringSeen(seen map[Type]bool) string {
	q := ""
	if t.OptMinus {
		q = "-?"
	} else if t.Optional {
		q = "?"
	}
	ro := ""
	if t.RoMinus {
		ro = "-readonly "
	} else if t.Readonly {
		ro = "readonly "
	}
	return "{ " + ro + "[" + t.Param + " in " + typeString(t.Constraint, seen) + "]" + q + ": " + typeString(t.Value, seen) + " }"
}

// IndexedAccess is `T[K]`, evaluated when both sides are concrete.
type IndexedAccess struct {
	Obj   Type
	Index Type
}

func (t *IndexedAccess) Kind() Kind { return KIndexedAccess }
func (t *IndexedAccess) String() string { return t.stringSeen(map[Type]bool{}) }
func (t *IndexedAccess) stringSeen(seen map[Type]bool) string {
	return typeString(t.Obj, seen) + "[" + typeString(t.Index, seen) + "]"
}

// literalKeyName normalizes a literal to an object field name.
func literalKeyName(t Type) (string, bool) {
	lit, ok := t.(*Literal)
	if !ok {
		return "", false
	}
	v := lit.Value
	if len(v) >= 2 && ((v[0] == '"' && v[len(v)-1] == '"') || (v[0] == '\'' && v[len(v)-1] == '\'')) {
		return v[1 : len(v)-1], true
	}
	return v, true
}

// evalMapped expands a mapped type over a concrete domain.
// Returns ok=false when the constraint still mentions parameters.
func evalMapped(m *Mapped, opt Options) (Type, bool) {
	if !IsConcreteType(m.Constraint) {
		return m, false
	}
	// Index-signature form: `{[K in string]: V}`.
	if prim, ok := m.Constraint.(*Primitive); ok &&
		(prim.K == KString || prim.K == KNumber || prim.K == KInteger) {
		return &Object{Fields: map[string]Field{}, Indexers: []Indexer{{
			Key:   prim,
			Value: Substitute(m.Value, map[string]Type{m.Param: prim}),
		}}}, true
	}
	// Literal domain: one field per key.
	keys := unionMembers(m.Constraint)
	var lits []Type
	for _, k := range keys {
		if _, ok := k.(*Literal); !ok {
			return m, false
		}
		lits = append(lits, k)
	}
	if len(lits) == 0 {
		return &Object{Fields: map[string]Field{}}, true
	}
	fields := map[string]Field{}
	// Homomorphic base for modifier inheritance (`keyof` object).
	var base *Object
	if m.Homomorphic != nil {
		if o, ok := m.Homomorphic.(*Object); ok {
			base = o
		}
	}
	for _, k := range lits {
		name, _ := literalKeyName(k)
		opt, ro := m.Optional, m.Readonly
		if m.OptMinus {
			opt = false
		} else if !m.Optional && base != nil {
			if f, ok := base.Fields[name]; ok && f.Optional {
				opt = true
			}
		}
		if m.RoMinus {
			ro = false
		} else if !m.Readonly && base != nil {
			if f, ok := base.Fields[name]; ok && f.Readonly {
				ro = true
			}
		}
		fields[name] = Field{
			Type:     Substitute(m.Value, map[string]Type{m.Param: k}),
			Optional: opt,
			Readonly: ro,
		}
	}
	return &Object{Fields: fields}, true
}

func unionMembers(t Type) []Type {
	if u, ok := t.(*Union); ok {
		return u.Types
	}
	return []Type{t}
}

// evalIndexedAccess resolves `T[K]`; ok=false defers (generic sides) while
// ok=true with an error string reports a definite mistake.
func evalIndexedAccess(obj, index Type, opt Options) (Type, bool, string) {
	if !IsConcreteType(obj) || !IsConcreteType(index) {
		return &IndexedAccess{Obj: obj, Index: index}, false, ""
	}
	if anyLike(obj) {
		return Any, true, ""
	}
	if obj.Kind() == KNever {
		return Never, true, ""
	}
	// Distribute unions on either side (all members must resolve).
	if u, ok := index.(*Union); ok {
		var rs []Type
		for _, m := range u.Types {
			r, ok, errStr := evalIndexedAccess(obj, m, opt)
			if !ok {
				return &IndexedAccess{Obj: obj, Index: index}, false, ""
			}
			if errStr != "" {
				return nil, true, errStr
			}
			rs = append(rs, r)
		}
		return NewUnion(rs), true, ""
	}
	if u, ok := obj.(*Union); ok {
		var rs []Type
		for _, m := range u.Types {
			r, ok, errStr := evalIndexedAccess(m, index, opt)
			if !ok {
				return &IndexedAccess{Obj: obj, Index: index}, false, ""
			}
			if errStr != "" {
				return nil, true, errStr
			}
			rs = append(rs, r)
		}
		return NewUnion(rs), true, ""
	}
	switch o := obj.(type) {
	case *Object:
		if name, ok := literalKeyName(index); ok {
			if f, ok := o.Fields[name]; ok {
				if f.Optional {
					return NewUnion([]Type{f.Type, Nil}), true, ""
				}
				return f.Type, true, ""
			}
			// Literal key against indexers.
			for _, ix := range o.Indexers {
				if keyAcceptsLiteral(ix.Key, index) {
					return ix.Value, true, ""
				}
			}
			return nil, true, "type '" + obj.String() + "' has no field '" + name + "'"
		}
		// General key: string over fields, or a matching indexer.
		if index.Kind() == KString || index.Kind() == KAny {
			var rs []Type
			for _, f := range o.Fields {
				rs = append(rs, f.Type)
			}
			for _, ix := range o.Indexers {
				if ix.Key.Kind() == KString || ix.Key.Kind() == KAny {
					rs = append(rs, ix.Value)
				}
			}
			if len(rs) == 0 {
				return nil, true, "cannot index type '" + obj.String() + "' with '" + index.String() + "'"
			}
			return NewUnion(rs), true, ""
		}
		return nil, true, "cannot index type '" + obj.String() + "' with '" + index.String() + "'"
	case *Tuple:
		if lit, ok := index.(*Literal); ok {
			n, err := tupleIndex(lit.Value)
			if err == nil && n >= 1 && n <= len(o.Elems) {
				return o.Elems[n-1], true, ""
			}
			return nil, true, "tuple index '" + lit.Value + "' out of range"
		}
		if index.Kind() == KNumber || index.Kind() == KInteger {
			if len(o.Elems) == 0 {
				return Never, true, ""
			}
			return NewUnion(o.Elems), true, ""
		}
		return nil, true, "cannot index tuple with '" + index.String() + "'"
	case *Array:
		if _, ok := index.(*Literal); ok {
			return o.Elem, true, ""
		}
		if index.Kind() == KNumber || index.Kind() == KInteger {
			return o.Elem, true, ""
		}
		return nil, true, "cannot index array with '" + index.String() + "'"
	case *Map:
		if IsAssignable(index, o.Key, opt) || IsAssignable(o.Key, index, opt) {
			return o.Value, true, ""
		}
		return nil, true, "cannot index type '" + obj.String() + "' with '" + index.String() + "'"
	case *Class:
		if name, ok := literalKeyName(index); ok {
			if f, ok := o.Fields[name]; ok && f.Access == Public {
				if f.Optional {
					return NewUnion([]Type{f.Type, Nil}), true, ""
				}
				return f.Type, true, ""
			}
			return nil, true, "type '" + obj.String() + "' has no field '" + name + "'"
		}
		return nil, true, "cannot index type '" + obj.String() + "' with '" + index.String() + "'"
	case *Enum:
		if name, ok := literalKeyName(index); ok {
			if m, ok := o.Members[name]; ok {
				return m, true, ""
			}
			return nil, true, "type '" + obj.String() + "' has no member '" + name + "'"
		}
		return nil, true, "cannot index type '" + obj.String() + "' with '" + index.String() + "'"
	}
	return nil, true, "cannot index type '" + obj.String() + "' with '" + index.String() + "'"
}

func keyAcceptsLiteral(key, lit Type) bool {
	switch key.Kind() {
	case KString, KAny:
		if l, ok := lit.(*Literal); ok {
			return l.Base == String || l.Base == Integer || l.Base == Float || l.Base == Number
		}
		return true
	case KInteger, KNumber:
		if l, ok := lit.(*Literal); ok {
			return l.Base == Integer || l.Base == Float || l.Base == Number
		}
	}
	return false
}

func tupleIndex(v string) (int, error) {
	return strconv.Atoi(v)
}

// anyLike mirrors the checker's permissive dynamic types.
func anyLike(t Type) bool {
	return t.Kind() == KAny || t.Kind() == KUnknown
}

// TryEvalMapped evaluates m when its domain is concrete.
func TryEvalMapped(m *Mapped, opt Options) (Type, bool) {
	return evalMapped(m, opt)
}

// TryEvalIndexed evaluates `obj[index]` when both sides are concrete.
// The error string is non-empty for definite mistakes.
func TryEvalIndexed(obj, index Type, opt Options) (Type, bool, string) {
	return evalIndexedAccess(obj, index, opt)
}
