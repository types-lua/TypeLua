package types

import (
	"strings"
	"testing"
)

func TestAbbrev(t *testing.T) {
	// Shallow messages pass through byte-identical.
	for _, s := range []string{
		"",
		"type 'number' is not assignable to type 'string'",
		`Point{ x: number, y?: string }`,
		`{ a: { b: "x{y" } }`,
		"f((x: number, ...: string) -> (number, string))",
	} {
		if got := Abbrev(s); got != s {
			t.Fatalf("shallow changed: %q -> %q", s, got)
		}
	}
	// Deep nesting collapses but stays balanced.
	deep := strings.Repeat("{ x: ", 30) + "number" + strings.Repeat(" }", 30)
	got := Abbrev(deep)
	if strings.Count(got, "{") != strings.Count(got, "}") {
		t.Fatalf("unbalanced: %q", got)
	}
	if !strings.Contains(got, "...") || len(got) >= len(deep) {
		t.Fatalf("not truncated: %q", got)
	}
	// Brackets inside strings do not count.
	s := `{"a{b": number}`
	if Abbrev(s) != s {
		t.Fatalf("string brackets miscounted: %q", Abbrev(s))
	}
	// Message-style single quoting must not hide nesting.
	msg := "type '" + strings.Repeat("{ x: ", 30) + "number" + strings.Repeat(" }", 30) + "' is not assignable"
	got = Abbrev(msg)
	if !strings.Contains(got, "...") || !strings.HasSuffix(got, "is not assignable") {
		t.Fatalf("message not truncated: %q", got)
	}
	// Sibling content after a cut survives.
	s2 := `{ a: { b: { c: { d: { e: { f: { g: { h: { i: 1 } } } } } } } }, z: 2 }`
	g2 := Abbrev(s2)
	if !strings.Contains(g2, "z: 2") || strings.Count(g2, "{") != strings.Count(g2, "}") {
		t.Fatalf("siblings lost or unbalanced: %q", g2)
	}
	// Length backstop.
	long := "[" + strings.Repeat("number, ", 600) + "number]"
	if got := Abbrev(long); len(got) > 1600 || !strings.HasSuffix(got, "...") {
		t.Fatalf("length cap missed: %d", len(got))
	}
}
