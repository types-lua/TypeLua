package checker

import (
	"testing"

	"github.com/typelua/typelua/internal/parser"
)

func checkSrc(src string) []string {
	f, pdiags := parser.ParseFile("test.tl", src)
	if len(pdiags) != 0 {
		var out []string
		for _, d := range pdiags {
			out = append(out, "parse: "+d.String())
		}
		return out
	}
	diags := CheckFile(f, DefaultOptions())
	var out []string
	for _, d := range diags {
		out = append(out, d.String())
	}
	return out
}

func expectOK(t *testing.T, src string) {
	t.Helper()
	if ds := checkSrc(src); len(ds) != 0 {
		t.Fatalf("expected no errors, got %v\nsrc:\n%s", ds, src)
	}
}

func expectErr(t *testing.T, src string) {
	t.Helper()
	if ds := checkSrc(src); len(ds) == 0 {
		t.Fatalf("expected errors, got none\nsrc:\n%s", src)
	}
}

func TestBasicOK(t *testing.T) {
	expectOK(t, `local x: number = 1`)
	expectOK(t, `local s: string = "hi"`)
	expectOK(t, `local function foo(a: number): number return a end`)
	expectOK(t, `
type Point = { x: number, y: number }
local p: Point = { x = 1, y = 2 }
`)
	expectOK(t, `
interface Greet { greet(msg: string): string }
local g: Greet = { greet = function(m: string) return m end }
`)
	expectOK(t, `
enum Color { Red = "red", Blue = "blue" }
local c: Color = Color.Red
`)
	expectOK(t, `
local function id<T>(x: T): T return x end
local a = id(1)
local b: string = id("hi")
`)
	expectOK(t, `
local x: number | nil = nil
if x ~= nil then local y: number = x end
`)
	expectOK(t, `
local x: any = 1
local y: number = x
`)
}

func TestBasicErr(t *testing.T) {
	expectErr(t, `local x: number = "hi"`)
	expectErr(t, `local x: string = 1`)
	expectErr(t, `
local function foo(a: number): number return "hi" end
`)
	expectErr(t, `
type Point = { x: number, y: number }
local p: Point = { x = 1 }
`)
	expectErr(t, `local x: number = nil`)
	expectErr(t, `undefined_var + 1`)
	expectErr(t, `
local t: { a: number } = {}
local y: number = t.b
`)
}

func TestNarrowing(t *testing.T) {
	expectOK(t, `
local function f(x: number | nil): number
  if x == nil then return 0 end
  return x
end
`)
	expectOK(t, `
local function g(x: any): string
  if type(x) == "string" then return x end
  return ""
end
`)
}

func TestMultiReturn(t *testing.T) {
	expectOK(t, `
local function divmod(a: number, b: number): (number, number) return a, b end
local q, r = divmod(7, 3)
local qq: number = q
`)
	expectErr(t, `
local function f(): (number, string) return 1, "a" end
local x: string, y: number = f()
`)
}

func TestRegressionExamples(t *testing.T) {
	// Union self-assignment (was failing due to target-union fast path).
	expectOK(t, `
local a: string | nil = nil
local b: string | nil = a
`)
	// Optional field read keeps the same union.
	expectOK(t, `
interface U { nickname?: string }
local u: U = {}
local n: string | nil = u.nickname
`)
	// Generic array param accepts list-literal (Tuple) argument.
	expectOK(t, `
local function first<T>(arr: T[]): T | nil return arr[1] end
local x: number | nil = first({ 1, 2, 3 })
`)
	// Constrained generic arithmetic.
	expectOK(t, `
local function add<T extends number>(a: T, b: T): number return a + b end
`)
	// Builtins may be localized (shadowing is idiomatic Lua).
	expectOK(t, `local next = 1`)
	expectOK(t, `local type = type`)
	// Colon-style higher-order return: `() -> number`.
	expectOK(t, `
local function counter(): () -> number
    return function(): number return 1 end
end
`)
	// Tuple literal fits tuple annotation positionally.
	expectOK(t, `local t: [string, number] = { "a", 1 }`)
	// Map dot access.
	expectOK(t, `
local m: map<string, number> = { score = 1 }
local v: number = m.score
`)
}

func TestClassOK(t *testing.T) {
	expectOK(t, `
class Point {
  x: number = 0,
  constructor(x: number) self.x = x end,
  move(dx: number): void self.x = dx end,
}
local p = Point.new(1)
p:move(2)
local q = Point(3)
print(q.x)
`)
	expectOK(t, `
abstract class A {
  constructor() end,
  abstract m(): string,
}
class B extends A {
  m(): string return "b" end,
}
local b: A = B.new()
`)
	expectOK(t, `
class Box<T> {
  v: T,
  constructor(v: T) self.v = v end,
  get(): T return self.v end,
}
local n: number = Box.new(1):get()
`)
	expectOK(t, `
interface N { name: string }
class C implements N {
  name: string = "c",
}
`)
	expectOK(t, `
class Base {
  protected x: integer = 1,
  get(): integer return self.x end,
}
class Sub extends Base {
  peek(): integer return self.x end,
}`)
}

func TestClassErr(t *testing.T) {
	// Abstract cannot be instantiated.
	expectErr(t, `abstract class A {} local a = A.new()`)
	// Missing abstract implementation.
	expectErr(t, `
abstract class A { abstract m(): void }
class B extends A {}`)
	// Private member access outside.
	expectErr(t, `
class S { private x: number = 1 }
local s = S.new()
print(s.x)`)
	// Missing super() with nontrivial base.
	expectErr(t, `
class B { constructor(x: number) self.y = x end, y: number = 0 }
class D extends B { constructor(x: number) self.y = x end }`)
	// Unassigned required field.
	expectErr(t, `class P { x: number }`)
	// Incompatible override.
	expectErr(t, `
class B { m(): string return "s" end }
class D extends B { m(): number return 1 end }`)
	// Unrelated classes are nominally incompatible.
	expectErr(t, `
class A { x: number = 1 }
class B { x: number = 2 }
local b = B.new()
local a: A = b`)
	// Private constructor blocks outside instantiation.
	expectErr(t, `
class S { private constructor() end }
local s = S.new()`)
}

func TestConditionalOK(t *testing.T) {
	expectOK(t, `
type El<T> = T extends (infer U)[] ? U : T
local a: El<number[]> = 1
local b: El<string> = "s"
local c: El<string | number[]> = 1
`)
	expectOK(t, `
type Head<T> = T extends [infer H, ...unknown[]] ? H : never
local h: Head<[string, number]> = "a"
`)
	expectOK(t, `
type Chain<T> = T extends string ? (T extends "a" ? 1 : 2) : 3
local x: Chain<"a"> = 1
local y: Chain<number> = 3
`)
	expectOK(t, `
class Box<T> {
  v: T,
  constructor(v: T) self.v = v end,
}
type Unbox<T> = T extends Box<infer U> ? U : never
local u: Unbox<Box<number>> = 1
`)
}

func TestConditionalErr(t *testing.T) {
	// Wrong branch values.
	expectErr(t, `
type El<T> = T extends (infer U)[] ? U : T
local a: El<number[]> = "s"`)
	// infer outside an extends clause.
	expectErr(t, `local x: infer U = 1`)
	expectErr(t, `type F<T> = T extends string ? string : infer U`)
	// Boxed vs unboxed mismatch through a class pattern.
	expectErr(t, `
class Box<T> {
  v: T,
  constructor(v: T) self.v = v end,
}
type Unbox<T> = T extends Box<infer U> ? U : never
local u: Unbox<Box<number>> = "s"`)
}
