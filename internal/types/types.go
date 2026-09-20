package types

import (
	"fmt"
	"sort"
	"strings"
)

// Kind classifies types.
type Kind int

const (
	KAny Kind = iota
	KUnknown
	KNever
	KVoid
	KNil
	KBoolean
	KNumber
	KInteger
	KFloat
	KString
	KTable
	KThread
	KUserdata
	KFunction
	KLiteral
	KUnion
	KIntersection
	KArray
	KMap
	KTuple
	KFunc
	KObject
	KEnum
	KGenericParam
	KRef
	KClass
	KClassValue
)

type Type interface {
	Kind() Kind
	String() string
}

// Access is member visibility (classes only; plain objects are always public).
type Access int

const (
	Public Access = iota
	Private
	Protected
)

func (a Access) String() string {
	switch a {
	case Private:
		return "private"
	case Protected:
		return "protected"
	}
	return "public"
}

type Field struct {
	Type     Type
	Optional bool
	Readonly bool
	Access   Access
	Owner    *ClassDef // defining class for class members; nil otherwise
}

type Indexer struct {
	Key   Type
	Value Type
}

type Primitive struct {
	K    Kind
	Name string
}

func (t *Primitive) Kind() Kind    { return t.K }
func (t *Primitive) String() string { return t.Name }

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
func (t *Union) String() string {
	parts := make([]string, len(t.Types))
	for i, x := range t.Types {
		parts[i] = x.String()
	}
	return strings.Join(parts, " | ")
}

type Intersection struct {
	Types []Type
}

func (t *Intersection) Kind() Kind { return KIntersection }
func (t *Intersection) String() string {
	parts := make([]string, len(t.Types))
	for i, x := range t.Types {
		parts[i] = x.String()
	}
	return strings.Join(parts, " & ")
}

type Array struct {
	Elem Type
}

func (t *Array) Kind() Kind { return KArray }
func (t *Array) String() string {
	return t.Elem.String() + "[]"
}

type Map struct {
	Key   Type
	Value Type
}

func (t *Map) Kind() Kind { return KMap }
func (t *Map) String() string {
	return fmt.Sprintf("map<%s, %s>", t.Key.String(), t.Value.String())
}

type Tuple struct {
	Elems []Type
}

func (t *Tuple) Kind() Kind { return KTuple }
func (t *Tuple) String() string {
	parts := make([]string, len(t.Elems))
	for i, e := range t.Elems {
		parts[i] = e.String()
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

type FuncParam struct {
	Name     string
	Type     Type
	Optional bool
}

type Func struct {
	Params    []FuncParam
	HasVarArg bool
	VarArg    Type
	Returns   []Type
}

func (t *Func) Kind() Kind { return KFunc }
func (t *Func) String() string {
	var ps []string
	for _, p := range t.Params {
		if p.Name != "" {
			if p.Optional {
				ps = append(ps, p.Name+"?: "+p.Type.String())
			} else {
				ps = append(ps, p.Name+": "+p.Type.String())
			}
		} else {
			ps = append(ps, p.Type.String())
		}
	}
	if t.HasVarArg {
		if t.VarArg != nil {
			ps = append(ps, "...: "+t.VarArg.String())
		} else {
			ps = append(ps, "...")
		}
	}
	ret := "void"
	if len(t.Returns) == 1 {
		ret = t.Returns[0].String()
	} else if len(t.Returns) > 1 {
		rs := make([]string, len(t.Returns))
		for i, r := range t.Returns {
			rs[i] = r.String()
		}
		ret = "(" + strings.Join(rs, ", ") + ")"
	}
	return "(" + strings.Join(ps, ", ") + ") -> " + ret
}

type Object struct {
	Name     string // interface name if named, else ""
	Fields   map[string]Field
	Indexers []Indexer
}

func (t *Object) Kind() Kind { return KObject }
func (t *Object) String() string {
	if t.Name != "" && len(t.Fields) == 0 && len(t.Indexers) == 0 {
		return t.Name
	}
	var parts []string
	names := make([]string, 0, len(t.Fields))
	for n := range t.Fields {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		f := t.Fields[n]
		q := ""
		if f.Optional {
			q = "?"
		}
		parts = append(parts, fmt.Sprintf("%s%s: %s", n, q, f.Type.String()))
	}
	for _, ix := range t.Indexers {
		parts = append(parts, fmt.Sprintf("[%s]: %s", ix.Key.String(), ix.Value.String()))
	}
	if t.Name != "" {
		return t.Name + "{ " + strings.Join(parts, ", ") + " }"
	}
	return "{ " + strings.Join(parts, ", ") + " }"
}

type Enum struct {
	Name       string
	Underlying Type // string or number (or union)
	Members    map[string]Type
}

func (t *Enum) Kind() Kind { return KEnum }
func (t *Enum) String() string { return t.Name }

type GenericParam struct {
	Name       string
	Constraint Type
	Default    Type
}

func (t *GenericParam) Kind() Kind { return KGenericParam }
func (t *GenericParam) String() string {
	if t.Constraint != nil {
		return t.Name + " extends " + t.Constraint.String()
	}
	return t.Name
}

// Ref is an unresolved named type with optional type arguments.
type Ref struct {
	Name string
	Args []Type
}

func (t *Ref) Kind() Kind { return KRef }
func (t *Ref) String() string {
	if len(t.Args) == 0 {
		return t.Name
	}
	ps := make([]string, len(t.Args))
	for i, a := range t.Args {
		ps[i] = a.String()
	}
	return t.Name + "<" + strings.Join(ps, ", ") + ">"
}

// ClassField is a single class member (field or method).
type ClassField struct {
	Type     Type
	Optional bool // fields only; methods are never optional
	Readonly bool
	Access   Access
	IsMethod bool
	Abstract bool
	Owner    *ClassDef // class that declared this member (for visibility)
}

// ClassDef is the identity of a class declaration. All instantiations of
// a generic class share one Def; nominal assignability compares Defs.
type ClassDef struct {
	Name string
}

// Class is a nominal instance type. Two class types are assignable only
// along the inheritance chain; against plain objects/interfaces the
// public members are checked structurally.
type Class struct {
	Def        *ClassDef
	Name       string
	Args       []Type // instantiation arguments for generic classes
	Params     []string
	Fields     map[string]ClassField // instance members (fields + methods)
	Base       *Class
	IsAbstract bool
}

func (t *Class) Kind() Kind { return KClass }
func (t *Class) String() string {
	if len(t.Args) == 0 {
		return t.Name
	}
	ps := make([]string, len(t.Args))
	for i, a := range t.Args {
		ps[i] = a.String()
	}
	return t.Name + "<" + strings.Join(ps, ", ") + ">"
}

// ObjectView exposes public instance members structurally (for
// interface conformance checks).
func (t *Class) ObjectView() *Object { return classObjectView(t) }

// ClassValue is the runtime class table: constructor plus statics.
// Calling it (or its `new` method) creates an instance.
type ClassValue struct {
	Class       *Class // instance type produced (generic definition form)
	Constructor *Func  // nil only for declare stubs (treated as any)
	CtorAccess  Access // visibility of the constructor
	Statics     map[string]ClassField
	Abstract    bool
}

func (t *ClassValue) Kind() Kind { return KClassValue }
func (t *ClassValue) String() string {
	if t.Class != nil {
		return "typeof " + t.Class.Name
	}
	return "class"
}

// ---------- constructors ----------

// NewUnion flattens nested unions, removes duplicates and never, handles any/unknown.
func NewUnion(types []Type) Type {
	var flat []Type
	for _, t := range types {
		if t == nil {
			continue
		}
		if u, ok := t.(*Union); ok {
			flat = append(flat, u.Types...)
			continue
		}
		if t.Kind() == KNever {
			continue
		}
		flat = append(flat, t)
	}
	// any absorbs everything; unknown absorbs (except any).
	for _, t := range flat {
		if t.Kind() == KAny {
			return Any
		}
	}
	for _, t := range flat {
		if t.Kind() == KUnknown {
			return Unknown
		}
	}
	// Dedup by string.
	seen := map[string]bool{}
	var out []Type
	for _, t := range flat {
		s := t.String()
		if !seen[s] {
			seen[s] = true
			out = append(out, t)
		}
	}
	if len(out) == 0 {
		return Never
	}
	if len(out) == 1 {
		return out[0]
	}
	// Canonical order for stable messages.
	sort.Slice(out, func(i, j int) bool { return out[i].String() < out[j].String() })
	return &Union{Types: out}
}

// NewIntersection flattens; never absorbs; any is identity.
func NewIntersection(types []Type) Type {
	var flat []Type
	for _, t := range types {
		if t == nil {
			continue
		}
		if u, ok := t.(*Intersection); ok {
			flat = append(flat, u.Types...)
			continue
		}
		if t.Kind() == KAny {
			continue
		}
		if t.Kind() == KNever {
			return Never
		}
		flat = append(flat, t)
	}
	if len(flat) == 0 {
		return Unknown
	}
	seen := map[string]bool{}
	var out []Type
	for _, t := range flat {
		s := t.String()
		if !seen[s] {
			seen[s] = true
			out = append(out, t)
		}
	}
	if len(out) == 1 {
		return out[0]
	}
	sort.Slice(out, func(i, j int) bool { return out[i].String() < out[j].String() })
	return &Intersection{Types: out}
}

// RemoveNil returns T without nil, and whether nil was present.
func RemoveNil(t Type) (Type, bool) {
	u, ok := t.(*Union)
	if !ok {
		if t.Kind() == KNil {
			return Never, true
		}
		return t, false
	}
	var rest []Type
	found := false
	for _, m := range u.Types {
		if m.Kind() == KNil {
			found = true
			continue
		}
		rest = append(rest, m)
	}
	if !found {
		return t, false
	}
	return NewUnion(rest), true
}

// ContainsNil reports whether nil is in the type.
func ContainsNil(t Type) bool {
	if t.Kind() == KNil {
		return true
	}
	if u, ok := t.(*Union); ok {
		for _, m := range u.Types {
			if m.Kind() == KNil {
				return true
			}
		}
	}
	return false
}

// ---------- subtyping ----------

type Options struct {
	StrictNilChecks bool
}

// IsAssignable reports whether `from` can be assigned to `to`.
func IsAssignable(from, to Type, opt Options) bool {
	return assignable(from, to, opt, map[[2]string]bool{})
}

func typeID(t Type) string { return fmt.Sprintf("%p:%s", t, t.String()) }

func assignable(from, to Type, opt Options, seen map[[2]string]bool) bool {
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
	key := [2]string{typeID(from), typeID(to)}
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
	if _, ok := from.(*GenericParam); ok {
		// A bare type param is assignable only to its constraint/unknown/any.
		// Conservative: allow to same param.
		return from.String() == to.String()
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
	return assignable(base, to, Options{StrictNilChecks: true}, map[[2]string]bool{})
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
func classArgsAssignable(from, to []Type, opt Options, seen map[[2]string]bool) bool {
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
func classAssignable(from, to *Class, opt Options, seen map[[2]string]bool) bool {
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

func classSatisfiesObject(from *Class, to *Object, opt Options, seen map[[2]string]bool) bool {
	return objectAssignable(classObjectView(from), to, opt, seen)
}

func unionOf(elems []Type) Type {
	if len(elems) == 0 {
		return Never
	}
	return NewUnion(elems)
}

func objectAcceptsArray(obj *Object, arr *Array, opt Options, seen map[[2]string]bool) bool {
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

func objectAssignable(from, to *Object, opt Options, seen map[[2]string]bool) bool {
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

func funcAssignable(from, to *Func, opt Options, seen map[[2]string]bool) bool {
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
		return &Func{Params: ps, HasVarArg: x.HasVarArg, VarArg: va, Returns: rs}
	case *Object:
		nf := map[string]Field{}
		for n, f := range x.Fields {
			nf[n] = Field{Type: substitute(f.Type, sub, ctx), Optional: f.Optional, Readonly: f.Readonly, Access: f.Access, Owner: f.Owner}
		}
		var nix []Indexer
		for _, ix := range x.Indexers {
			nix = append(nix, Indexer{Key: substitute(ix.Key, sub, ctx), Value: substitute(ix.Value, sub, ctx)})
		}
		return &Object{Name: x.Name, Fields: nf, Indexers: nix}
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
			na[i] = Substitute(a, sub)
		}
		return &Ref{Name: x.Name, Args: na}
	case *Enum:
		return x
	case *Literal:
		return x
	case *Primitive:
		return x
	}
	return t
}
