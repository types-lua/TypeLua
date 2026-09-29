package emitter

import (
	"strings"
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

func TestEmitDefer(t *testing.T) {
	out := emit(t, `local function f(x: number): number
  defer print("a", x)
  defer print("b")
  if x < 0 then
    return -1
  end
  return x
end`)
	want := "local function f(x)\n" +
		"    local __tl_defer_1 = {}\n" +
		"    local function __tl_rundefer_1(...) for __i = #__tl_defer_1, 1, -1 do if not __tl_defer_1[__i].err then __tl_defer_1[__i].fn() end end return ... end\n" +
		"    __tl_defer_1[#__tl_defer_1 + 1] = { fn = function() print(\"a\", x) end }\n" +
		"    __tl_defer_1[#__tl_defer_1 + 1] = { fn = function() print(\"b\") end }\n" +
		"    if x < 0 then\n" +
		"        return __tl_rundefer_1(-1)\n" +
		"    end\n" +
		"    return __tl_rundefer_1(x)\n" +
		"end\n"
	if out != want {
		t.Fatalf("got:\n%s\nwant:\n%s", out, want)
	}
}

func TestEmitErrDefer(t *testing.T) {
	// Standalone emission (no checker run): the error call passes
	// through unmarked, so errdefers only register. Valid output.
	out := emit(t, `local function g(): number
  local h = 1
  errdefer print("failed")
  if h > 0 then
    error("boom")
  end
  return h
end`)
	want := "local function g()\n" +
		"    local __tl_defer_1 = {}\n" +
		"    local function __tl_runerr_1() for __i = #__tl_defer_1, 1, -1 do __tl_defer_1[__i].fn() end end\n" +
		"    local h = 1\n" +
		"    __tl_defer_1[#__tl_defer_1 + 1] = { fn = function() print(\"failed\") end, err = true }\n" +
		"    if h > 0 then\n" +
		"        error(\"boom\")\n" +
		"    end\n" +
		"    return h\n" +
		"end\n"
	if out != want {
		t.Fatalf("got:\n%s\nwant:\n%s", out, want)
	}
}

func TestEmitNoDeferUnchanged(t *testing.T) {
	// Functions without defer emit byte-identical output to before.
	out := emit(t, `local function f(x: number): number
  if x < 0 then
    return -1
  end
  return x
end`)
	want := "local function f(x)\n    if x < 0 then\n        return -1\n    end\n    return x\nend\n"
	if out != want {
		t.Fatalf("got:\n%s\nwant:\n%s", out, want)
	}
}

func TestEmitDeferMethod(t *testing.T) {
	out := emit(t, `class C {
  x: number,
  constructor(x: number) self.x = x end,
  get(): number
    defer print("get")
    return self.x
  end,
}`)
	if !strings.Contains(out, "local __tl_defer_1 = {}") {
		t.Fatalf("method defer prelude missing:\n%s", out)
	}
	if !strings.Contains(out, "return __tl_rundefer_1(self.x)") {
		t.Fatalf("method return not wrapped:\n%s", out)
	}
}

func TestEmitDeferTopLevel(t *testing.T) {
	out := emit(t, "defer print(\"bye\")\nprint(\"hi\")\n")
	if !strings.Contains(out, "__tl_defer_1[#__tl_defer_1 + 1]") {
		t.Fatalf("top-level defer not registered:\n%s", out)
	}
	if !strings.Contains(out, "__tl_rundefer_1()\n") {
		t.Fatalf("top-level trailing run missing:\n%s", out)
	}
}

func TestEmitDeferGlobalAndCtor(t *testing.T) {
	out := emit(t, `function g(): number
  defer print("g")
  return 1
end
class C {
  constructor() defer print("ctor") end,
}`)
	if !strings.Contains(out, "return __tl_rundefer_1(1)") {
		t.Fatalf("global fn return not wrapped:\n%s", out)
	}
	if !strings.Contains(out, "__tl_defer_2[#__tl_defer_2 + 1]") {
		t.Fatalf("ctor defer not registered:\n%s", out)
	}
}

func TestAnonymousCallParenthesized(t *testing.T) {
	// Lua requires parentheses around a function literal before a call:
	// `function() end()` is a syntax error.
	out := emit(t, `local f = function() return 1 end()`)
	if out != "local f = (function() return 1 end)()\n" {
		t.Fatalf("got %q", out)
	}
	out = emit(t, `local f = function(x: number) return x end(1)`)
	if !strings.Contains(out, "(function(x) return x end)(1)") {
		t.Fatalf("got %q", out)
	}
}

func TestEmitBraceFuncBody(t *testing.T) {
	// Brace bodies emit the canonical `end` form, byte-identical to it.
	pairs := [][2]string{
		{
			`local function f(x: number): number { return x + 1 }`,
			"local function f(x)\n    return x + 1\nend\n",
		},
		{
			`local cb = function(a: number): number { return a * 2 }`,
			"local cb = function(a) return a * 2 end\n",
		},
		{
			"local function e() {}",
			"local function e()\nend\n",
		},
	}
	for i, pr := range pairs {
		if got := emit(t, pr[0]); got != pr[1] {
			t.Fatalf("case %d: got %q, want %q", i, got, pr[1])
		}
	}
	// Class brace methods match their `end` twins byte for byte.
	a := emit(t, "class C {\n  m(): number { return 1 },\n}\n")
	b := emit(t, "class C {\n  m(): number return 1 end,\n}\n")
	if a != b {
		t.Fatalf("class brace/end differ:\n%s\n---\n%s", a, b)
	}
}
