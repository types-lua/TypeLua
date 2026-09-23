package types

import (
	"fmt"
	"strings"
)

var (
	Any      = &Primitive{KAny, "any"}
	Unknown  = &Primitive{KUnknown, "unknown"}
	Never    = &Primitive{KNever, "never"}
	Void     = &Primitive{KVoid, "void"}
	Nil      = &Primitive{KNil, "nil"}
	Boolean  = &Primitive{KBoolean, "boolean"}
	Number   = &Primitive{KNumber, "number"}
	Integer  = &Primitive{KInteger, "integer"}
	Float    = &Primitive{KFloat, "float"}
	String   = &Primitive{KString, "string"}
	Table    = &Primitive{KTable, "table"}
	Thread   = &Primitive{KThread, "thread"}
	Userdata = &Primitive{KUserdata, "userdata"}
	Function = &Primitive{KFunction, "function"}
)

// Literal is a string/number/boolean literal type.
type Literal struct {
	Value string // source spelling, e.g. `"red"`, `42`, `true`
	Base  Type   // String/Number/Boolean/Nil
}

func (t *Literal) Kind() Kind { return KLiteral }
func (t *Literal) String() string { return t.Value }

type Union struct {
	Types []Type
}

func (t *Union) Kind() Kind { return KUnion }

// typeString formats t with a visited set: shared *Object nodes reached
// twice (recursive declarations) collapse to their name (or "{...}"),
// so printing a cyclic type terminates. Only objects can recur
// structurally (classes print nominally); containers forward the set.
func typeString(t Type, seen map[Type]bool) string {
	switch x := t.(type) {
	case *Object:
		if seen[x] {
			if x.Name != "" {
				return x.Name
			}
			return "{...}"
		}
		seen[x] = true
		return x.stringSeen(seen)
	case *Union:
		return x.stringSeen(seen)
	case *Intersection:
		return x.stringSeen(seen)
	case *Array:
		return x.stringSeen(seen)
	case *Map:
		return x.stringSeen(seen)
	case *Tuple:
		return x.stringSeen(seen)
	case *Func:
		return x.stringSeen(seen)
	case *Class:
		return x.stringSeen(seen)
	case *GenericParam:
		return x.stringSeen(seen)
	case *Ref:
		return x.stringSeen(seen)
	case *Conditional:
		return x.stringSeen(seen)
	case *Mapped:
		return x.stringSeen(seen)
	case *IndexedAccess:
		return x.stringSeen(seen)
	case *Chan:
		return x.stringSeen(seen)
	default:
		return t.String()
	}
}
func (t *Union) String() string { return t.stringSeen(map[Type]bool{}) }
func (t *Union) stringSeen(seen map[Type]bool) string {
	parts := make([]string, len(t.Types))
	for i, x := range t.Types {
		parts[i] = typeString(x, seen)
	}
	return strings.Join(parts, " | ")
}

type Intersection struct {
	Types []Type
}

func (t *Intersection) Kind() Kind { return KIntersection }
func (t *Intersection) String() string { return t.stringSeen(map[Type]bool{}) }
func (t *Intersection) stringSeen(seen map[Type]bool) string {
	parts := make([]string, len(t.Types))
	for i, x := range t.Types {
		parts[i] = typeString(x, seen)
	}
	return strings.Join(parts, " & ")
}

type Array struct {
	Elem Type
}

func (t *Array) Kind() Kind { return KArray }
func (t *Array) String() string { return t.stringSeen(map[Type]bool{}) }
func (t *Array) stringSeen(seen map[Type]bool) string {
	return typeString(t.Elem, seen) + "[]"
}

type Map struct {
	Key   Type
	Value Type
}

func (t *Map) Kind() Kind { return KMap }
func (t *Map) String() string { return t.stringSeen(map[Type]bool{}) }
func (t *Map) stringSeen(seen map[Type]bool) string {
	return fmt.Sprintf("map<%s, %s>", typeString(t.Key, seen), typeString(t.Value, seen))
}

type Tuple struct {
	Elems []Type
}

func (t *Tuple) Kind() Kind { return KTuple }
func (t *Tuple) String() string { return t.stringSeen(map[Type]bool{}) }
func (t *Tuple) stringSeen(seen map[Type]bool) string {
	parts := make([]string, len(t.Elems))
	for i, e := range t.Elems {
		parts[i] = typeString(e, seen)
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

// Chan is `chan<T>`: a CSP channel carrying values of Elem.
// Invariant in T (like Go): chan<A> is assignable to chan<B> only when
// A and B are mutually assignable (practically: identical, modulo any).
type Chan struct {
	Elem Type
}

func (t *Chan) Kind() Kind { return KChan }
func (t *Chan) String() string { return t.stringSeen(map[Type]bool{}) }
func (t *Chan) stringSeen(seen map[Type]bool) string {
	return fmt.Sprintf("chan<%s>", typeString(t.Elem, seen))
}
