package parser

import (
	"strings"
	"testing"

	"github.com/typelua/typelua/internal/ast"
	"github.com/typelua/typelua/internal/diag"
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
	// Modifier operators.
	for _, src := range []string{
		`type A<T> = { [K in keyof T]+?: T[K] }`,
		`type B<T> = { [K in keyof T]-?: T[K] }`,
		`type C<T> = { +readonly [K in keyof T]: T[K] }`,
		`type D<T> = { -readonly [K in keyof T]: T[K] }`,
	} {
		mustParse(t, src)
	}
	fm := mustParse(t, `type E<T> = { -readonly [K in keyof T]-?: T[K] }`)
	em, ok := fm.Block.Stmts[0].(*ast.TypeAliasDecl)
	if !ok {
		t.Fatalf("not alias %T", fm.Block.Stmts[0])
	}
	mt, ok := em.Type.(*ast.MappedType)
	if !ok || !mt.OptMinus || !mt.RoMinus || mt.Optional || mt.Readonly {
		t.Fatalf("bad modifier flags %+v", em.Type)
	}
}

func TestParseMacro(t *testing.T) {
	f := mustParse(t, `macro twice(x) = (x) + (x)`)
	md, ok := f.Block.Stmts[0].(*ast.MacroDef)
	if !ok || !md.IsExpr || len(md.Params) != 1 {
		t.Fatalf("bad macrodef %T", f.Block.Stmts[0])
	}
	f2 := mustParse(t, "macro unless(c, b) do\n if not c then b end\nend")
	md2, ok := f2.Block.Stmts[0].(*ast.MacroDef)
	if !ok || md2.IsExpr || md2.Block == nil {
		t.Fatalf("bad stmt macro %T", f2.Block.Stmts[0])
	}
	mustParse(t, `local macro hidden(x) = x`)
	// `macro` stays usable as an identifier.
	mustParse(t, `local macro = 1`)
	// Variadic `...` must be last.
	fv := mustParse(t, `macro v(fmt, ...) = fmt`)
	mdv, ok := fv.Block.Stmts[0].(*ast.MacroDef)
	if !ok || !mdv.HasVarArg || len(mdv.Params) != 1 {
		t.Fatalf("bad variadic macrodef %+v", fv.Block.Stmts[0])
	}
	mustParse(t, `macro v2(...) = 1`)
	// Errors, all recoverable.
	for _, src := range []string{
		`macro bad(x, x) = x`,
		`macro bad2(self) = self`,
		"macro bad3(x) do\n return x \nend",
		`export macro e(x) = x`,
		`declare macro d(x) = x`,
		`macro bad4(..., x) = x`,
	} {
		if _, diags := ParseFile("t.tl", src); len(diags) == 0 {
			t.Fatalf("expected errors for %q", src)
		}
	}
	// Broken call arguments never leak nils into the AST.
	if _, diags := ParseFile("t.tl", `local r = f(local x = 1)`); len(diags) == 0 {
		t.Fatalf("expected errors for broken call args")
	}
}

func TestConditionalQ(t *testing.T) {
	// `extends ? :` is a conditional; bare `?` is Optional.
	f := mustParse(t, `type X<T> = T extends string ? string : number`)
	if _, ok := f.Block.Stmts[0].(*ast.TypeAliasDecl).Type.(*ast.ConditionalType); !ok {
		t.Fatalf("not conditional %T", f.Block.Stmts[0])
	}
	mustParse(t, `type Y = string?`)
	mustParse(t, `type Z = (string | number)?`)
	// Optional of a conditional parenthesized.
	mustParse(t, `type W<T> = (T extends string ? string : number)?`)
	// Nested conditionals stay right-associative through lookahead.
	mustParse(t, `type N<T> = T extends string ? T extends "a" ? 1 : 2 : 3`)
}

func TestDeepNestingGuard(t *testing.T) {
	for _, src := range []string{
		`local x = ` + strings.Repeat("(", 5000) + `1` + strings.Repeat(")", 5000),
		`local x: ` + strings.Repeat("(", 2000) + `number` + strings.Repeat(")", 2000) + ` = nil`,
		strings.Repeat("do ", 2000) + `local x = 1 ` + strings.Repeat("end ", 2000),
	} {
		_, diags := ParseFile("t.tl", src)
		found := false
		for _, d := range diags {
			if d.Code == diag.ParseNestingTooDeep {
				found = true
			}
		}
		if !found {
			t.Fatalf("expected TL2015, got %v", diags)
		}
	}
}

func TestParseImport(t *testing.T) {
	f := mustParse(t, `import G: Api from "example.com.greet.greet"`)
	im, ok := f.Block.Stmts[0].(*ast.ImportStmt)
	if !ok || im.Name == nil || im.Name.Name != "G" || im.Type == nil || im.Path.Literal != `"example.com.greet.greet"` {
		t.Fatalf("bad import %+v", f.Block.Stmts[0])
	}
	f2 := mustParse(t, `import H from "example.com.other"`)
	im2, ok := f2.Block.Stmts[0].(*ast.ImportStmt)
	if !ok || im2.Name.Name != "H" || im2.Type != nil {
		t.Fatalf("bad untyped import %+v", f2.Block.Stmts[0])
	}
	f3 := mustParse(t, `import "example.com.side"`)
	im3, ok := f3.Block.Stmts[0].(*ast.ImportStmt)
	if !ok || im3.Name != nil {
		t.Fatalf("bad side-effect import %+v", f3.Block.Stmts[0])
	}
	// `import` stays a plain identifier everywhere else.
	mustParse(t, `local import = 1`)
	mustParse(t, `import("x")`)
	mustParse(t, `import.foo()`)
	mustParse(t, "local x = import")
	// A bare `import` followed by a string on the NEXT line never glues
	// into an import (it stays a string-call expression statement).
	f4 := mustParse(t, "import\n\"x\"\n")
	if len(f4.Block.Stmts) != 1 {
		t.Fatalf("expected one statement, got %d", len(f4.Block.Stmts))
	}
	if _, ok := f4.Block.Stmts[0].(*ast.ImportStmt); ok {
		t.Fatalf("newline must not glue an import")
	}
	// Errors, all recoverable.
	for _, src := range []string{
		`import G from`,
		`import G: number from 42`,
		`import 42 from "x"`,
	} {
		if _, diags := ParseFile("t.tl", src); len(diags) == 0 {
			t.Fatalf("expected errors for %q", src)
		}
	}
}

func TestParseDefer(t *testing.T) {
	f := mustParse(t, `defer print("bye")`)
	dm, ok := f.Block.Stmts[0].(*ast.DeferStmt)
	if !ok || dm.IsErr {
		t.Fatalf("bad defer %+v", f.Block.Stmts[0])
	}
	if _, ok := dm.Call.(*ast.CallExpr); !ok {
		t.Fatalf("defer call not preserved: %T", dm.Call)
	}
	f2 := mustParse(t, `errdefer f:close()`)
	dm2, ok := f2.Block.Stmts[0].(*ast.DeferStmt)
	if !ok || !dm2.IsErr {
		t.Fatalf("bad errdefer %+v", f2.Block.Stmts[0])
	}
	if _, ok := dm2.Call.(*ast.MethodCallExpr); !ok {
		t.Fatalf("errdefer method call not preserved: %T", dm2.Call)
	}
	mustParse(t, `defer f<string>("x")`)
	// `defer`/`errdefer` stay plain identifiers elsewhere.
	mustParse(t, `local defer = 1`)
	mustParse(t, `local errdefer = 2`)
	mustParse(t, `defer(x)`)
	mustParse(t, `defer = 1`)
	mustParse(t, `defer.foo()`)
	// Errors, all recoverable.
	for _, src := range []string{
		`defer x`,
		`defer 42`,
		`errdefer`,
	} {
		if _, diags := ParseFile("t.tl", src); len(diags) == 0 {
			t.Fatalf("expected errors for %q", src)
		}
	}
}

func TestParseBraceFuncBody(t *testing.T) {
	// Every function shape accepts `{ ... }` instead of `... end`.
	mustParse(t, `local function f(x: number): number { return x + 1 }`)
	mustParse(t, `local function e() {}`)
	mustParse(t, `local cb = function(a: number, b: number): number { return a + b }`)
	mustParse(t, `function M.m(self, x: number) { print(x) }`)
	mustParse(t, "local t = { f = function() { return 1 } }")
	mustParse(t, "local function outer(): number\n  local function inner(): number { return 7 }\n  return inner()\nend")
	mustParse(t, `class C {
  constructor(x: number) { self.x = x },
  m(): number { return 1 },
}`)
	mustParse(t, `local r = table.sort({3, 1, 2}, function(a: number, b: number): boolean { return a < b })`)
	// The body lands in the same AST slot as the `end` form.
	f := mustParse(t, `local function f(x: number): number { return x + 1 }`)
	lf, ok := f.Block.Stmts[0].(*ast.LocalFuncDef)
	if !ok {
		t.Fatalf("not localfuncdef %T", f.Block.Stmts[0])
	}
	if lf.Func == nil || lf.Func.Body == nil || lf.Func.Body.Ret == nil {
		t.Fatalf("brace body missing return")
	}
	// Errors, all recoverable: missing `}` and mixed closers.
	for _, src := range []string{
		`local function f(x: number): number { return x + 1 `,
		`local function f(x: number): number { return x + 1 end }`,
		`class C {
  m(): number { return 1 end },
}`,
	} {
		if _, diags := ParseFile("t.tl", src); len(diags) == 0 {
			t.Fatalf("expected errors for %q", src)
		}
	}
}
