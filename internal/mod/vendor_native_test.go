package mod

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCopyTlTreeIncludesNativeAssets(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()
	write := func(name, content string) {
		p := filepath.Join(src, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("init.tl", "return {}\n")
	write("types.d.tl", "type T = { x: number }\n")
	write("init.lua", "return {}\n")
	write("libfoo.so", "so-bytes")
	write("libfoo.dll", "dll-bytes")
	write("libfoo.dylib", "dylib-bytes")
	write("libfoo.a", "archive-bytes")
	write("foo.h", "int f();\n")
	write(".hidden.tl", "x\n")
	if err := copyTlTree(src, dst); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"init.tl", "types.d.tl", "init.lua", "libfoo.so", "libfoo.dll", "libfoo.dylib", "libfoo.a"} {
		if _, err := os.Stat(filepath.Join(dst, want)); err != nil {
			t.Fatalf("must vendor %s: %v", want, err)
		}
	}
	for _, skip := range []string{"foo.h", ".hidden.tl"} {
		if _, err := os.Stat(filepath.Join(dst, skip)); err == nil {
			t.Fatalf("must not vendor %s", skip)
		}
	}
}
