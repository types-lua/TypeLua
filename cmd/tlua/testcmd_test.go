package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeProject(t *testing.T, dir string) {
	t.Helper()
	writeFile(t, filepath.Join(dir, "tlua.json"), `{
  "compilerOptions": {"target": "5.4", "strict": true, "strictNilChecks": true, "noImplicitAny": true, "rootDir": ".", "outDir": "dist"},
  "include": ["**/*.tl"],
  "exclude": ["dist"]
}`)
}

func TestDiscoverTests(t *testing.T) {
	dir := t.TempDir()
	writeProject(t, dir)
	writeFile(t, filepath.Join(dir, "tests", "a.tl"), "return {}")
	writeFile(t, filepath.Join(dir, "nested", "b_test.tl"), "return {}")
	writeFile(t, filepath.Join(dir, "c.test.tl"), "return {}")
	writeFile(t, filepath.Join(dir, "main.tl"), "print(1)")
	writeFile(t, filepath.Join(dir, "tests", "types.d.tl"), "declare type X = number")
	writeFile(t, filepath.Join(dir, "tlua_modules", "pkg", "ignored_test.tl"), "return {}")

	files, err := discoverTests(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	var rels []string
	for _, f := range files {
		rel, _ := filepath.Rel(dir, f)
		rels = append(rels, filepath.ToSlash(rel))
	}
	got := strings.Join(rels, ",")
	want := "c.test.tl,nested/b_test.tl,tests/a.tl"
	if got != want {
		t.Fatalf("discovered %q, want %q", got, want)
	}
}

func TestBuildTestRunnerUsesDottedModules(t *testing.T) {
	root := "/proj"
	src := buildTestRunner(root, []string{"/proj/tests/a_test.tl"}, "add", true)
	for _, want := range []string{
		`require("tests.a_test")`,
		`T.run("tests.a_test", __m)`,
		`T.configure("add", true)`,
	} {
		if !strings.Contains(src, want) {
			t.Fatalf("runner missing %q:\n%s", want, src)
		}
	}
}

func TestCmdTestPassAndFail(t *testing.T) {
	dir := t.TempDir()
	writeProject(t, dir)
	writeFile(t, filepath.Join(dir, "tests", "math_test.tl"), `import T from "tlua_test"
return {
  ["adds"] = function(): void
    T.eq(1 + 1, 2)
  end,
  ["compares"] = function(): void
    T.ok("a" < "b")
  end,
}
`)
	t.Chdir(dir)
	if err := cmdTest(nil); err != nil {
		t.Fatalf("passing suite reported an error: %v", err)
	}

	writeFile(t, filepath.Join(dir, "tests", "broken_test.tl"), `import T from "tlua_test"
return {
  ["fails"] = function(): void
    T.eq(1 + 1, 3)
  end,
}
`)
	if err := cmdTest(nil); err == nil {
		t.Fatal("failing suite should report an error")
	}
	if err := cmdTest([]string{"--filter", "compares"}); err != nil {
		t.Fatalf("filter should skip the broken case: %v", err)
	}
}

func TestCmdTestNoFiles(t *testing.T) {
	dir := t.TempDir()
	writeProject(t, dir)
	t.Chdir(dir)
	err := cmdTest(nil)
	if err == nil || !strings.Contains(err.Error(), "no test files") {
		t.Fatalf("want no-test-files error, got %v", err)
	}
}
