package compiler

import (
	"encoding/json"
	"os"
	"path/filepath"
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

func TestRequireInference(t *testing.T) {
	p := NewProgram(testConfig())
	p.LoadSources(map[string]string{
		"lib.tl": `local M = {}
function M.add(a: number, b: number): number return a + b end
return M
`,
		"a.tl": `local M = require("lib")
local n: number = M.add(1, 2)
print(n)
`,
	})
	p.Check()
	if p.HasErrors() {
		t.Fatalf("inferred require should check clean:\n%s", p.FormatDiags())
	}
}

func TestRequireMismatch(t *testing.T) {
	p := NewProgram(testConfig())
	p.LoadSources(map[string]string{
		"lib.tl": `local M = {}
function M.add(a: number, b: number): number return a + b end
return M
`,
		"a.tl": `local M = require("lib")
local s: string = M.add(1, 2)
`,
	})
	p.Check()
	if !p.HasErrors() {
		t.Fatalf("return-type mismatch through require must error")
	}
}

func TestRequireMissingStaysAny(t *testing.T) {
	p := NewProgram(testConfig())
	p.LoadSources(map[string]string{
		"a.tl": `local M = require("nope.missing")
local v = M.anything
print(v)
`,
	})
	p.Check()
	if p.HasErrors() {
		t.Fatalf("missing modules must stay any:\n%s", p.FormatDiags())
	}
}

func TestRequireCycleStaysAny(t *testing.T) {
	p := NewProgram(testConfig())
	p.LoadSources(map[string]string{
		"a.tl": `local B = require("b")
local M = {}
function M.fa(): number return 1 end
return M
`,
		"b.tl": `local A = require("a")
local M = {}
function M.fb(): number return 2 end
return M
`,
	})
	p.Check()
	if p.HasErrors() {
		t.Fatalf("cyclic requires must degrade to any:\n%s", p.FormatDiags())
	}
}

func TestImportInfersWithoutAnnotation(t *testing.T) {
	p := NewProgram(testConfig())
	p.LoadSources(map[string]string{
		"lib.tl": `local M = {}
function M.add(a: number, b: number): number return a + b end
return M
`,
		"a.tl": `import M from "lib"
local n: number = M.add(1, 2)
print(n)
`,
	})
	p.Check()
	if p.HasErrors() {
		t.Fatalf("unannotated import should infer:\n%s", p.FormatDiags())
	}
}

func TestImportAnnotationConflict(t *testing.T) {
	p := NewProgram(testConfig())
	p.LoadSources(map[string]string{
		"lib.tl": `local M = {}
function M.add(a: number, b: number): number return a + b end
return M
`,
		"a.tl": `import M: { add(a: number): string } from "lib"
local s: string = M.add(1)
`,
	})
	p.Check()
	if !p.HasErrors() {
		t.Fatalf("annotation conflicting with inferred module type must error")
	}
}

func TestRequireEdges(t *testing.T) {
	// Shadowed require stays a plain (failing) call.
	p := NewProgram(testConfig())
	p.LoadSources(map[string]string{
		"a.tl": `local require = 1
local v = require("lib")
`,
	})
	p.Check()
	if !p.HasErrors() {
		t.Fatalf("calling a shadowed require must error")
	}
	// Non-literal and multi-arg requires fall through to any.
	p = NewProgram(testConfig())
	p.LoadSources(map[string]string{
		"a.tl": `local name = "lib"
local M = require(name)
local v = M.anything
`,
	})
	p.Check()
	if p.HasErrors() {
		t.Fatalf("dynamic require must stay any:\n%s", p.FormatDiags())
	}
	// Self-require degrades without hanging.
	p = NewProgram(testConfig())
	p.LoadSources(map[string]string{
		"a.tl": `local M = require("a")
local v = M
return M
`,
	})
	p.Check()
	// .d.tl targets stay any (no runtime value).
	p = NewProgram(testConfig())
	p.LoadSources(map[string]string{
		"lib.d.tl": `declare function external(s: string): number
`,
		"a.tl": `local M = require("lib")
local v = M
`,
	})
	p.Check()
	if p.HasErrors() {
		t.Fatalf("decl targets must stay any:\n%s", p.FormatDiags())
	}
	// Chained and subdirectory requires.
	p = NewProgram(testConfig())
	p.LoadSources(map[string]string{
		"sub/deep.tl": `local M = {}
function M.k(): number return 7 end
return M
`,
		"mid.tl": `local D = require("sub.deep")
local M = {}
function M.m(): number return D.k() end
return M
`,
		"a.tl": `local M = require("mid")
local n: number = M.m()
print(n)
`,
	})
	p.Check()
	if p.HasErrors() {
		t.Fatalf("chained requires should resolve:\n%s", p.FormatDiags())
	}
}

// diskProject creates a real directory project for manifest-aware tests.
func diskProject(t *testing.T, files map[string]string) (*Program, string) {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cfg := config.Default(dir)
	cfg.CompilerOptions.RootDir = "."
	p := NewProgram(cfg)
	if err := p.Load(); err != nil {
		t.Fatal(err)
	}
	return p, dir
}

func TestRequireInitPackage(t *testing.T) {
	p, _ := diskProject(t, map[string]string{
		"tlua.toml": "[package]\nname = \"app\"\n\n[dependencies.\"example.com/greet\"]\nversion = \"v1.0.0\"\n",
		"tlua_modules/example.com/greet/init.tl": `local M = {}
function M.hello(name: string): string return "hi " .. name end
return M
`,
		"main.tl": `import G from "example.com.greet"
local s: string = G.hello("x")
print(s)
`,
	})
	p.Check()
	if p.HasErrors() {
		t.Fatalf("package-root require should resolve init.tl:\n%s", p.FormatDiags())
	}
}

func TestRequireShortName(t *testing.T) {
	p, _ := diskProject(t, map[string]string{
		"tlua.toml": "[package]\nname = \"app\"\n\n[dependencies.\"example.com/greet\"]\nversion = \"v1.0.0\"\n",
		"tlua_modules/example.com/greet/init.tl": `local M = {}
function M.hello(name: string): string return "hi " .. name end
return M
`,
		"main.tl": `import G from "greet"
local s: string = G.hello("x")
print(s)
`,
	})
	p.Check()
	if p.HasErrors() {
		t.Fatalf("unambiguous short name should resolve:\n%s", p.FormatDiags())
	}
}

func TestRequireShortAmbiguousStaysAny(t *testing.T) {
	p, _ := diskProject(t, map[string]string{
		"tlua.toml": "[package]\nname = \"app\"\n\n[dependencies.\"example.com/a/greet\"]\nversion = \"v1.0.0\"\n\n[dependencies.\"example.com/b/greet\"]\nversion = \"v1.0.0\"\n",
		"tlua_modules/example.com/a/greet/init.tl": `local M = {}
function M.hello(name: string): number return 1 end
return M
`,
		"main.tl": `import G from "greet"
local s: string = G.hello("x")
print(s)
`,
	})
	p.Check()
	if p.HasErrors() {
		t.Fatalf("ambiguous short must degrade to any:\n%s", p.FormatDiags())
	}
}

func TestRequireRelativeBeatsVendor(t *testing.T) {
	p, _ := diskProject(t, map[string]string{
		"sub.tl": `local M = {}
function M.k(): string return "rel" end
return M
`,
		"tlua_modules/sub/init.tl": `local M = {}
function M.k(): number return 1 end
return M
`,
		"main.tl": `import S from "sub"
local s: string = S.k()
print(s)
`,
	})
	p.Check()
	if p.HasErrors() {
		t.Fatalf("relative require must win over vendor:\n%s", p.FormatDiags())
	}
}
