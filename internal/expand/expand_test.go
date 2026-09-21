package expand

import (
	"strings"
	"testing"

	"github.com/typelua/typelua/internal/emitter"
	"github.com/typelua/typelua/internal/parser"
)

func expandEmit(t *testing.T, src string) (string, []string) {
	t.Helper()
	f, pdiags := parser.ParseFile("test.tl", src)
	if len(pdiags) != 0 {
		t.Fatalf("parse diags: %v", pdiags)
	}
	ediags := ExpandFile(f)
	var codes []string
	for _, d := range ediags {
		codes = append(codes, d.Code)
	}
	return emitter.EmitFile(f, emitter.DefaultOptions()), codes
}

func TestHygieneCapture(t *testing.T) {
	out, codes := expandEmit(t, `
local t = "caller"
macro mangle(x) do
  local t = 1
  print(t)
  print(x)
end
mangle(t)
`)
	if len(codes) != 0 {
		t.Fatalf("diags: %v", codes)
	}
	// Macro-local t renamed; caller's t untouched in both positions.
	if strings.Count(out, "__tl_macro_") != 2 {
		t.Fatalf("expected 2 fresh refs, got:\n%s", out)
	}
	if !strings.Contains(out, `print(t)`) {
		t.Fatalf("caller t must survive:\n%s", out)
	}
}

func TestHygieneShadowParam(t *testing.T) {
	out, codes := expandEmit(t, `
macro evil(x) do
  local x = 99
  print(x)
end
local x = "outer"
evil(x)
print(x)
`)
	if len(codes) != 0 {
		t.Fatalf("diags: %v", codes)
	}
	// Last print must still see the outer x.
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if lines[len(lines)-1] != "print(x)" {
		t.Fatalf("outer x captured:\n%s", out)
	}
}

func TestDeterministicDoubleExpand(t *testing.T) {
	src := `
macro withTmp(x) do
  local t = x
  print(t)
end
withTmp(1)
withTmp(2)
`
	out1, _ := expandEmit(t, src)
	out2, _ := expandEmit(t, src)
	if out1 != out2 {
		t.Fatalf("non-deterministic:\n%s\n---\n%s", out1, out2)
	}
	// Expanding twice must not corrupt the definition (fresh counter per file).
	if strings.Count(out1, "__tl_macro_1__") != 2 || strings.Count(out1, "__tl_macro_2__") != 2 {
		t.Fatalf("unexpected fresh numbering:\n%s", out1)
	}
}

func TestRecursionAndArity(t *testing.T) {
	_, codes := expandEmit(t, "macro loop(x) = loop(x)\nlocal b = loop(1)\n")
	if len(codes) != 1 || codes[0] != "TL3703" {
		t.Fatalf("want TL3703, got %v", codes)
	}
	_, codes = expandEmit(t, "macro twice(x) = (x) + (x)\nlocal a = twice(1, 2)\n")
	if len(codes) != 1 || codes[0] != "TL3702" {
		t.Fatalf("want TL3702, got %v", codes)
	}
	_, codes = expandEmit(t, "macro doIt(x) do print(x) end\nlocal c = doIt(1)\n")
	if len(codes) != 1 || codes[0] != "TL3704" {
		t.Fatalf("want TL3704, got %v", codes)
	}
}

func TestScopedVisibility(t *testing.T) {
	// Macro defined inside a function is invisible outside: call survives.
	out, codes := expandEmit(t, `
local function f()
  macro twice(x) = (x) + (x)
  return twice(3)
end
local g = twice(4)
`)
	if len(codes) != 0 {
		t.Fatalf("diags: %v", codes)
	}
	if !strings.Contains(out, "twice(4)") {
		t.Fatalf("outer call must stay unexpanded:\n%s", out)
	}
	if strings.Contains(out, "twice(3)") {
		t.Fatalf("inner call must expand:\n%s", out)
	}
}

func TestLabelHygiene(t *testing.T) {
	out, codes := expandEmit(t, `
macro withLabel(v) do
  goto done
  print(v)
  ::done::
end
withLabel(1)
withLabel(2)
`)
	if len(codes) != 0 {
		t.Fatalf("diags: %v", codes)
	}
	// Two expansions need distinct labels.
	if strings.Count(out, "__tl_macro_1__done") != 2 || strings.Count(out, "__tl_macro_2__done") != 2 {
		t.Fatalf("labels not hygienic:\n%s", out)
	}
}

func TestVariadicMacro(t *testing.T) {
	// Extras splice into call arguments.
	out, codes := expandEmit(t, `
macro wrap(f, ...) = f(...)
local r = wrap(print, 1, "a")
`)
	if len(codes) != 0 {
		t.Fatalf("diags: %v", codes)
	}
	if !strings.Contains(out, `print(1, "a")`) {
		t.Fatalf("extras not spliced:\n%s", out)
	}
	// Zero extras splice to nothing.
	out, codes = expandEmit(t, `
macro fwd(f, ...) = f(...)
local r = fwd(print)
`)
	if len(codes) != 0 {
		t.Fatalf("diags: %v", codes)
	}
	if !strings.Contains(out, `print()`) {
		t.Fatalf("empty splice:\n%s", out)
	}
	// Statement macro form.
	out, codes = expandEmit(t, `
macro each(f, ...) do f(...) end
each(print, 1, 2)
`)
	if len(codes) != 0 {
		t.Fatalf("diags: %v", codes)
	}
	if !strings.Contains(out, `print(1, 2)`) {
		t.Fatalf("stmt splice:\n%s", out)
	}
	// Too few arguments still errors.
	_, codes = expandEmit(t, "macro req(a, b, ...) = a\nlocal r = req(1)\n")
	if len(codes) != 1 || codes[0] != "TL3702" {
		t.Fatalf("want TL3702, got %v", codes)
	}
	// Bare `...` outside call arguments errors.
	_, codes = expandEmit(t, "macro bad(...) = ...\nlocal r = bad(1)\n")
	if len(codes) != 1 || codes[0] != "TL3706" {
		t.Fatalf("want TL3706, got %v", codes)
	}
	// `...` inside a nested function belongs to it, not the macro.
	out, codes = expandEmit(t, `
macro mk(...) = print(function() return ... end)
local r = mk(1)
`)
	if len(codes) != 0 {
		t.Fatalf("diags: %v", codes)
	}
	if !strings.Contains(out, `return ...`) {
		t.Fatalf("inner ... must survive:\n%s", out)
	}
}

func TestImportInMacro(t *testing.T) {
	// An import inside a macro body binds hygienically like a local.
	out, codes := expandEmit(t, `
macro m(q) do
  import q from "p"
  print(q)
end
m(42)
`)
	if len(codes) != 0 {
		t.Fatalf("diags: %v", codes)
	}
	// Bound once, used once, both renamed; the argument never leaks in.
	if strings.Count(out, "__tl_macro_1__q") != 2 {
		t.Fatalf("import binding not hygienic:\n%s", out)
	}
	if strings.Contains(out, "print(42)") {
		t.Fatalf("argument captured:\n%s", out)
	}
}
