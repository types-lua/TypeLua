package checker

import (
	"testing"

	"github.com/typelua/typelua/internal/ast"
	"github.com/typelua/typelua/internal/diag"
	"github.com/typelua/typelua/internal/parser"
)

func ffiCodes(t *testing.T, src string) []diag.Code {
	t.Helper()
	f, pdiags := parser.ParseFile("test.tl", src)
	if len(pdiags) != 0 {
		t.Fatalf("parse errors: %v", pdiags[0].Message)
	}
	var _ = ast.File{}
	diags := CheckFile(f, DefaultOptions())
	var out []diag.Code
	for _, d := range diags {
		out = append(out, d.Code)
	}
	return out
}

func hasFFCode(codes []diag.Code, want diag.Code) bool {
	for _, c := range codes {
		if c == want {
			return true
		}
	}
	return false
}

func TestFFIRequirePrecise(t *testing.T) {
	if codes := ffiCodes(t, "local ffi = require(\"ffi\")\nffi.cdef[[ int add(int a, int b); ]]\n"); len(codes) != 0 {
		t.Fatalf("ffi basic use must pass, got %v", codes)
	}
}

func TestFFICdefArgChecking(t *testing.T) {
	codes := ffiCodes(t, "local ffi = require(\"ffi\")\nffi.cdef(123)\n")
	if !hasFFCode(codes, diag.ArgTypeMismatch) {
		t.Fatalf("cdef(123) must be arg mismatch, got %v", codes)
	}
}

func TestFFICdataCtypeDistinct(t *testing.T) {
	if codes := ffiCodes(t, "local ffi = require(\"ffi\")\nlocal x: cdata = ffi.new(\"int[?]\", 4)\nlocal y: ctype = ffi.typeof(\"int\")\nlocal s: string = ffi.string(x)\nprint(x, y, s)\n"); len(codes) != 0 {
		t.Fatalf("cdata/ctype flow must pass, got %v", codes)
	}
	codes := ffiCodes(t, "local ffi = require(\"ffi\")\nlocal bad: number = ffi.new(\"int\")\n")
	if !hasFFCode(codes, diag.TypeNotAssignable) {
		t.Fatalf("cdata -> number must error, got %v", codes)
	}
}

func TestFFICastFlow(t *testing.T) {
	if codes := ffiCodes(t, "local ffi = require(\"ffi\")\ntype CLib = { add(a: integer, b: integer): integer }\nlocal C = ffi.load(\"mylib\") as CLib\nlocal n: number = C.add(1, 2)\nprint(n)\n"); len(codes) != 0 {
		t.Fatalf("ffi.load as CLib must pass, got %v", codes)
	}
	codes := ffiCodes(t, "local ffi = require(\"ffi\")\ntype CLib = { add(a: integer, b: integer): integer }\nlocal C = ffi.load(\"mylib\") as CLib\nlocal s: string = C.add(1, 2)\n")
	if !hasFFCode(codes, diag.TypeNotAssignable) {
		t.Fatalf("CLib.add returns integer, string assign must error, got %v", codes)
	}
}

func TestFFIShadowedRequireStaysAny(t *testing.T) {
	// A user local named require shadows the builtin: ffi special-case must
	// not apply (classic any fallback, no false precise errors).
	codes := ffiCodes(t, "local require = function(x: string): string return x end\nlocal v = require(\"ffi\")\nlocal s: string = v\nprint(s)\n")
	if len(codes) != 0 {
		t.Fatalf("shadowed require must stay permissive, got %v", codes)
	}
}

func TestCdataPrimitiveResolves(t *testing.T) {
	if codes := ffiCodes(t, "local ffi = require(\"ffi\")\nlocal x: cdata = ffi.new(\"int\")\nlocal y: ctype = ffi.typeof(\"int\")\nprint(x, y)\n"); len(codes) != 0 {
		t.Fatalf("cdata/ctype annotations must resolve, got %v", codes)
	}
}
