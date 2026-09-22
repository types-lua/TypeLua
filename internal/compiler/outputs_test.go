package compiler

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/typelua/typelua/internal/config"
)

func TestOutputForFileBasic(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Default(dir)
	dst, ok := OutputForFile(cfg, filepath.Join(dir, "a.tl"))
	if !ok {
		t.Fatalf("expected ok")
	}
	if want := filepath.Join(dir, "dist", "a.lua"); dst != want {
		t.Fatalf("got %q want %q", dst, want)
	}
}

func TestOutputForFileNestedAndRootDir(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Default(dir)
	cfg.CompilerOptions.RootDir = "src"
	cfg.CompilerOptions.OutDir = "build"
	dst, ok := OutputForFile(cfg, filepath.Join(dir, "src", "sub", "b.tl"))
	if !ok {
		t.Fatalf("expected ok")
	}
	if want := filepath.Join(dir, "build", "sub", "b.lua"); dst != want {
		t.Fatalf("got %q want %q", dst, want)
	}
}

func TestOutputForFileSkipsDecl(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Default(dir)
	if _, ok := OutputForFile(cfg, filepath.Join(dir, "lib.d.tl")); ok {
		t.Fatalf("decl files must not map to outputs")
	}
}

func TestOutputForFileMatchesEmit(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Default(dir)
	cfg.CompilerOptions.OutDir = dir
	cfg.CompilerOptions.RootDir = "."
	p := NewProgram(cfg)
	p.LoadSources(map[string]string{"a.tl": "local x: number = 1\n"})
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
	dst, ok := OutputForFile(cfg, "a.tl")
	if !ok {
		t.Fatalf("expected ok")
	}
	if emitted[0] != dst {
		t.Fatalf("Emit wrote %q but mapping says %q", emitted[0], dst)
	}
	if _, err := os.Stat(dst); err != nil {
		t.Fatalf("output missing: %v", err)
	}
}
