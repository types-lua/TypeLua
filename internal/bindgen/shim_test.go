package bindgen

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tlparser "github.com/typelua/typelua/internal/parser"
)

func mustParseShim(t *testing.T, src string) {
	t.Helper()
	if _, diags := tlparser.ParseFile("gen.tl", src); len(diags) > 0 {
		t.Fatalf("generated shim must parse:\n%s\n---\n%s", src, diags[0].Message)
	}
}

func TestDefaultSoName(t *testing.T) {
	for in, want := range map[string]string{
		"mylib.h":      "libmylib.so",
		"dir/libfoo.h": "libfoo.so",
		"foo.hpp":      "libfoo.so",
		"bar.HH":       "libbar.so",
		"plain":        "libplain.so",
		"a/b/calc.h":   "libcalc.so",
	} {
		if got := DefaultSoName(in); got != want {
			t.Fatalf("DefaultSoName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestGenerateShimBasic(t *testing.T) {
	header := "typedef struct { double x, y; } MyPoint;\nint my_add(int a, int b);\n"
	out := GenerateShim(header, "mylib.h", "CMyLib", "")
	mustParseShim(t, out)
	for _, want := range []string{
		"local ffi = require(\"ffi\")",
		"ffi.cdef[[",
		"typedef struct { double x, y; } MyPoint;",
		"libDir() .. \"libmylib.so\"",
		"as CMyLib",
		"return M",
		"local function libDir(): string",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in:\n%s", want, out)
		}
	}
}

func TestGenerateShimDeterministic(t *testing.T) {
	header := "int f(int x);\n"
	if a, b := GenerateShim(header, "f.h", "L", "libf.so"), GenerateShim(header, "f.h", "L", "libf.so"); a != b {
		t.Fatalf("shim generation must be deterministic:\n%s\n---\n%s", a, b)
	}
}

func TestGenerateShimGuardsBrackets(t *testing.T) {
	// A pathological body containing `]]` must not break the long string.
	header := "int x;\n"
	out := GenerateShim(header+"]]\n", "x.h", "L", "libx.so")
	mustParseShim(t, out)
	if !strings.Contains(out, "[=[") {
		t.Fatalf("bracket collision must step up long-string level:\n%s", out)
	}
}

func TestGenerateShimEmpty(t *testing.T) {
	out := GenerateShim("// nothing\n", "e.h", "L", "")
	mustParseShim(t, out)
	if !strings.Contains(out, "return M") {
		t.Fatalf("empty header still yields a loader:\n%s", out)
	}
}

// TestExampleCShimInSync guards the single-source-of-truth workflow: the
// committed examples/c contract + shim must equal fresh generator output,
// so editing mylib.h without re-running bindgen fails loudly instead of
// drifting silently.
func TestExampleCShimInSync(t *testing.T) {
	dir := filepath.Join("..", "..", "examples", "c")
	raw, err := os.ReadFile(filepath.Join(dir, "mylib.h"))
	if err != nil {
		t.Skipf("example header unavailable: %v", err)
	}
	wantDTL, err := os.ReadFile(filepath.Join(dir, "mylib.d.tl"))
	if err != nil {
		t.Fatalf("read committed contract: %v", err)
	}
	if got := Generate(string(raw), "CMyLib"); got != string(wantDTL) {
		t.Fatalf("examples/c/mylib.d.tl drifted; re-run bindgen (see mylib.h header)")
	}
	wantShim, err := os.ReadFile(filepath.Join(dir, "mylib.tl"))
	if err != nil {
		t.Fatalf("read committed shim: %v", err)
	}
	if got := GenerateShim(string(raw), "mylib.h", "CMyLib", ""); got != string(wantShim) {
		t.Fatalf("examples/c/mylib.tl drifted; re-run bindgen --shim (see mylib.h header)")
	}
}
