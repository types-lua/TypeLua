package parser

import (
	"testing"

	"github.com/typelua/typelua/internal/ast"
)

func mustParse(t *testing.T, src string) *ast.File {
	t.Helper()
	f, diags := ParseFile("test.tl", src)
	for _, d := range diags {
		t.Logf("diag: %v", d)
	}
	if len(diags) != 0 {
		t.Fatalf("expected no diags, got %d", len(diags))
	}
	return f
}

func TestParseLocalWithType(t *testing.T) {
	f := mustParse(t, `local x: number = 1`)
	if len(f.Block.Stmts) != 1 {
		t.Fatalf("stmts=%d", len(f.Block.Stmts))
	}
	la, ok := f.Block.Stmts[0].(*ast.LocalAssign)
	if !ok {
		t.Fatalf("not localassign %T", f.Block.Stmts[0])
	}
	if la.Types[0] == nil {
		t.Fatalf("missing type")
	}
}

func TestParseFunction(t *testing.T) {
	mustParse(t, `local function foo(a: number, b: string): boolean return true end`)
	mustParse(t, `function M.foo<T>(a: T): T return a end`)
	mustParse(t, `local f: (x: number) -> string = function(x) return "" end`)
}

func TestParseTypes(t *testing.T) {
	mustParse(t, `local x: number | string | nil = nil`)
	mustParse(t, `local x: number[] = {}`)
	mustParse(t, `local x: map<string, number> = {}`)
	mustParse(t, `local x: [string, number] = {"a", 1}`)
	mustParse(t, `type Point = { x: number, y: number }`)
	mustParse(t, `interface P { name: string, age?: integer, greet(msg: string): string }`)
	mustParse(t, `enum Color { Red = "red", Blue = "blue" }`)
	mustParse(t, `local f: (x: number, ...rest: string[]) -> boolean = nil`)
}

func TestParseLuaComplete(t *testing.T) {
	src := `
local x <const> = 1
local y <close> = nil
for i = 1, 10, 2 do print(i) end
for k, v in pairs({}) do print(k) end
while true do break end
repeat x = x + 1 until x > 10
if x == 1 then print(1) elseif x == 2 then print(2) else print(3) end
goto done
::done::
local t = { a = 1, [1] = 2, 3 }
local s = a.b:c(1, 2)
local u = a as number
local v = b!
`
	mustParse(t, src)
}

func TestParseErrorRecovery(t *testing.T) {
	f, diags := ParseFile("test.tl", "local x: = \nlocal y: number = 1")
	if len(diags) == 0 {
		t.Fatalf("expected errors")
	}
	if len(f.Block.Stmts) == 0 {
		t.Fatalf("expected recovery")
	}
}

func TestParseRegression(t *testing.T) {
	// Zero-arg method calls must parse (empty parens are valid args).
	mustParse(t, `fh:close()`)
	mustParse(t, `print(Counter:inc())`)
	// Colon-style higher-order return.
	mustParse(t, `local function counter(): () -> number return function(): number return 1 end end`)
	// Builtin names as values.
	mustParse(t, `local t = type(x)`)
	mustParse(t, `local typeAlias = 1`)
}

func TestParseClass(t *testing.T) {
	f := mustParse(t, `
class Point {
  x: number = 0,
  constructor(x: number) self.x = x end,
  move(dx: number): void self.x = dx end,
}`)
	decl, ok := f.Block.Stmts[0].(*ast.ClassDecl)
	if !ok {
		t.Fatalf("not classdecl %T", f.Block.Stmts[0])
	}
	if decl.Name.Name != "Point" || len(decl.Members) != 3 {
		t.Fatalf("bad class %+v", decl)
	}
	mustParse(t, `abstract class A extends B implements I { abstract m(): void }`)
	mustParse(t, `class C<T> extends B<T> { v: T, static make(v: T): C<T> return C.new(v) end }`)
	mustParse(t, `local class L { private x: number = 1 }`)
	mustParse(t, `export class E { protected static n: integer = 0 }`)
	mustParse(t, `declare class D { title: string, render(): string }`)
	// Errors recover: missing end / bad member.
	if _, diags := ParseFile("t.tl", `class Broken { x: number = }`); len(diags) == 0 {
		t.Fatalf("expected errors")
	}
}

func TestParseConditional(t *testing.T) {
	f := mustParse(t, `type X<T> = T extends string ? true : false`)
	alias, ok := f.Block.Stmts[0].(*ast.TypeAliasDecl)
	if !ok {
		t.Fatalf("not alias %T", f.Block.Stmts[0])
	}
	if _, ok := alias.Type.(*ast.ConditionalType); !ok {
		t.Fatalf("not conditional %T", alias.Type)
	}
	// `?` after a type stays Optional when no `: true-branch` follows.
	mustParse(t, `local x: number? = nil`)
	mustParse(t, `local y: string | nil = nil`)
	// infer, nesting, chaining, tuples with rest.
	mustParse(t, `type E<T> = T extends (infer U)[] ? U : T`)
	mustParse(t, `type H<T> = T extends [infer H, ...unknown[]] ? H : never`)
	mustParse(t, `type C<T> = T extends string ? (T extends "a" ? 1 : 2) : 3`)
	mustParse(t, `type P<T> = T extends string ? 1 : T extends number ? 2 : 3`)
	mustParse(t, `type R<T> = T extends (...args: any[]) -> infer R ? R : never`)
}

func TestParseExplicitTypeArgs(t *testing.T) {
	f := mustParse(t, `local a = id<string>("hi")`)
	lc, ok := f.Block.Stmts[0].(*ast.LocalAssign)
	if !ok || len(lc.Values) != 1 {
		t.Fatalf("bad localassign %T", f.Block.Stmts[0])
	}
	call, ok := lc.Values[0].(*ast.CallExpr)
	if !ok || len(call.TypeArgs) != 1 {
		t.Fatalf("expected 1 type arg, got %+v", lc.Values[0])
	}
	mustParse(t, `local b = obj:map<string>(1)`)
	// Comparisons still parse as comparisons.
	f2 := mustParse(t, `local c = a < b`)
	lc2 := f2.Block.Stmts[0].(*ast.LocalAssign)
	if _, ok := lc2.Values[0].(*ast.BinOpExpr); !ok {
		t.Fatalf("expected binop, got %T", lc2.Values[0])
	}
	mustParse(t, `local d = Box.new<string>("hi")`)
}

func TestParseMappedAndIndexed(t *testing.T) {
	f := mustParse(t, `type P<T> = { [K in keyof T]?: T[K] }`)
	alias, ok := f.Block.Stmts[0].(*ast.TypeAliasDecl)
	if !ok {
		t.Fatalf("not alias %T", f.Block.Stmts[0])
	}
	if _, ok := alias.Type.(*ast.MappedType); !ok {
		t.Fatalf("not mapped %T", alias.Type)
	}
	mustParse(t, `type R = { readonly [K in string]: number }`)
	mustParse(t, `local x: Point["x"] = 1`)
	mustParse(t, `local y: T[number] = 1`)
	// Indexer still parses (no `in` after the name).
	mustParse(t, `type M = { [string]: number }`)
}
