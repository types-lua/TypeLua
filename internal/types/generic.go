package types

import (
	"strings"
)

type GenericParam struct {
	Name       string
	Constraint Type
	Default    Type
}

func (t *GenericParam) Kind() Kind { return KGenericParam }
func (t *GenericParam) String() string { return t.stringSeen(map[Type]bool{}) }
func (t *GenericParam) stringSeen(seen map[Type]bool) string {
	if t.Constraint != nil {
		return t.Name + " extends " + typeString(t.Constraint, seen)
	}
	return t.Name
}

// Ref is an unresolved named type with optional type arguments.
type Ref struct {
	Name string
	Args []Type
}

func (t *Ref) Kind() Kind { return KRef }
func (t *Ref) String() string { return t.stringSeen(map[Type]bool{}) }
func (t *Ref) stringSeen(seen map[Type]bool) string {
	if len(t.Args) == 0 {
		return t.Name
	}
	ps := make([]string, len(t.Args))
	for i, a := range t.Args {
		ps[i] = typeString(a, seen)
	}
	return t.Name + "<" + strings.Join(ps, ", ") + ">"
}

// InferParam is an `infer U` placeholder. It only lives inside the extends
// clause of a (possibly deferred) conditional type; everywhere else the
// checker reports it and evaluation treats it permissively.
type InferParam struct {
	Name string
}

func (t *InferParam) Kind() Kind     { return KInferParam }
func (t *InferParam) String() string { return "infer " + t.Name }
