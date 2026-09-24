package compiler

import (
	"strings"
	"testing"
)

func TestTreeShakeOptionDropsDead(t *testing.T) {
	cfg := testAmbientConfig(t.TempDir())
	cfg.CompilerOptions.TreeShake = true
	p := NewProgram(cfg)
	p.LoadSources(map[string]string{
		"a.tl": "local function dead() return 1 end\nlocal function live() return 2 end\nprint(live())\n",
	})
	p.Check()
	if p.HasErrors() {
		t.Fatalf("diags:\n%s", p.FormatDiags())
	}
	out := p.EmitToString()["a.tl"]
	if strings.Contains(out, "return 1") || !strings.Contains(out, "return 2") {
		t.Fatalf("treeshake emit:\n%s", out)
	}
}

func TestTreeShakeOffByDefault(t *testing.T) {
	cfg := testAmbientConfig(t.TempDir())
	p := NewProgram(cfg)
	p.LoadSources(map[string]string{
		"a.tl": "local function dead() return 1 end\nprint(2)\n",
	})
	p.Check()
	out := p.EmitToString()["a.tl"]
	if !strings.Contains(out, "return 1") {
		t.Fatalf("default build must keep everything:\n%s", out)
	}
}
