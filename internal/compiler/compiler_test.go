package compiler

import (
	"encoding/json"
	"os"
	"strings"
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

func TestSourceMapEmit(t *testing.T) {
	cfg := testConfig()
	cfg.CompilerOptions.OutDir = t.TempDir()
	cfg.CompilerOptions.SourceMap = true
	p := NewProgram(cfg)
	p.LoadSources(map[string]string{"a.tl": "local x: number = 1\nprint(x)\n"})
	p.Check()
	if p.HasErrors() {
		t.Fatalf("diags:\n%s", p.FormatDiags())
	}
	emitted, err := p.Emit()
	if err != nil {
		t.Fatal(err)
	}
	if len(emitted) != 1 {
		t.Fatalf("emitted: %v", emitted)
	}
	dst := emitted[0]
	lua, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(string(lua), "--# sourceMappingURL=a.lua.map\n") {
		t.Fatalf("missing footer:\n%s", lua)
	}
	raw, err := os.ReadFile(dst + ".map")
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	if doc["version"].(float64) != 3 {
		t.Fatalf("version: %v", doc["version"])
	}
	srcs := doc["sources"].([]any)
	if len(srcs) != 1 || srcs[0].(string) != "a.tl" {
		t.Fatalf("sources: %v", srcs)
	}
	if doc["mappings"].(string) == "" {
		t.Fatalf("empty mappings")
	}
	contents := doc["sourcesContent"].([]any)
	if len(contents) != 1 || !strings.Contains(contents[0].(string), "local x") {
		t.Fatalf("sourcesContent: %v", contents)
	}
}

func TestNoSourceMapByDefault(t *testing.T) {
	cfg := testConfig()
	cfg.CompilerOptions.OutDir = t.TempDir()
	p := NewProgram(cfg)
	p.LoadSources(map[string]string{"a.tl": "local x: number = 1\n"})
	p.Check()
	emitted, err := p.Emit()
	if err != nil {
		t.Fatal(err)
	}
	lua, err := os.ReadFile(emitted[0])
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(lua), "sourceMappingURL") {
		t.Fatalf("unexpected footer:\n%s", lua)
	}
	if _, err := os.Stat(emitted[0] + ".map"); !os.IsNotExist(err) {
		t.Fatalf("unexpected map file")
	}
}
