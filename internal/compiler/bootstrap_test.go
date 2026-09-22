package compiler

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/typelua/typelua/internal/config"
	"github.com/typelua/typelua/internal/emitter"
)

func TestCandidateRels(t *testing.T) {
	got := candidateRels("example.com.greet")
	want := []string{
		filepath.FromSlash("example/com/greet"),
		filepath.FromSlash("example.com/greet"),
		"example.com.greet",
	}
	if len(got) != len(want) {
		t.Fatalf("got %q", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %q want %q", got, want)
		}
	}
	if got := candidateRels("greet"); len(got) != 1 || got[0] != "greet" {
		t.Fatalf("single segment: %q", got)
	}
}

func vendorProject(t *testing.T, main string) (string, *Program) {
	t.Helper()
	dir := t.TempDir()
	write := func(name, content string) {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("tlua.json", `{"compilerOptions": {"target": "5.4", "strict": true, "rootDir": ".", "outDir": "dist"},
"include": ["**/*.tl"], "exclude": ["dist"]}`)
	write("tlua_modules/example.com/greet/init.tl", `local M = {}
function M.hello(name: string): string return "hi " .. name end
return M
`)
	write("main.tl", main)
	cfg, err := config.Load(filepath.Join(dir, "tlua.json"))
	if err != nil {
		t.Fatal(err)
	}
	p := NewProgram(cfg)
	if err := p.Load(); err != nil {
		t.Fatal(err)
	}
	return dir, p
}

func TestRequireDottedVendorResolves(t *testing.T) {
	_, p := vendorProject(t, `import G from "example.com.greet"
local s: string = G.hello("x")
print(s)
`)
	p.Check()
	if p.HasErrors() {
		t.Fatalf("dotted vendor require should resolve:\n%s", p.FormatDiags())
	}
}

func TestRequireDottedVendorTyped(t *testing.T) {
	// Proves the import is really resolved (not any): arg mismatch errors.
	_, p := vendorProject(t, `import G from "example.com.greet"
local s: string = G.hello(123)
print(s)
`)
	p.Check()
	if !p.HasErrors() {
		t.Fatalf("typed vendor mismatch must error (import resolved to any?)")
	}
}

func TestPreludeApplied(t *testing.T) {
	_, p := vendorProject(t, `import G from "example.com.greet"
print(G.hello("tlua"))
`)
	p.Check()
	if p.HasErrors() {
		t.Fatalf("diags:\n%s", p.FormatDiags())
	}
	emitted, err := p.Emit()
	if err != nil {
		t.Fatal(err)
	}
	if len(emitted) != 2 {
		t.Fatalf("emitted: %v", emitted)
	}
	for _, dst := range emitted {
		raw, err := os.ReadFile(dst)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(string(raw), "if not __tl_modpath_done then\n") {
			t.Fatalf("%s missing prelude:\n%.200s", dst, raw)
		}
	}
}

func TestPreludeSkippedWithoutRequires(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Default(dir)
	cfg.CompilerOptions.RootDir = "."
	cfg.CompilerOptions.OutDir = filepath.Join(dir, "dist")
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
	raw, err := os.ReadFile(emitted[0])
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "__tl_modpath_done") {
		t.Fatalf("prelude must be skipped without requires:\n%s", raw)
	}
}

func findLua() string {
	for _, c := range []string{"lua5.4", "lua", "luajit"} {
		if p, err := exec.LookPath(c); err == nil {
			return p
		}
	}
	return ""
}

func TestPreludeRunsUnderRealLua(t *testing.T) {
	lua := findLua()
	if lua == "" {
		t.Skip("no lua interpreter on PATH")
	}
	dir, p := vendorProject(t, `import G from "example.com.greet"
print(G.hello("tlua"))
`)
	p.Check()
	if p.HasErrors() {
		t.Fatalf("diags:\n%s", p.FormatDiags())
	}
	if _, err := p.Emit(); err != nil {
		t.Fatal(err)
	}
	// From the project root with a relative script path (user's case).
	cmd := exec.Command(lua, "dist/main.lua")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("lua failed: %v\n%s", err, out)
	}
	if string(out) != "hi tlua\n" {
		t.Fatalf("got %q", out)
	}
	// From another cwd with an absolute script path.
	cmd = exec.Command(lua, filepath.Join(dir, "dist", "main.lua"))
	cmd.Dir = t.TempDir()
	out, err = cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("lua abs failed: %v\n%s", err, out)
	}
	if string(out) != "hi tlua\n" {
		t.Fatalf("abs: got %q", out)
	}
}

// --- source-map shift ---

const b64 = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"

func decodeSeg(seg string) []int {
	var out []int
	val, shift := 0, 0
	flush := func() {
		v := val >> 1
		if val&1 != 0 {
			v = -v
		}
		out = append(out, v)
		val, shift = 0, 0
	}
	for _, r := range seg {
		d := strings.IndexRune(b64, r)
		if d < 0 {
			continue
		}
		val |= (d & 31) << shift
		shift += 5
		if d&32 == 0 {
			flush()
		}
	}
	return out
}

type genPos struct {
	line, col, src, srcLine, srcCol int
}

func decodeMappings(s string) []genPos {
	var out []genPos
	line := 1
	prev := [4]int{}
	for _, group := range strings.Split(s, ";") {
		if group != "" {
			for _, seg := range strings.Split(group, ",") {
				vals := decodeSeg(seg)
				for i, v := range vals {
					if i < 4 {
						prev[i] += v
					}
				}
				out = append(out, genPos{line, prev[0], prev[1], prev[2], prev[3]})
			}
		}
		line++
	}
	return out
}

func TestPreludeSourceMapShift(t *testing.T) {
	dir, p := vendorProject(t, `import G from "example.com.greet"
print(G.hello("tlua"))
`)
	p.Cfg.CompilerOptions.SourceMap = true
	p.Check()
	if p.HasErrors() {
		t.Fatalf("diags:\n%s", p.FormatDiags())
	}
	var mainFile *SourceFile
	for _, sf := range p.Files {
		if strings.HasSuffix(sf.Path, "main.tl") {
			mainFile = sf
		}
	}
	if mainFile == nil {
		t.Fatalf("files: %v", p.Files)
	}
	rawOut, rawMaps := emitter.EmitFileWithMap(mainFile.File, emitter.Options{Target: p.Cfg.Target()})
	if _, err := p.Emit(); err != nil {
		t.Fatal(err)
	}
	dst, ok := OutputForFile(p.Cfg, mainFile.Path)
	if !ok {
		t.Fatal("no output mapping")
	}
	raw, err := os.ReadFile(dst + ".map")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Mappings string `json:"mappings"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	got := decodeMappings(doc.Mappings)
	if len(got) != len(rawMaps) {
		t.Fatalf("mapping count changed: %d vs %d", len(got), len(rawMaps))
	}
	shift := preludeLines()
	for i := range got {
		if got[i].line != rawMaps[i].GenLine+shift {
			t.Fatalf("seg %d gen line %d, want %d", i, got[i].line, rawMaps[i].GenLine+shift)
		}
		if got[i].srcLine != rawMaps[i].SrcLine || got[i].srcCol != rawMaps[i].SrcCol {
			t.Fatalf("seg %d src moved: %+v vs %+v", i, got[i], rawMaps[i])
		}
	}
	_ = dir
	_ = rawOut
}
