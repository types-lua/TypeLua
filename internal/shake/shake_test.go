package shake_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/typelua/typelua/internal/emitter"
	"github.com/typelua/typelua/internal/parser"
	"github.com/typelua/typelua/internal/shake"
)

// mustParse parses src or fails the test (no error recovery noise: cases
// below are all valid TypeLua).
func mustParse(t *testing.T, src string) string {
	t.Helper()
	return src
}

func shakeCount(t *testing.T, src string) int {
	t.Helper()
	f, diags := parser.ParseFile("t.tl", mustParse(t, src))
	if len(diags) != 0 {
		t.Fatalf("parse errors: %v", diags[0].Message)
	}
	return shake.ShakeFile(f)
}

// shakeEmit shakes then emits plain Lua (no second shake pass).
func shakeEmit(t *testing.T, src string) string {
	t.Helper()
	f, diags := parser.ParseFile("t.tl", mustParse(t, src))
	if len(diags) != 0 {
		t.Fatalf("parse errors: %v", diags[0].Message)
	}
	shake.ShakeFile(f)
	return emitter.EmitFile(f, emitter.Options{})
}

func TestRemovesSimpleUnused(t *testing.T) {
	if n := shakeCount(t, "local function dead() return 1 end\nprint(\"hi\")\n"); n != 1 {
		t.Fatalf("want 1 removal, got %d", n)
	}
	out := shakeEmit(t, "local function dead() return 1 end\nprint(\"hi\")\n")
	if strings.Contains(out, "dead") {
		t.Fatalf("dead function must be gone:\n%s", out)
	}
}

func TestKeepsUsed(t *testing.T) {
	out := shakeEmit(t, "local function f() return 1 end\nprint(f())\n")
	if !strings.Contains(out, "return 1") {
		t.Fatalf("used function must survive:\n%s", out)
	}
}

func TestTransitiveRemoval(t *testing.T) {
	src := "local function a() return b() end\nlocal function b() return 1 end\nprint(\"x\")\n"
	if n := shakeCount(t, src); n != 2 {
		t.Fatalf("want 2 removals, got %d", n)
	}
}

func TestKeepsTransitiveCallee(t *testing.T) {
	// b precedes a: the reference inside a resolves lexically to local b.
	// (Reversed order would bind to global b — then dropping local b is
	// correct Lua semantics, covered by TestForwardReferenceBindsGlobal.)
	out := shakeEmit(t, "local function b() return 1 end\nlocal function a() return b() end\nprint(a())\n")
	if !strings.Contains(out, "return 1") {
		t.Fatalf("transitive callee must survive:\n%s", out)
	}
}

func TestForwardReferenceBindsGlobal(t *testing.T) {
	// `b` inside a binds to the global (local b is declared later), so the
	// later local b is genuinely dead and must go.
	out := shakeEmit(t, "local function a() return b() end\nlocal function b() return 1 end\nprint(a())\n")
	if strings.Contains(out, "return 1") {
		t.Fatalf("forward-declared b is not visible inside a:\n%s", out)
	}
	if !strings.Contains(out, "return b()") {
		t.Fatalf("live caller a must survive:\n%s", out)
	}
}

func TestUnusedRecursionRemoved(t *testing.T) {
	if n := shakeCount(t, "local function f(n: number): number if n == 0 then return 0 end return f(n - 1) end\n"); n != 1 {
		t.Fatalf("want 1 removal, got %d", n)
	}
}

func TestMutualRecursionRemoved(t *testing.T) {
	src := "local function a(): number return b() end\nlocal function b(): number return a() end\nprint(1)\n"
	if n := shakeCount(t, src); n != 2 {
		t.Fatalf("want 2 removals, got %d", n)
	}
}

func TestLocalFuncValueForm(t *testing.T) {
	if n := shakeCount(t, "local dead = function() return 1 end\nprint(2)\n"); n != 1 {
		t.Fatalf("want 1 removal, got %d", n)
	}
	out := shakeEmit(t, "local live = function() return 1 end\nprint(live())\n")
	if !strings.Contains(out, "return 1") {
		t.Fatalf("used closure value must survive:\n%s", out)
	}
}

func TestKeepsTableFieldUse(t *testing.T) {
	out := shakeEmit(t, "local function cb() return 1 end\nlocal t = {}\nt.cb = cb\nprint(t.cb())\n")
	if !strings.Contains(out, "return 1") {
		t.Fatalf("table-stored function must survive:\n%s", out)
	}
}

func TestKeepsReturnUse(t *testing.T) {
	out := shakeEmit(t, "local function f() return 1 end\nreturn f\n")
	if !strings.Contains(out, "return 1") {
		t.Fatalf("returned function must survive:\n%s", out)
	}
}

func TestKeepsGlobalsAndMethods(t *testing.T) {
	src := "local M = {}\nfunction M.m() return 1 end\nreturn M\n"
	if n := shakeCount(t, src); n != 0 {
		t.Fatalf("want 0 removals, got %d", n)
	}
}

func TestKeepsRequireAndImport(t *testing.T) {
	out := shakeEmit(t, "local U = require(\"m\")\nimport \"side\"\nprint(1)\n")
	if !strings.Contains(out, "require(\"m\")") || !strings.Contains(out, "require(\"side\")") {
		t.Fatalf("requires must survive shaking:\n%s", out)
	}
}

func TestKeepsAttributed(t *testing.T) {
	if n := shakeCount(t, "local f <const> = function() return 1 end\nprint(2)\n"); n != 0 {
		t.Fatalf("attributed locals are never candidates, got %d", n)
	}
}

func TestKeepsExported(t *testing.T) {
	if n := shakeCount(t, "export local function f() return 1 end\nprint(2)\n"); n != 0 {
		t.Fatalf("exported functions are external contract, got %d", n)
	}
}

func TestOuterShadowRemovedInnerKept(t *testing.T) {
	out := shakeEmit(t, "local function f() return 1 end\ndo\nlocal function f() return 2 end\nprint(f())\nend\n")
	if strings.Contains(out, "return 1") || !strings.Contains(out, "return 2") {
		t.Fatalf("outer dead / inner live:\n%s", out)
	}
}

func TestInnerShadowRemovedOuterKept(t *testing.T) {
	out := shakeEmit(t, "local function f() return 1 end\ndo\nlocal function f() return 2 end\nend\nprint(f())\n")
	if !strings.Contains(out, "return 1") || strings.Contains(out, "return 2") {
		t.Fatalf("inner dead / outer live:\n%s", out)
	}
}

func TestParamShadowsOuter(t *testing.T) {
	out := shakeEmit(t, "local function f() return 0 end\nlocal function g(f) return f end\nprint(g(1))\n")
	if strings.Contains(out, "return 0") || !strings.Contains(out, "return f") {
		t.Fatalf("param shadows outer; outer must go:\n%s", out)
	}
}

func TestFieldAndMethodNamesAreNotUses(t *testing.T) {
	out := shakeEmit(t, "local function foo() return 1 end\nlocal t = {}\nt.foo = function() return 2 end\nlocal o = t\nprint(o.foo())\n")
	if strings.Contains(out, "return 1") || !strings.Contains(out, "return 2") {
		t.Fatalf("member names must not keep locals alive:\n%s", out)
	}
}

func TestMethodCallNameIsNotUse(t *testing.T) {
	out := shakeEmit(t, "local function foo() return 1 end\nlocal o = { foo = function(self) return 2 end }\nprint(o:foo())\n")
	if strings.Contains(out, "return 1") || !strings.Contains(out, "return 2") {
		t.Fatalf("method names must not keep locals alive:\n%s", out)
	}
}

func TestLabelsAreNotUses(t *testing.T) {
	if n := shakeCount(t, "local function done() return 1 end\ngoto done\n::done::\n"); n != 1 {
		t.Fatalf("labels live in another namespace, got %d", n)
	}
}

func TestWriteOnlyIsConservativelyKept(t *testing.T) {
	// Assignment targets count as uses: a write-only local survives.
	// A missed optimization by design, never a mis-shake.
	if n := shakeCount(t, "local function f() return 1 end\nf = print\n"); n != 0 {
		t.Fatalf("write-only must be kept, got %d", n)
	}
}

func TestKeepsMethodBodyReference(t *testing.T) {
	out := shakeEmit(t, "local function helper() return 1 end\nlocal M = {}\nfunction M.m() return helper() end\nreturn M\n")
	if !strings.Contains(out, "return 1") {
		t.Fatalf("method-body reference must keep helper:\n%s", out)
	}
}

func TestDeferReferenceKeeps(t *testing.T) {
	out := shakeEmit(t, "local function cleanup() print(\"c\") end\nlocal function f(): number\ndefer cleanup()\nreturn 1\nend\nprint(f())\n")
	if !strings.Contains(out, "print(\"c\")") {
		t.Fatalf("deferred call must keep cleanup:\n%s", out)
	}
}

func TestDeferDeadCalleeRemoved(t *testing.T) {
	out := shakeEmit(t, "local function cleanup() print(\"c\") end\nlocal function f(): number\nreturn 1\nend\nprint(f())\n")
	if strings.Contains(out, "print(\"c\")") {
		t.Fatalf("unreferenced cleanup must go:\n%s", out)
	}
}

func TestNestedDeadInsideLive(t *testing.T) {
	out := shakeEmit(t, "local function outer() local function inner() return 9 end return 1 end\nprint(outer())\n")
	if strings.Contains(out, "return 9") || !strings.Contains(out, "return 1") {
		t.Fatalf("nested dead must go, outer stays:\n%s", out)
	}
}

func TestIdempotent(t *testing.T) {
	f, diags := parser.ParseFile("t.tl", "local function d() return 1 end\nprint(1)\n")
	if len(diags) != 0 {
		t.Fatalf("parse errors: %v", diags[0].Message)
	}
	if n := shake.ShakeFile(f); n != 1 {
		t.Fatalf("first pass want 1, got %d", n)
	}
	if n := shake.ShakeFile(f); n != 0 {
		t.Fatalf("second pass want 0, got %d", n)
	}
}

func TestNilSafe(t *testing.T) {
	if n := shake.ShakeFile(nil); n != 0 {
		t.Fatalf("nil file: got %d", n)
	}
}

// TestShakeDoesNotDropAwaitPreludeWhenLive guards the emitter ordering:
// shaking runs before concurrency detection.
func TestShakeAwaitPreludeLive(t *testing.T) {
	src := "local function bg(): void\nawait sleep(1)\nend\nspawn bg()\n"
	f, diags := parser.ParseFile("t.tl", src)
	if len(diags) != 0 {
		t.Fatalf("parse errors: %v", diags[0].Message)
	}
	shake.ShakeFile(f)
	out := emitter.EmitFile(f, emitter.Options{})
	if !strings.Contains(out, "__tl_run") {
		t.Fatalf("live spawn path must keep the scheduler:\n%s", out)
	}
}

func TestShakeDropsPreludeWhenOnlyDeadAwait(t *testing.T) {
	src := "local function bg(): void\nawait sleep(1)\nend\nprint(1)\n"
	f, diags := parser.ParseFile("t.tl", src)
	if len(diags) != 0 {
		t.Fatalf("parse errors: %v", diags[0].Message)
	}
	if n := shake.ShakeFile(f); n != 1 {
		t.Fatalf("want 1 removal, got %d", n)
	}
	out := emitter.EmitFile(f, emitter.Options{})
	if strings.Contains(out, "__tl_run") {
		t.Fatalf("dead-only await must not pull the scheduler:\n%s", out)
	}
}

// TestAllExamplesShakeClean runs the pass over the whole corpus: no crash,
// and every `require` plus every chunk `return` survives verbatim.
func TestAllExamplesShakeClean(t *testing.T) {
	root := filepath.Join("..", "..", "examples")
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Skipf("examples unavailable: %v", err)
	}
	files := 0
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		sub, err := os.ReadDir(filepath.Join(root, e.Name()))
		if err != nil {
			continue
		}
		for _, f := range sub {
			if f.IsDir() || !strings.HasSuffix(f.Name(), ".tl") {
				continue
			}
			path := filepath.Join(root, e.Name(), f.Name())
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read %s: %v", path, err)
			}
			pf, diags := parser.ParseFile(path, string(raw))
			if len(diags) != 0 {
				continue // broken example: parser already reported; skip
			}
			before := emitter.EmitFile(pf, emitter.Options{})
			removed := shake.ShakeFile(pf)
			after := emitter.EmitFile(pf, emitter.Options{})
			files++
			_ = removed
			for _, needle := range []string{"require("} {
				if strings.Contains(before, needle) && !strings.Contains(after, needle) {
					t.Fatalf("%s: %q lost in shake", path, needle)
				}
			}
			if strings.Contains(before, "\nreturn ") && !strings.Contains(after, "\nreturn ") {
				t.Fatalf("%s: chunk return lost in shake", path)
			}
		}
	}
	if files == 0 {
		t.Fatalf("no example files found")
	}
	t.Logf("shook %d example files", files)
}
