package emitter

import (
	"testing"

	"github.com/typelua/typelua/internal/parser"
)

func emit(t *testing.T, src string) string {
	t.Helper()
	f, diags := parser.ParseFile("test.tl", src)
	if len(diags) != 0 {
		t.Fatalf("parse diags: %v", diags)
	}
	return EmitFile(f, DefaultOptions())
}

func TestEraseTypes(t *testing.T) {
	out := emit(t, `local x: number = 1`)
	if out != "local x = 1\n" {
		t.Fatalf("got %q", out)
	}
	out = emit(t, `local function foo(a: number, b: string): boolean return true end`)
	if out != "local function foo(a, b)\n    return true\nend\n" {
		t.Fatalf("got %q", out)
	}
	out = emit(t, `local v = x as number`)
	if out != "local v = x\n" {
		t.Fatalf("got %q", out)
	}
	out = emit(t, `type P = { x: number }
local p: P = { x = 1 }`)
	if out != "local p = {x = 1}\n" {
		t.Fatalf("got %q", out)
	}
}

func TestEnum(t *testing.T) {
	out := emit(t, `enum Color { Red = "red", Blue }`)
	t.Logf("enum: %q", out)
	if len(out) == 0 {
		t.Fatalf("empty")
	}
}

func TestNeAlias(t *testing.T) {
	out := emit(t, `if a ~= b then print(1) end`)
	if out == "" {
		t.Fatalf("empty")
	}
}
