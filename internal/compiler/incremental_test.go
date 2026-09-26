package compiler

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/typelua/typelua/internal/config"
	"github.com/typelua/typelua/internal/watch"
)

// sproj builds a temp-dir project exercising ambient decls, requires,
// a require cycle and a missing module.
func sproj(t *testing.T) (string, *config.Config) {
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
	write("tlua.json", `{"compilerOptions": {"target": "5.4", "strict": true, "rootDir": ".", "outDir": "dist"}, "include": ["**/*.tl"], "exclude": ["dist"]}`)
	write("types.d.tl", "type Point = { x: number, y: number }\ndeclare function dist(a: Point, b: Point): number\n")
	write("lib.tl", "local M = {}\nfunction M.add(a: number, b: number): number return a + b end\nreturn M\n")
	write("main.tl", "import L from \"lib\"\nlocal p: Point = { x = 1, y = 2 }\nprint(dist(p, p))\nprint(L.add(1, 2))\nlocal M = require(\"nope.missing\")\nprint(M.anything)\n")
	write("a.tl", "local B = require(\"b\")\nlocal M = {}\nfunction M.fa(): number return 1 end\nreturn M\n")
	write("b.tl", "local A = require(\"a\")\nlocal M = {}\nfunction M.fb(): number return 2 end\nreturn M\n")
	cfg, err := config.Load(filepath.Join(dir, "tlua.json"))
	if err != nil {
		t.Fatal(err)
	}
	return dir, cfg
}

// freshOutputs runs a one-shot Program and returns diags + emitted bytes.
func freshOutputs(t *testing.T, cfg *config.Config) (string, map[string]string) {
	t.Helper()
	p := NewProgram(cfg)
	if err := p.Load(); err != nil {
		t.Fatal(err)
	}
	p.Check()
	out := map[string]string{}
	if _, err := p.Emit(); err != nil {
		t.Fatalf("emit: %v\n%s", err, p.FormatDiags())
	}
	for _, sf := range p.Files {
		if sf.IsDecl {
			continue
		}
		dst, ok := OutputForFile(cfg, sf.Path)
		if !ok {
			continue
		}
		raw, err := os.ReadFile(dst)
		if err != nil {
			t.Fatalf("read %s: %v", dst, err)
		}
		rel, _ := filepath.Rel(cfg.Dir, dst)
		out[filepath.ToSlash(rel)] = string(raw)
	}
	return p.FormatDiags(), out
}

func sameOutputs(a, b map[string]string) (string, bool) {
	if len(a) != len(b) {
		return "size", false
	}
	for k, v := range a {
		if w, ok := b[k]; !ok || w != v {
			return k, false
		}
	}
	return "", true
}

// TestSessionFullMatchesProgram is the equivalence anchor: a full session
// sync must equal a fresh one-shot Program in diags and emitted bytes
// (ambient + cycle + missing-module edges included).
func TestSessionFullMatchesProgram(t *testing.T) {
	_, cfg := sproj(t)
	wantDiags, wantOut := freshOutputs(t, cfg)
	sess := NewSession(cfg)
	if _, err := sess.Sync(true, nil, true); err != nil {
		t.Fatalf("sync: %v\n%s", err, sess.FormatDiags())
	}
	if got := sess.FormatDiags(); got != wantDiags {
		t.Fatalf("diags differ:\n--- session ---\n%s\n--- program ---\n%s", got, wantDiags)
	}
	sessOut := map[string]string{}
	for _, sf := range sess.Files() {
		if sf.IsDecl {
			continue
		}
		dst, ok := OutputForFile(cfg, sf.Path)
		if !ok {
			continue
		}
		raw, err := os.ReadFile(dst)
		if err != nil {
			t.Fatalf("read %s: %v", dst, err)
		}
		rel, _ := filepath.Rel(cfg.Dir, dst)
		sessOut[filepath.ToSlash(rel)] = string(raw)
	}
	if k, ok := sameOutputs(wantOut, sessOut); !ok {
		t.Fatalf("emitted bytes differ at %q", k)
	}
}

// absChange builds a watch.Change for tests (watch reports abs-clean paths).
func absChange(t *testing.T, dir, name string, kind watch.ChangeKind) watch.Change {
	t.Helper()
	a, err := filepath.Abs(filepath.Join(dir, filepath.FromSlash(name)))
	if err != nil {
		t.Fatal(err)
	}
	return watch.Change{Path: filepath.Clean(a), Kind: kind}
}

func rewrite(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, filepath.FromSlash(name)), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestSessionIncrementalSubset proves steady cycles recheck subsets: a leaf
// edit rechecks only the leaf, a lib edit rechecks lib + dependents.
func TestSessionIncrementalSubset(t *testing.T) {
	dir, cfg := sproj(t)
	sess := NewSession(cfg)
	if _, err := sess.Sync(true, nil, true); err != nil {
		t.Fatal(err)
	}
	// Leaf edit: only main.tl.
	rewrite(t, dir, "main.tl", "import L from \"lib\"\nlocal p: Point = { x = 1, y = 2 }\nprint(dist(p, p))\nprint(L.add(3, 4))\nlocal M = require(\"nope.missing\")\nprint(M.anything)\n")
	apath := func(n string) string {
		a, _ := filepath.Abs(filepath.Join(dir, n))
		return filepath.Clean(a)
	}
	if _, err := sess.Sync(false, []watch.Change{absChange(t, dir, "main.tl", watch.Modified)}, true); err != nil {
		t.Fatal(err)
	}
	if got := sess.LastChecked(); len(got) != 1 || got[0] != apath("main.tl") {
		t.Fatalf("leaf edit must recheck [main.tl], got %v", got)
	}
	// Lib edit: lib + its dependent main (cycle pair untouched).
	rewrite(t, dir, "lib.tl", "local M = {}\nfunction M.add(a: number, b: number): number return a + b + 1 end\nreturn M\n")
	if _, err := sess.Sync(false, []watch.Change{absChange(t, dir, "lib.tl", watch.Modified)}, true); err != nil {
		t.Fatal(err)
	}
	got := sess.LastChecked()
	want := map[string]bool{apath("lib.tl"): true, apath("main.tl"): true}
	if len(got) != 2 || !want[got[0]] || !want[got[1]] {
		t.Fatalf("lib edit must recheck [lib.tl main.tl], got %v", got)
	}
	// Equivalence after incremental steps.
	p := NewProgram(cfg)
	if err := p.Load(); err != nil {
		t.Fatal(err)
	}
	p.Check()
	if p.FormatDiags() != sess.FormatDiags() {
		t.Fatalf("incremental diags diverged:\n--- session ---\n%s\n--- fresh ---\n%s", sess.FormatDiags(), p.FormatDiags())
	}
}

// TestSessionCycleRechecksWholeSCC: editing one cycle member rechecks both
// (deterministic any-degradation needs the complete SCC).
func TestSessionCycleRechecksWholeSCC(t *testing.T) {
	dir, cfg := sproj(t)
	sess := NewSession(cfg)
	if _, err := sess.Sync(true, nil, true); err != nil {
		t.Fatal(err)
	}
	rewrite(t, dir, "b.tl", "local A = require(\"a\")\nlocal M = {}\nfunction M.fb(): number return 3 end\nreturn M\n")
	if _, err := sess.Sync(false, []watch.Change{absChange(t, dir, "b.tl", watch.Modified)}, true); err != nil {
		t.Fatal(err)
	}
	apath := func(n string) string {
		a, _ := filepath.Abs(filepath.Join(dir, n))
		return filepath.Clean(a)
	}
	got := sess.LastChecked()
	want := map[string]bool{apath("a.tl"): true, apath("b.tl"): true}
	if len(got) != 2 || !want[got[0]] || !want[got[1]] {
		t.Fatalf("cycle edit must recheck [a.tl b.tl], got %v", got)
	}
	if sess.HasErrors() {
		t.Fatalf("cycle must stay any-degraded:\n%s", sess.FormatDiags())
	}
}

// TestSessionTouchNoop: same bytes rewritten (mtime-only change) heals to a
// no-op without touching outputs.
func TestSessionTouchNoop(t *testing.T) {
	dir, cfg := sproj(t)
	sess := NewSession(cfg)
	if _, err := sess.Sync(true, nil, true); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(dir, "dist", "main.lua")
	before, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	mt0 := mtimeOf(t, dst)
	rewrite(t, dir, "main.tl", "import L from \"lib\"\nlocal p: Point = { x = 1, y = 2 }\nprint(dist(p, p))\nprint(L.add(1, 2))\nlocal M = require(\"nope.missing\")\nprint(M.anything)\n")
	if _, err := sess.Sync(false, []watch.Change{absChange(t, dir, "main.tl", watch.Modified)}, true); err != nil {
		t.Fatal(err)
	}
	if len(sess.LastChecked()) != 0 {
		t.Fatalf("identical bytes must not recheck, got %v", sess.LastChecked())
	}
	if mtimeOf(t, dst) != mt0 {
		t.Fatalf("output mtime must not move on noop")
	}
	after, _ := os.ReadFile(dst)
	if string(before) != string(after) {
		t.Fatalf("output bytes must not change on noop")
	}
}

func mtimeOf(t *testing.T, p string) int64 {
	t.Helper()
	st, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	return st.ModTime().UnixNano()
}

// TestSessionAddRemove: added files check alone; removing a lib rechecks
// dependents with any-fallback and stays crash-free.
func TestSessionAddRemove(t *testing.T) {
	dir, cfg := sproj(t)
	sess := NewSession(cfg)
	if _, err := sess.Sync(true, nil, true); err != nil {
		t.Fatal(err)
	}
	apath := func(n string) string {
		a, _ := filepath.Abs(filepath.Join(dir, n))
		return filepath.Clean(a)
	}
	// Add an independent file: only it checks.
	rewrite(t, dir, "new.tl", "local z: string = \"new\"\nprint(z)\n")
	if _, err := sess.Sync(false, []watch.Change{absChange(t, dir, "new.tl", watch.Added)}, true); err != nil {
		t.Fatal(err)
	}
	if got := sess.LastChecked(); len(got) != 1 || got[0] != apath("new.tl") {
		t.Fatalf("added file must check alone, got %v", got)
	}
	// Remove lib.tl: main.tl rechecks (any-fallback), no crash.
	if err := os.Remove(filepath.Join(dir, "lib.tl")); err != nil {
		t.Fatal(err)
	}
	if _, err := sess.Sync(false, []watch.Change{absChange(t, dir, "lib.tl", watch.Removed)}, true); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, k := range sess.LastChecked() {
		if k == apath("main.tl") {
			found = true
		}
	}
	if !found {
		t.Fatalf("dependent must recheck after removal, got %v", sess.LastChecked())
	}
	p := NewProgram(cfg)
	if err := p.Load(); err != nil {
		t.Fatal(err)
	}
	p.Check()
	if p.FormatDiags() != sess.FormatDiags() {
		t.Fatalf("post-removal diags diverged:\n--- session ---\n%s\n--- fresh ---\n%s", sess.FormatDiags(), p.FormatDiags())
	}
}

// TestSessionAmbientEdit rechecks everything but rewrites nothing when only
// a decl changed (ambient affects checking, never emission).
func TestSessionAmbientEdit(t *testing.T) {
	dir, cfg := sproj(t)
	sess := NewSession(cfg)
	if _, err := sess.Sync(true, nil, true); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(dir, "dist", "main.lua")
	mt0 := mtimeOf(t, dst)
	// Comment-only decl edit: ambient rebuilds, all recheck, outputs equal.
	rewrite(t, dir, "types.d.tl", "-- touched\ntype Point = { x: number, y: number }\ndeclare function dist(a: Point, b: Point): number\n")
	if _, err := sess.Sync(false, []watch.Change{absChange(t, dir, "types.d.tl", watch.Modified)}, true); err != nil {
		t.Fatal(err)
	}
	if len(sess.LastChecked()) != 4 { // lib, main, a, b (decls never standalone-check)
		t.Fatalf("ambient edit must recheck all program files, got %v", sess.LastChecked())
	}
	if mtimeOf(t, dst) != mt0 {
		t.Fatalf("decl-only change must not rewrite outputs")
	}
	if sess.HasErrors() {
		t.Fatalf("unexpected errors:\n%s", sess.FormatDiags())
	}
}

// TestSessionResolutionFlip: adding a same-dir file shadows a vendor
// target; the dependent rechecks via the resolution diff.
func TestSessionResolutionFlip(t *testing.T) {
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
	write("tlua.json", `{"compilerOptions": {"target": "5.4", "strict": true, "rootDir": ".", "outDir": "dist"}, "include": ["**/*.tl"], "exclude": ["dist"]}`)
	write("main.tl", "local U = require(\"u\")\nprint(U.hello(\"x\"))\n")
	write("tlua_modules/u.tl", "local M = {}\nfunction M.hello(name: string): string return \"hi \" .. name end\nreturn M\n")
	cfg, err := config.Load(filepath.Join(dir, "tlua.json"))
	if err != nil {
		t.Fatal(err)
	}
	sess := NewSession(cfg)
	if _, err := sess.Sync(true, nil, true); err != nil {
		t.Fatal(err)
	}
	if sess.HasErrors() {
		t.Fatalf("vendor resolve must check clean:\n%s", sess.FormatDiags())
	}
	// Local u.tl shadows the vendor one (same-dir base wins).
	write("u.tl", "local M = {}\nfunction M.hello(name: string): string return \"yo \" .. name end\nreturn M\n")
	apath := func(n string) string {
		a, _ := filepath.Abs(filepath.Join(dir, n))
		return filepath.Clean(a)
	}
	if _, err := sess.Sync(false, []watch.Change{absChange(t, dir, "u.tl", watch.Added)}, true); err != nil {
		t.Fatal(err)
	}
	got := sess.LastChecked()
	want := map[string]bool{apath("main.tl"): true, apath("u.tl"): true}
	if len(got) != 2 || !want[got[0]] || !want[got[1]] {
		t.Fatalf("shadowing flip must recheck exactly [main.tl u.tl], got %v", got)
	}
	if sess.HasErrors() {
		t.Fatalf("shadowed resolve must stay clean:\n%s", sess.FormatDiags())
	}
}
