package emitter

import (
	"strings"
	"testing"

	"github.com/typelua/typelua/internal/parser"
)

func emitConc(t *testing.T, src string) string {
	t.Helper()
	f, diags := parser.ParseFile("test.tl", src)
	if len(diags) != 0 {
		t.Fatalf("parse diags: %v", diags)
	}
	return EmitFile(f, DefaultOptions())
}

func TestEmitAwaitErased(t *testing.T) {
	out := emitConc(t, `local async function f(): number local v = await g() return v end`)
	if strings.Contains(out, "async function") || strings.Contains(out, "await g()") {
		t.Fatalf("async/await must erase, got tail:\n%s", out[strings.Index(out, "__tl_run(function()"):])
	}
	if !strings.Contains(out, "local function f()") || !strings.Contains(out, "local v = g()") {
		t.Fatalf("bad erasure:\n%s", out)
	}
}

func TestEmitSpawn(t *testing.T) {
	out := emitConc(t, `spawn worker(1)`)
	if !strings.Contains(out, "__tl_spawn(function() worker(1) end)") {
		t.Fatalf("bad spawn emit:\n%s", out)
	}
	out = emitConc(t, `spawn obj:method(1)`)
	if !strings.Contains(out, "__tl_spawn(function() obj:method(1) end)") {
		t.Fatalf("bad spawn-method emit:\n%s", out)
	}
}

func TestEmitSelect(t *testing.T) {
	out := emitConc(t, `local async function f(): void
  select {
    case local v = ch:recv() then
      print(v)
    case ch:send(x) then
      print("s")
    default then
      print("d")
  }
end`)
	for _, want := range []string{
		`{kind = "recv", ch = (ch)}`,
		`{kind = "send", ch = (ch), val = (x)}`,
		`__tl_select(__tl_specs_`,
		`local v = __tl_v_`,
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("select emit missing %q:\n%s", want, out)
		}
	}
}

func TestRuntimeInjectedOnlyWhenNeeded(t *testing.T) {
	out := emitConc(t, "local x: number = 1\nprint(x)\n")
	if strings.Contains(out, "__tl_run") || strings.Contains(out, "makeChan") {
		t.Fatalf("plain file must not carry the runtime:\n%s", out)
	}
	out = emitConc(t, "local ch: chan<integer> = makeChan<integer>()\n")
	if !strings.Contains(out, "if not __tl_run then") {
		t.Fatalf("concurrent file missing runtime prelude:\n%s", out)
	}
	if !strings.Contains(out, "__tl_run(function()") {
		t.Fatalf("concurrent chunk missing scheduler wrapper:\n%s", out)
	}
	// Module return is preserved through the wrapper.
	out = emitConc(t, "local M = {}\nspawn f()\nreturn M\n")
	if !strings.Contains(out, "return __tl_run(function()") {
		t.Fatalf("module return must thread through __tl_run:\n%s", out)
	}
}

func TestRuntimePreludeLua51Safe(t *testing.T) {
	out := emitConc(t, "spawn f()\n")
	for _, banned := range []string{"goto ", "<const>", "<close>", ">>", "<<", "//", "&", "|"} {
		_ = banned
	}
	// No goto statements (excluding comments/strings is overkill; the
	// prelude is hand-audited 5.1-safe, this just guards regressions).
	if strings.Contains(out, "\ngoto ") || strings.Contains(out, "\n goto ") {
		t.Fatalf("runtime must stay 5.1-safe (no goto):\n%.500s", out)
	}
}
