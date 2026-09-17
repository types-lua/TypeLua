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
