package compiler

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/typelua/typelua/internal/config"
	"github.com/typelua/typelua/internal/diag"
)

func writeProjectFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func newTestProgram(t *testing.T, dir string) *Program {
	t.Helper()
	cfg := config.Default(dir)
	cfg.Include = []string{"**/*.tl"}
	p := NewProgram(cfg)
	if err := p.Load(); err != nil {
		t.Fatal(err)
	}
	return p
}

func hasDiagCode(p *Program, code diag.Code) bool {
	for _, d := range p.Diags {
		if d.Code == code {
			return true
		}
	}
	return false
}

func TestRelativeImportResolvesAndEmitsModuleName(t *testing.T) {
	dir := t.TempDir()
	writeProjectFile(t, filepath.Join(dir, "lib", "codec.tl"), "local M = { add = function(a: number, b: number): number return a + b end }\nreturn M\n")
	writeProjectFile(t, filepath.Join(dir, "lib", "init.tl"), "import codec from \"./codec\"\nreturn { add = codec.add }\n")
	writeProjectFile(t, filepath.Join(dir, "main.tl"), "import L from \"./lib\"\nprint(L.add(1, 2))\n")

	p := newTestProgram(t, dir)
	p.Check()
	if p.HasErrors() {
		t.Fatalf("unexpected diagnostics: %s", p.FormatDiags())
	}
	out := p.EmitToString()
	if got := out[filepath.Join(dir, "lib", "init.tl")]; !strings.Contains(got, `require("lib.codec")`) {
		t.Fatalf("relative import not rewritten:\n%s", got)
	}
	if got := out[filepath.Join(dir, "main.tl")]; !strings.Contains(got, `require("lib")`) {
		t.Fatalf("package directory import should canonicalize to lib:\n%s", got)
	}
}

func TestRelativeImportVendoredUsesVendorRoot(t *testing.T) {
	dir := t.TempDir()
	pkg := filepath.Join(dir, "tlua_modules", "example.com", "pkg")
	writeProjectFile(t, filepath.Join(pkg, "codec.tl"), "return { twice = function(n: number): number return n * 2 end }\n")
	writeProjectFile(t, filepath.Join(pkg, "init.tl"), "import codec from \"./codec\"\nreturn { twice = codec.twice }\n")
	writeProjectFile(t, filepath.Join(dir, "main.tl"), "import P from \"example.com.pkg\"\nprint(P.twice(21))\n")

	p := newTestProgram(t, dir)
	p.Check()
	if p.HasErrors() {
		t.Fatalf("unexpected diagnostics: %s", p.FormatDiags())
	}
	out := p.EmitToString()
	if got := out[filepath.Join(pkg, "init.tl")]; !strings.Contains(got, `require("example.com.pkg.codec")`) {
		t.Fatalf("vendored relative import should use the vendor-root name:\n%s", got)
	}
}

func TestRelativeImportErrors(t *testing.T) {
	cases := []struct {
		name  string
		files map[string]string
	}{
		{
			name:  "missing",
			files: map[string]string{"main.tl": "import x from \"./nope\"\nprint(x)\n"},
		},
		{
			name: "relative require",
			files: map[string]string{
				"main.tl": "local x = require(\"./codec\")\nprint(x)\n",
			},
		},
		{
			name: "ambiguous module name",
			files: map[string]string{
				"a.b.tl":  "return { v = 1 }\n",
				"a/b.tl":  "return { v = 2 }\n",
				"main.tl": "import x from \"./a.b\"\nprint(x)\n",
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			for name, content := range tc.files {
				writeProjectFile(t, filepath.Join(dir, filepath.FromSlash(name)), content)
			}
			p := newTestProgram(t, dir)
			p.Check()
			if !hasDiagCode(p, diag.RelativeImportInvalid) {
				t.Fatalf("expected TL4002, got:\n%s", p.FormatDiags())
			}
		})
	}
}

func TestRelativeImportDirectoryAndInitCanonicalization(t *testing.T) {
	dir := t.TempDir()
	writeProjectFile(t, filepath.Join(dir, "pkg", "sub", "init.tl"), "return { v = 7 }\n")
	writeProjectFile(t, filepath.Join(dir, "pkg", "init.tl"), "import S from \"./sub\"\nreturn { v = S.v }\n")
	writeProjectFile(t, filepath.Join(dir, "main.tl"), "import P from \"./pkg\"\nprint(P.v)\n")

	p := newTestProgram(t, dir)
	p.Check()
	if p.HasErrors() {
		t.Fatalf("unexpected diagnostics: %s", p.FormatDiags())
	}
	out := p.EmitToString()
	if got := out[filepath.Join(dir, "pkg", "init.tl")]; !strings.Contains(got, `require("pkg.sub")`) {
		t.Fatalf("directory import should canonicalize init:\n%s", got)
	}
}
