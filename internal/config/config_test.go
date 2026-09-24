package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestTypeRootsDefault(t *testing.T) {
	c := Default(t.TempDir())
	roots := c.TypeRoots()
	if len(roots) != 2 {
		t.Fatalf("default typeRoots must be 2, got %v", roots)
	}
}

func TestIsAmbientIncluded(t *testing.T) {
	if !IsAmbientIncluded("types/foo.d.tl", nil) {
		t.Fatalf("empty allow must include all")
	}
	if !IsAmbientIncluded("/x/types/foo.d.tl", []string{"foo"}) {
		t.Fatalf("base match must include")
	}
	if !IsAmbientIncluded("/x/tlua_modules/@types/foo/index.d.tl", []string{"foo"}) {
		t.Fatalf("@types layout must include")
	}
	if IsAmbientIncluded("/x/types/foo.d.tl", []string{"bar"}) {
		t.Fatalf("non-listed must exclude")
	}
}

func TestListFilesIncludesTypeRootDecls(t *testing.T) {
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
	write("tlua.json", `{"compilerOptions": {"target": "5.1", "rootDir": ".", "outDir": "dist"}, "include": ["*.tl"], "exclude": ["dist"]}`)
	write("main.tl", "print(1)\n")
	write("types/foo.d.tl", "type Foo = { x: number }\n")
	cfg, err := Load(filepath.Join(dir, "tlua.json"))
	if err != nil {
		t.Fatal(err)
	}
	list, err := cfg.ListFiles()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, f := range list {
		if filepath.Base(f) == "foo.d.tl" {
			found = true
		}
	}
	if !found {
		t.Fatalf("typeRoots .d.tl must list even outside include, got %v", list)
	}
}

func TestListNativeAssets(t *testing.T) {
	dir := t.TempDir()
	write := func(name string) {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("tlua.json")
	write("main.tl")
	write("tlua_modules/a/init.lua")
	write("tlua_modules/a/lib.so")
	write("notes.txt")
	c := Default(dir)
	assets, err := c.ListNativeAssets()
	if err != nil {
		t.Fatal(err)
	}
	if len(assets) != 2 {
		t.Fatalf("want init.lua + lib.so, got %v", assets)
	}
}
