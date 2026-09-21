package checker

import (
	"testing"

	"github.com/typelua/typelua/internal/ast"
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

func TestNarrowingMembers(t *testing.T) {
	// The motivating case: no need to copy `node.next` into a local first.
	expectOK(t, `
local function get(n: { next?: { v: number } }): number
  if n.next == nil then
    return -1
  end
  return n.next.v
end
`)
	expectOK(t, `
local function get(n: { next?: { v: number } }): number
  if n.next ~= nil then
    return n.next.v
  end
  return -1
end
`)
	// String-index form narrows the same member.
	expectOK(t, `
local function get(n: { next?: { v: number } }): number
  if n["next"] ~= nil then
    return n.next.v
  end
  return -1
end
`)
	// `type()` comparisons narrow members too.
	expectOK(t, `
local function f(t: { v: number | string }): string
  if type(t.v) == "string" then
    return t.v
  else
    return "n"
  end
end
`)
	// Nested paths narrow through each level.
	expectOK(t, `
local function get(n: { inner: { next?: { v: number } } }): number
  if n.inner.next ~= nil then
    return n.inner.next.v
  end
  return -1
end
`)
	// A nil-narrowed member rejects field access.
	expectErr(t, `
local function get(n: { next?: { v: number } }): number
  if n.next == nil then
    return n.next.v
  end
  return -1
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

func TestExplicitTypeArgsOK(t *testing.T) {
	expectOK(t, `
local function id<T>(x: T): T return x end
local a = id<string>("hi")
local b = id(1)
`)
	expectOK(t, `
local function withDef<T, U = number>(a: T): U return nil as any end
local w: number = withDef<string>("hi")
`)
	expectOK(t, `
class Box<T> {
  v: T,
  constructor(v: T) self.v = v end,
}
local b = Box.new<string>("hi")
local c = Box<string>("hi")
`)
	expectOK(t, `
local x = 1
local y = x < 2
`)
}

func TestExplicitTypeArgsErr(t *testing.T) {
	// Too many.
	expectErr(t, `
local function id<T>(x: T): T return x end
local b = id<number, string>(1)`)
	// Non-generic callee.
	expectErr(t, `local c = print<number>("hi")`)
	// Constraint violation.
	expectErr(t, `
local function f<T extends number>(x: T): T return x end
local d = f<string>("hi")`)
	// Argument mismatch under explicit binding.
	expectErr(t, `
local function id<T>(x: T): T return x end
local a: number = id<string>("hi")`)
}

func TestMappedIndexedOK(t *testing.T) {
	expectOK(t, `
type Point = { x: number, y: number }
type PP = { [K in keyof Point]?: Point[K] }
local p: PP = { x = 1 }
local x: number | nil = p.x
`)
	expectOK(t, `
type Pick<T, K> = { [P in K]: T[P] }
type Point = { x: number, y: number }
local pk: Pick<Point, "x"> = { x = 1 }
`)
	expectOK(t, `
type Tup = [string, number]
local t: Tup = {"a", 1}
local a: string = t[1]
local n: Tup[number] = 1
`)
	expectOK(t, `
type M = { [string]: number }
local m: M = {}
local v: number = m["k"]
`)
}

func TestUnionMemberAccess(t *testing.T) {
	expectOK(t, `
type A = { x: number, tag: string }
type B = { x: number, tag: string, y?: boolean }
local function f(v: A | B): number
  return v.x
end
local function g(v: A | B): string
  return v.tag
end
`)
	// Optional folds in from any member (both sides declare it).
	expectOK(t, `
type A = { x: number, y: boolean }
type B = { x: number, y?: boolean }
local function f(v: A | B): boolean | nil
  return v.y
end
`)
	// Index distribution over unions.
	expectOK(t, `
type Tup = [string, number]
local function h(t: Tup | string[]): string | number
  return t[1]
end
`)
	// A member missing on one side fails the whole access.
	expectErr(t, `
type A = { x: number }
type C = { z: number }
local function f(v: A | C): number
  return v.x
end
`)
	// nil members fail under strict nil checks (narrow first).
	expectErr(t, `
type A = { x: number }
local function f(v: A | nil): number
  return v.x
end
`)
	expectOK(t, `
type A = { x: number }
local function f(v: A | nil): number
  if v == nil then return 0 end
  return v.x
end
`)
	// Methods distribute too.
	expectOK(t, `
interface S { name: string }
interface T { name: string, id: integer }
local function g(s: S | T): string
  return s.name
end
`)
}

func TestNarrowingTruthy(t *testing.T) {
	// `if x then` removes nil.
	expectOK(t, `
local function f(x: string | nil): string
  if x then return x end
  return ""
end
`)
	expectOK(t, `
local function f(x: string | nil): string
  if not x then return "d" end
  return x
end
`)
	// Booleans narrow to literals.
	expectOK(t, `
local function f(b: boolean): boolean
  if b then return b end
  return false
end
`)
	// Member truthiness needs no nil comparison.
	expectOK(t, `
local function get(n: { next?: { v: number } }): number
  if n.next then
    return n.next.v
  end
  return -1
end
`)
	// `and` refinements of the same root compose.
	expectOK(t, `
local function f(a: { x?: number, y?: string }): number
  if a.x ~= nil and a.y ~= nil then
    return a.x
  end
  return 0
end
`)
	// Non-nullable values are untouched.
	expectOK(t, `
local function f(x: number): number
  if x then return x end
  return 0
end
`)
}

func TestRecursiveTypes(t *testing.T) {
	// Interface self-reference.
	expectOK(t, `
interface Node { v: number, next?: Node }
local n: Node = { v = 1, next = { v = 2 } }
local h: number = 0
if n.next ~= nil then
  h = n.next.v
end
`)
	// Generic alias recursion.
	expectOK(t, `
type List<T> = { head: T, tail: List<T> | nil }
local l: List<number> = { head = 1, tail = { head = 2, tail = nil } }
local function sum(l: List<number> | nil): number
  if l == nil then return 0 end
  return l.head
end
`)
	// Mutual recursion.
	expectOK(t, `
interface A { b?: B }
interface B { a?: A, tag: string }
local ab: A = {}
local bb: B = { tag = "x", a = {} }
local t: string = bb.tag
`)
	// Class-typed recursive fields.
	expectOK(t, `
class BoxC<T> {
  v: T,
  constructor(v: T) self.v = v end,
}
interface Node { v: number, box?: BoxC<Node> }
local n: Node = { v = 1 }
`)
	// Direct self-aliases still error.
	expectErr(t, `type A2 = A2`)
	expectErr(t, `type U = U | nil`)
	// Missing members through recursion still error.
	expectErr(t, `
interface Node { v: number, next?: Node }
local n: Node = { v = 1 }
local s: string = n.nope
`)
	// Self-feeding expansions terminate with an error, not a hang.
	expectErr(t, `
type Wrap<T> = { w: T }
type L<T> = { x: L<Wrap<T>> }
local v: L<number> = nil as any
`)
}

func TestNilTolerance(t *testing.T) {
	// Hand-built broken ASTs (as error recovery may produce) must not crash.
	broken := []*ast.File{
		{Name: "t.tl", Block: &ast.Block{Stmts: []ast.Stmt{
			&ast.ExprStmt{E: &ast.CallExpr{Func: &ast.Ident{Name: "f"}, Args: []ast.Expr{nil}}},
		}}},
		{Name: "t.tl", Block: &ast.Block{Stmts: []ast.Stmt{
			&ast.LocalAssign{Names: []*ast.Ident{{Name: "x"}}, Types: []ast.TypeNode{nil}},
		}}},
		{Name: "t.tl", Block: &ast.Block{Stmts: []ast.Stmt{nil}}},
	}
	for i, f := range broken {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("case %d panicked: %v", i, r)
				}
			}()
			_ = CheckFile(f, DefaultOptions())
		}()
	}
}

func TestMappedModifiers(t *testing.T) {
	// Homomorphic: unmarked mapping over keyof inherits base modifiers.
	expectOK(t, `
type Base = { x: number, y?: string, readonly z: boolean }
type H = { [K in keyof Base]: Base[K] }
local h: H = { x = 1, z = true }
local ys: string | nil = h.y
`)
	expectErr(t, `
type Base = { x: number, y?: string, readonly z: boolean }
type H = { [K in keyof Base]: Base[K] }
local h: H = { x = 1, z = true }
h.z = false`)
	// `-?` removes optionality (Required-like).
	expectErr(t, `
type Base = { x: number, y?: string }
type Req = { [K in keyof Base]-?: Base[K] }
local r: Req = { x = 1 }`)
	expectOK(t, `
type Base = { x: number, y?: string }
type Req = { [K in keyof Base]-?: Base[K] }
local r: Req = { x = 1, y = "a" }`)
	// `-readonly` removes readonly.
	expectOK(t, `
type Base = { readonly x: number }
type Mut = { -readonly [K in keyof Base]: Base[K] }
local m: Mut = { x = 1 }
m.x = 2`)
	// `+?` / `+readonly` behave like the bare markers.
	expectOK(t, `
type Base = { x: number }
type P = { [K in keyof Base]+?: Base[K] }
local p: P = {}
`)
	expectErr(t, `
type Base = { x: number }
type R = { +readonly [K in keyof Base]: Base[K] }
local r: R = { x = 1 }
r.x = 2`)
}

func TestMappedIndexedErr(t *testing.T) {
	expectErr(t, `
type Point = { x: number, y: number }
local a: Point["z"] = 1`)
	expectErr(t, `type Bad = { [K in boolean]: string }`)
	expectErr(t, `
type RO<T> = { readonly [K in keyof T]: T[K] }
type Point = { x: number, y: number }
local ro: RO<Point> = { x = 1, y = 2 }
ro.x = 2`)
}

func TestImportStmt(t *testing.T) {
	// Annotated import binds the declared type.
	expectOK(t, `
type GreetApi = { hello(name: string): string }
import G: GreetApi from "example.com.greet.greet"
local s: string = G.hello("x")
`)
	// Unannotated import is any.
	expectOK(t, `
import H from "example.com.other"
local v = H.anything
`)
	// Side-effect form binds nothing.
	expectOK(t, `import "example.com.side"`)
	// Misuse still errors.
	expectErr(t, `
type GreetApi = { hello(name: string): string }
import G: GreetApi from "example.com.greet.greet"
local n: number = G
`)
	expectErr(t, `
import G from "example.com.greet"
import G from "example.com.other"
`)
}

func TestDeferCheck(t *testing.T) {
	// Deferred calls type-check like normal calls.
	expectOK(t, `
local function f(x: number): number
  defer print(x)
  return x
end
`)
	// Argument mismatches surface.
	expectErr(t, `
local function f(x: number): number
  defer print(x, y)
  return x
end
`)
	// Bare ... cannot cross into the deferred closure.
	expectErr(t, `
local function f(...: string)
  defer print(...)
end
`)
	// ... inside a nested function is that function's own business.
	expectOK(t, `
local function f(...: string)
  defer print(function(...) return ... end)
end
`)
}

func TestLongStringKeys(t *testing.T) {
	// Long-bracket keys behave like quoted ones everywhere.
	expectOK(t, `
type T = { name: string }
local t: T = { name = "x" }
local a = t[ [[name]] ]
local b: string = t[ [=[name]=] ]
`)
	expectOK(t, `
local function get(n: { next?: { v: number } }): number
  if n[ [[next]] ] ~= nil then
    return n.next.v
  end
  return -1
end
`)
	// Note: `n[[next]]` (no space) lexes as a string-argument call
	// `n([[next]])`, exactly like Lua; index needs the space.
	expectOK(t, `
local function f(x: any): string
  if type(x) == [[string]] then return x end
  return ""
end
`)
}

func TestRecursiveGenericGuard(t *testing.T) {
	// Self-referential constraints/defaults terminate with errors.
	expectErr(t, `
type F<T extends F<T>> = { v: T }
local a: F<number> = nil as any
`)
	expectErr(t, `
type L<T = L<T>> = { x: T }
local b: L<number> = nil as any
`)
	// Deep but finite generic nesting is unaffected by the depth cap.
	nested := "number"
	for i := 0; i < 100; i++ {
		nested = "Box<" + nested + ">"
	}
	expectOK(t, `
class Box<T> {
  v: T,
  constructor(v: T) self.v = v end,
}
local x: `+nested+` = nil as any
`)
}

func checkSrcOpt(src string, opt Options) []string {
	f, pdiags := parser.ParseFile("test.tl", src)
	if len(pdiags) != 0 {
		var out []string
		for _, d := range pdiags {
			out = append(out, "parse: "+d.String())
		}
		return out
	}
	var out []string
	for _, d := range CheckFile(f, opt) {
		out = append(out, d.String())
	}
	return out
}

func TestCoroutine(t *testing.T) {
	expectOK(t, `
local co = coroutine.create(function() print("hi") end)
local ok: boolean = coroutine.resume(co)
local ok2, v = coroutine.resume(co, 1, "x")
local st: string = coroutine.status(co)
local w = coroutine.wrap(function(a: number) return a end)
local t, main = coroutine.running()
local y: boolean = coroutine.isyieldable()
coroutine.close(co)
print(ok, ok2, v, st, w, t, main, y)
`)
	// create rejects non-functions; resume/status/close need threads.
	expectErr(t, `local co = coroutine.create(42)`)
	expectErr(t, `
local co = coroutine.create(function() end)
coroutine.resume(123)
`)
	expectErr(t, `
local co = coroutine.create(function() end)
coroutine.status("x")
`)
	// Status literals are precise.
	expectErr(t, `
local co = coroutine.create(function() end)
local n: number = coroutine.status(co)
`)
	expectOK(t, `
local co = coroutine.create(function() end)
local st = coroutine.status(co)
if st == "dead" then
  print("done")
else
  print("live")
end
`)
}

func TestCoroutine51(t *testing.T) {
	opt := Options{Strict: true, StrictNilChecks: true, Target: "5.1"}
	ok := `
local co = coroutine.create(function() end)
local ok, v = coroutine.resume(co)
local st = coroutine.status(co)
print(ok, v, st)
`
	if ds := checkSrcOpt(ok, opt); len(ds) != 0 {
		t.Fatalf("5.1 core should pass, got %v", ds)
	}
	bad := `
local co = coroutine.create(function() end)
coroutine.close(co)
`
	if ds := checkSrcOpt(bad, opt); len(ds) == 0 {
		t.Fatalf("close must fail on 5.1")
	}
	bad2 := `
local y = coroutine.isyieldable()
`
	if ds := checkSrcOpt(bad2, opt); len(ds) == 0 {
		t.Fatalf("isyieldable must fail on 5.1")
	}
}

func TestLiteralNarrowing(t *testing.T) {
	expectOK(t, `
local function f(s: string | nil): string
  if s == "a" then return s end
  return ""
end
`)
	expectOK(t, `
type Level = "low" | "high"
local function f(l: Level): string
  if l ~= "low" then return l end
  return "low"
end
`)
	expectOK(t, `
local function get(n: { mode?: string }): string
  if n.mode == "fast" then
    return n.mode
  end
  return ""
end
`)
	// Mismatched literal stays put (no unsound narrowing).
	expectOK(t, `
local x: number = 1
if x == "a" then print("impossible") end
print(x)
`)
}
