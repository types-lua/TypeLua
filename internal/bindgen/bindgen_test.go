package bindgen

import (
	"strings"
	"testing"

	tlparser "github.com/typelua/typelua/internal/parser"
)

func mustParseT(t *testing.T, src string) {
	t.Helper()
	if _, diags := tlparser.ParseFile("gen.d.tl", src); len(diags) > 0 {
		t.Fatalf("generated output must parse:\n%s\n---\n%s", src, diags[0].Message)
	}
}

func TestGenerateBasic(t *testing.T) {
	out := Generate(`
typedef struct { double x, y; } Point;
typedef enum { Red = 0, Green = 1 } Color;
int add(int a, int b);
double dist(Point a, Point b);
const char* greet(const char* name);
void* alloc(unsigned long n);
#define MAX_N 128
`, "MyLib")
	mustParseT(t, out)
	for _, want := range []string{
		"type Point = {", "x: number", "enum Color", "Red = 0",
		"interface MyLib", "add(a: integer, b: integer): integer",
		"dist(a: Point, b: Point): number", "greet(name: string): string",
		"alloc(n: integer): cdata", "MAX_N: 128",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in:\n%s", want, out)
		}
	}
}

func TestUnsignedLongRegression(t *testing.T) {
	// `unsigned` must not be corrupted via inner `signed` substring replace.
	out := Generate(`void* alloc(unsigned long n);`, "L")
	if !strings.Contains(out, "alloc(n: integer): cdata") {
		t.Fatalf("unsigned long must map to integer, got:\n%s", out)
	}
	out = Generate(`int f(unsigned x, signed y);`, "L")
	if !strings.Contains(out, "f(") || !strings.Contains(out, "integer") {
		t.Fatalf("unsigned/signed scalars must map to integer:\n%s", out)
	}
	mustParseT(t, out)
}

func TestGenerateSkipsGracefully(t *testing.T) {
	out := Generate(`
struct Forward;
int printf(const char* fmt, ...);
typedef int (*cb_t)(int a, void* ud);
int register_cb(cb_t cb, void* ud);
int weird(unknown_type q);
`, "L")
	mustParseT(t, out)
	for _, want := range []string{
		"type Forward = cdata", "...args: any[]", "type cb_t = (",
		"register_cb(", "weird(",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in:\n%s", want, out)
		}
	}
}

func TestGenerateEmpty(t *testing.T) {
	out := Generate(`// nothing here`, "L")
	mustParseT(t, out)
	if !strings.Contains(out, "no functions") {
		t.Fatalf("empty header should note no functions:\n%s", out)
	}
}
