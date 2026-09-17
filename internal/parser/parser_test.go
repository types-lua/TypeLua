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
