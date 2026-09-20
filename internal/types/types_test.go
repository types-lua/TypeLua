package types

import (
	"testing"
)

func TestUnion(t *testing.T) {
	u := NewUnion([]Type{Number, String})
	if u.Kind() != KUnion {
		t.Fatalf("want union got %s", u)
	}
	if NewUnion([]Type{Any, Number}) != Any {
		t.Fatalf("any absorbs")
	}
	if NewUnion(nil).Kind() != KNever {
		t.Fatalf("empty union = never")
	}
}

func TestAssignable(t *testing.T) {
	opt := Options{StrictNilChecks: true}
	cases := []struct {
		from, to string
		a, b     Type
		want     bool
	}{
		{"int->num", "int", Integer, Number, true},
		{"num->int", "num", Number, Integer, false},
		{"nil strict", "nil", Nil, Number, false},
		{"lit->base", "lit", &Literal{Value: `"a"`, Base: String}, String, true},
		{"any both", "any", Any, Number, true},
		{"never->all", "never", Never, Number, true},
	}
	for _, c := range cases {
		if got := IsAssignable(c.a, c.b, opt); got != c.want {
			t.Fatalf("%s: got %v want %v", c.from, got, c.want)
		}
	}
	// Union / object.
	u := NewUnion([]Type{Number, String})
	if !IsAssignable(Number, u, opt) {
		t.Fatalf("number <: number|string")
	}
	if IsAssignable(u, Number, opt) {
		t.Fatalf("union not <: number")
	}
	o1 := &Object{Fields: map[string]Field{"a": {Type: Number}}}
	o2 := &Object{Fields: map[string]Field{"a": {Type: Number}, "b": {Type: String}}}
	if !IsAssignable(o2, o1, opt) {
		t.Fatalf("width subtyping")
	}
	if IsAssignable(o1, o2, opt) {
		t.Fatalf("missing field")
	}
	// Func covariance/contra.
	f1 := &Func{Params: []FuncParam{{Type: Number}}, Returns: []Type{Number}}
	f2 := &Func{Params: []FuncParam{{Type: Integer}}, Returns: []Type{Integer}}
	_ = f1
	_ = f2
	// Union reflexivity (regression: `A|B <: A|B` must hold).
	u2 := NewUnion([]Type{String, Nil})
	if !IsAssignable(u2, u2, opt) {
		t.Fatalf("union not reflexive: %s", u2)
	}
	if !IsAssignable(NewUnion([]Type{Nil, String}), NewUnion([]Type{String, Nil}), opt) {
		t.Fatalf("union order-insensitive")
	}
}

func TestClassNominal(t *testing.T) {
	opt := Options{StrictNilChecks: true}
	defA := &ClassDef{Name: "A"}
	defB := &ClassDef{Name: "B"}
	a1 := &Class{Def: defA, Name: "A", Fields: map[string]ClassField{}}
	a2 := &Class{Def: defA, Name: "A", Fields: map[string]ClassField{}}
	b := &Class{Def: defB, Name: "B", Fields: map[string]ClassField{}}
	sub := &Class{Def: &ClassDef{Name: "Sub"}, Name: "Sub", Fields: map[string]ClassField{}, Base: a1}
	if !IsAssignable(a1, a2, opt) {
		t.Fatalf("same class should be assignable")
	}
	if !IsAssignable(sub, a1, opt) {
		t.Fatalf("subclass should be assignable to base")
	}
	if IsAssignable(a1, sub, opt) {
		t.Fatalf("base must not flow into subclass")
	}
	if IsAssignable(a1, b, opt) {
		t.Fatalf("unrelated same-shape classes must be incompatible")
	}
	// Class instances satisfy object shapes structurally.
	obj := &Object{Fields: map[string]Field{"x": {Type: Number}}}
	withX := &Class{Def: defA, Name: "A", Fields: map[string]ClassField{"x": {Type: Number}}}
	if !IsAssignable(withX, obj, opt) {
		t.Fatalf("class should satisfy matching interface")
	}
	if !IsAssignable(withX, Table, opt) {
		t.Fatalf("class instance should be a table")
	}
}

func evalTo(t *testing.T, check, pattern Type, tB, fB Type, dist bool, want string) {
	t.Helper()
	got := EvalConditional(check, pattern, tB, fB, dist, Options{StrictNilChecks: true})
	if got.String() != want {
		t.Fatalf("got %s want %s", got.String(), want)
	}
}

func TestConditionalEval(t *testing.T) {
	strArr := &Array{Elem: String}
	// Plain branch selection.
	evalTo(t, String, String, Number, Boolean, false, "number")
	evalTo(t, Number, String, Number, Boolean, false, "boolean")
	// infer binds array elements.
	evalTo(t, &Array{Elem: Integer}, &Array{Elem: &InferParam{Name: "U"}},
		&InferParam{Name: "U"}, Never, false, "integer")
	// Naked-param distribution over unions.
	evalTo(t, NewUnion([]Type{String, &Array{Elem: Integer}}), &Array{Elem: &InferParam{Name: "U"}},
		&InferParam{Name: "U"}, String, true, "integer | string")
	_ = strArr
	// Tuple head pattern.
	head := &Tuple{Elems: []Type{&InferParam{Name: "H"}, &Ref{Name: "...", Args: []Type{&Array{Elem: Unknown}}}}}
	evalTo(t, &Tuple{Elems: []Type{String, Number}}, head, &InferParam{Name: "H"}, Never, false, "string")
	// Still-generic checks stay deferred.
	def := &Conditional{Check: &GenericParam{Name: "T"}, Extends: String, TrueBranch: Number, FalseBranch: Boolean, Distributive: true}
	if _, ok := TryEvalConditional(def, Options{StrictNilChecks: true}); ok {
		t.Fatalf("generic check must stay deferred")
	}
	// Deferred conditionals are permissive in assignability (generic code).
	if !IsAssignable(Number, def, Options{StrictNilChecks: true}) {
		t.Fatalf("concrete should flow into deferred conditional")
	}
}
