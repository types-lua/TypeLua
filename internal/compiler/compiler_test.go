package compiler

import (
	"testing"

	"github.com/typelua/typelua/internal/config"
	"github.com/typelua/typelua/internal/parser"
)

func testConfig() *config.Config {
	c := config.Default(".")
	c.CompilerOptions.RootDir = "."
	return c
}

func TestProgramOK(t *testing.T) {
	p := NewProgram(testConfig())
	p.LoadSources(map[string]string{
		"a.tl": `local x: number = 1`,
	})
	p.Check()
	if p.HasErrors() {
		t.Fatalf("diags:\n%s", p.FormatDiags())
	}
	out := p.EmitToString()
	if out["a.tl"] != "local x = 1\n" {
		t.Fatalf("emit %q", out["a.tl"])
	}
}

func TestRoundTrip(t *testing.T) {
	srcs := map[string]string{
		"m.tl": "local function foo(a: number): number return a + 1 end\nlocal x = foo(1)\n",
	}
	p := NewProgram(testConfig())
	p.LoadSources(srcs)
	p.Check()
	if p.HasErrors() {
		t.Fatalf("%s", p.FormatDiags())
	}
	for _, sf := range p.Files {
		_ = sf
	}
	emitted := p.EmitToString()["m.tl"]
	// Emitted Lua must re-parse cleanly (superset property).
	_, diags := parser.ParseFile("m.lua", emitted)
	if len(diags) != 0 {
		t.Fatalf("re-parse failed: %v\n%s", diags, emitted)
	}
}

func TestProgramErr(t *testing.T) {
	p := NewProgram(testConfig())
	p.LoadSources(map[string]string{"b.tl": `local x: number = "s"`})
	p.Check()
	if !p.HasErrors() {
		t.Fatalf("expected errors")
	}
}
