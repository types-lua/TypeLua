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

func TestEmitClass(t *testing.T) {
	out := emit(t, `
class Dog extends Animal {
  constructor(name: string) super(name) end,
  speak(): string return "woof" end,
}`)
	for _, want := range []string{
		"local Dog = setmetatable",
		"__index = Animal",
		"function Dog.new(...)",
		"getmetatable(Dog).__call",
		"function Dog:constructor(name)",
		"Animal.constructor(self, name)",
		"function Dog:speak()",
	} {
		if !contains(out, want) {
			t.Fatalf("emit missing %q:\n%s", want, out)
		}
	}
	// declare classes erase fully.
	out = emit(t, `declare class D { m(): string }`)
	if out != "" {
		t.Fatalf("declare should erase, got %q", out)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func TestEmitImport(t *testing.T) {
	out := emit(t, `import G: Api from "example.com.greet.greet"`)
	if out != "local G = require(\"example.com.greet.greet\")\n" {
		t.Fatalf("got %q", out)
	}
	out = emit(t, `import H from "example.com.other"`)
	if out != "local H = require(\"example.com.other\")\n" {
		t.Fatalf("got %q", out)
	}
	out = emit(t, `import "example.com.side"`)
	if out != "require(\"example.com.side\")\n" {
		t.Fatalf("got %q", out)
	}
}
