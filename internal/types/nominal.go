package types

import (
	"strings"
)

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
func (t *Class) String() string { return t.stringSeen(map[Type]bool{}) }
func (t *Class) stringSeen(seen map[Type]bool) string {
	if len(t.Args) == 0 {
		return t.Name
	}
	ps := make([]string, len(t.Args))
	for i, a := range t.Args {
		ps[i] = typeString(a, seen)
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
