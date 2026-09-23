package compiler

import (
	"strings"
	"testing"

	"github.com/typelua/typelua/internal/config"
	"github.com/typelua/typelua/internal/diag"
)

// diagCodes runs the full pipeline (lex → parse → expand → check) on one
// in-memory source and returns every diagnostic code it produced.
func diagCodes(t *testing.T, src string) []diag.Code {
	t.Helper()
	dir := t.TempDir()
	cfg := config.Default(dir)
	p := NewProgram(cfg)
	p.LoadSources(map[string]string{"main.tl": src})
	p.Check()
	var codes []diag.Code
	for _, d := range p.Diags {
		codes = append(codes, d.Code)
	}
	return codes
}

func hasCode(codes []diag.Code, want diag.Code) bool {
	for _, c := range codes {
		if c == want {
			return true
		}
	}
	return false
}

// TestDiagnosticCodeStability pins one representative code per stage and
// category, so refactors cannot silently renumber user-visible
// diagnostics. Every expectation is a registry constant.
func TestDiagnosticCodeStability(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want diag.Code
	}{
		{"lexer: invalid character", "local x = §\n", diag.LexInvalidCharacter},
		{"parser: stray expression", "1 + 2\n", diag.ParseExpectedCall},
		{"parser: nesting limit", strings.Repeat("(", 300) + "1" + strings.Repeat(")", 300), diag.ParseNestingTooDeep},
		{"checker: not assignable", "local x: number = \"s\"\n", diag.TypeNotAssignable},
		{"checker: undefined variable", "print(nope)\n", diag.UndefinedVariableExpr},
		{"checker: no such field", "local t: { a: number } = { a = 1 }\nprint(t.b)\n", diag.NoField},
		{"checker: class access", `class C {
  private x: number = 1,
}
local c = C.new()
print(c.x)
`, diag.AccessViolation},
		{"checker: enum member value", `enum E { A = "a", B }
`, diag.EnumMemberNeedsValue},
		{"checker: cannot map", "type M = { [K in boolean]: number }\n", diag.CannotMapOverType},
		{"expander: macro arity", `macro m(a) = (a)
local x = m()
`, diag.MacroArityMismatch},
		{"concurrency: await outside async", `local function f(): number
  local x = await g()
  return 1
end
`, diag.AwaitOutsideAsync},
		{"concurrency: async call in sync", `local function f(): void
  sleep(1)
end
`, diag.AsyncCallOutsideAsync},
		{"concurrency: makeChan needs T", "local ch = makeChan()\n", diag.MakeChanInvalid},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			codes := diagCodes(t, tc.src)
			if len(codes) == 0 {
				t.Fatalf("expected %s, got no diagnostics\nsrc:\n%s", tc.want, tc.src)
			}
			if !hasCode(codes, tc.want) {
				t.Fatalf("expected %s, got %v\nsrc:\n%s", tc.want, codes, tc.src)
			}
			for _, c := range codes {
				if !c.Known() {
					t.Fatalf("unknown diagnostic code %q (not in the registry)", c)
				}
				if c.Category() == diag.CatUnknown {
					t.Fatalf("code %s has no category", c)
				}
			}
		})
	}
}

// TestExampleDiagnosticsUseKnownCodes checks the repo's own examples: they
// must stay clean, and any diagnostic that ever appears must come from the
// registry (guards accidental raw strings / unregistered codes).
func TestExampleDiagnosticsUseKnownCodes(t *testing.T) {
	cfg, err := config.Load("../../tlua.json")
	if err != nil {
		t.Skipf("repo tlua.json unavailable: %v", err)
	}
	p := NewProgram(cfg)
	if err := p.Load(); err != nil {
		t.Fatal(err)
	}
	p.Check()
	for _, d := range p.Diags {
		if !d.Code.Known() {
			t.Errorf("unregistered diagnostic code %q in %s: %s", d.Code, d.Span.File, d.Message)
		}
	}
	if p.HasErrors() {
		t.Fatalf("examples must check clean:\n%s", p.FormatDiags())
	}
}
