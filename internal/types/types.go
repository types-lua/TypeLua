package types

import (
	"fmt"
	"sort"
	"strconv"
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
	KConditional
	KInferParam
	KMapped
	KIndexedAccess
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
	// TypeParams records a generic declaration (constraints/defaults
	// intact) so explicit type arguments can bind positionally.
	TypeParams []*GenericParam
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
	// TypeParams holds resolved generic declarations (constraints and
	// defaults intact) for explicit-argument checking on `new`.
	TypeParams []*GenericParam
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

// InferParam is an `infer U` placeholder. It only lives inside the extends
// clause of a (possibly deferred) conditional type; everywhere else the
// checker reports it and evaluation treats it permissively.
type InferParam struct {
	Name string
}

func (t *InferParam) Kind() Kind     { return KInferParam }
func (t *InferParam) String() string { return "infer " + t.Name }

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
func (t *Conditional) String() string {
	return t.Check.String() + " extends " + t.Extends.String() + " ? " + t.TrueBranch.String() + " : " + t.FalseBranch.String()
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
		return to.Kind() == KAny || to.Kind() == KUnknown || from.String() == to.String()
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
		tps := make([]*GenericParam, len(x.TypeParams))
		for i, gp := range x.TypeParams {
			tps[i] = substituteGenericParam(gp, sub, ctx)
		}
		return &Func{Params: ps, HasVarArg: x.HasVarArg, VarArg: va, Returns: rs, TypeParams: tps}
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
		na := make([]Type, len(x.Args))
		for i, a := range x.Args {
			na[i] = substitute(a, sub, ctx)
		}
		return &Ref{Name: x.Name, Args: na}
	case *InferParam:
		if s, ok := sub[x.Name]; ok {
			return s
		}
		return x
	case *Mapped:
		m2 := &Mapped{
			Param:      x.Param,
			Constraint: substitute(x.Constraint, sub, ctx),
			Optional:   x.Optional,
			Readonly:   x.Readonly,
			Value:      substitute(x.Value, sub, ctx),
		}
		if r, ok := evalMapped(m2, Options{StrictNilChecks: true}); ok {
			return r
		}
		return m2
	case *IndexedAccess:
		obj := substitute(x.Obj, sub, ctx)
		index := substitute(x.Index, sub, ctx)
		if r, ok, _ := evalIndexedAccess(obj, index, Options{StrictNilChecks: true}); ok {
			return r
		}
		return &IndexedAccess{Obj: obj, Index: index}
	case *Conditional:
		check := substitute(x.Check, sub, ctx)
		ext := substitute(x.Extends, sub, ctx)
		trueB := substitute(x.TrueBranch, sub, ctx)
		falseB := substitute(x.FalseBranch, sub, ctx)
		if IsConcreteType(check) {
			// Strict nil checks match the checker's default; documented.
			return EvalConditional(check, ext, trueB, falseB, x.Distributive, Options{StrictNilChecks: true})
		}
		return &Conditional{Check: check, Extends: ext, TrueBranch: trueB, FalseBranch: falseB, Distributive: x.Distributive}
	case *Enum:
		return x
	case *Literal:
		return x
	case *Primitive:
		return x
	}
	return t
}

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
		return hasTypeParams(x.Constraint, seen) || hasTypeParams(x.Value, seen)
	case *IndexedAccess:
		return hasTypeParams(x.Obj, seen) || hasTypeParams(x.Index, seen)
	}
	return false
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
			var rs []Type
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
	if matchInfer(check, extends, bindings, map[[2]string]bool{}, opt) {
		return Substitute(trueB, bindings)
	}
	return falseB
}

// matchInfer matches a concrete source against a pattern, collecting infer
// bindings. The seen map makes matching coinductive over recursive types.
func matchInfer(source, pattern Type, bindings map[string]Type, seen map[[2]string]bool, opt Options) bool {
	if source == nil || pattern == nil {
		return source == nil && pattern == nil
	}
	// `infer U` binds the whole source (joining conflicts as unions).
	if ip, ok := pattern.(*InferParam); ok {
		if prev, ok := bindings[ip.Name]; ok {
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
	key := [2]string{typeID(source), typeID(pattern)}
	if seen[key] {
		return true
	}
	seen[key] = true

	// Bare type parameters only match themselves here (concrete sources
	// never satisfy an unbound parameter).
	if _, ok := pattern.(*GenericParam); ok {
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

	return source.String() == pattern.String()
}

func copyBindings(b map[string]Type) map[string]Type {
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

func matchTupleElems(src, pat []Type, bindings map[string]Type, seen map[[2]string]bool, opt Options) bool {
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
func matchObjectFields(source Type, fields map[string]Field, bindings map[string]Type, seen map[[2]string]bool, opt Options) bool {
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
func matchClassPattern(source, pattern *Class, bindings map[string]Type, seen map[[2]string]bool, opt Options) bool {
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

// Mapped is `{ [K in Keys]?: V }`. Concrete domains expand eagerly
// (see evalMapped); anything still generic stays deferred.
type Mapped struct {
	Param      string
	Constraint Type
	Optional   bool
	Readonly   bool
	Value      Type
}

func (t *Mapped) Kind() Kind { return KMapped }
func (t *Mapped) String() string {
	q := ""
	if t.Optional {
		q = "?"
	}
	ro := ""
	if t.Readonly {
		ro = "readonly "
	}
	return "{ " + ro + "[" + t.Param + " in " + t.Constraint.String() + "]" + q + ": " + t.Value.String() + " }"
}

// IndexedAccess is `T[K]`, evaluated when both sides are concrete.
type IndexedAccess struct {
	Obj   Type
	Index Type
}

func (t *IndexedAccess) Kind() Kind { return KIndexedAccess }
func (t *IndexedAccess) String() string {
	return t.Obj.String() + "[" + t.Index.String() + "]"
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
	for _, k := range lits {
		name, _ := literalKeyName(k)
		fields[name] = Field{
			Type:     Substitute(m.Value, map[string]Type{m.Param: k}),
			Optional: m.Optional,
			Readonly: m.Readonly,
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
