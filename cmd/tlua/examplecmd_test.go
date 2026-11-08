package main

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestDiscoverExamples(t *testing.T) {
	dir := t.TempDir()
	writeProject(t, dir)
	writeFile(t, filepath.Join(dir, "examples", "basic", "main.tl"), "--- Basic tour of the API.\nprint(\"basic\")\n")
	writeFile(t, filepath.Join(dir, "examples", "single.tl"), "--- One-file example.\nprint(\"single\")\n")
	writeFile(t, filepath.Join(dir, "examples", "skip.d.tl"), "declare type X = number")

	examples := discoverExamples(dir)
	if len(examples) != 2 {
		t.Fatalf("got %d examples, want 2: %+v", len(examples), examples)
	}
	if examples[0].Name != "basic" || examples[1].Name != "single" {
		t.Fatalf("names not sorted: %+v", examples)
	}
	if examples[0].Desc != "Basic tour of the API." {
		t.Fatalf("description = %q", examples[0].Desc)
	}
	if !strings.HasSuffix(filepath.ToSlash(examples[0].Path), "examples/basic/main.tl") {
		t.Fatalf("directory example should resolve main.tl, got %s", examples[0].Path)
	}
}

func TestCmdExampleListRunAndUnknown(t *testing.T) {
	dir := t.TempDir()
	writeProject(t, dir)
	writeFile(t, filepath.Join(dir, "examples", "hello", "main.tl"),
		"--- Prints a greeting.\nprint(\"hello from example\")\n")
	t.Chdir(dir)

	if err := cmdExample(nil); err != nil {
		t.Fatalf("listing failed: %v", err)
	}
	if err := cmdExample([]string{"hello"}); err != nil {
		t.Fatalf("running example failed: %v", err)
	}
	if err := cmdExample([]string{"nope"}); err == nil {
		t.Fatal("unknown example should fail")
	}
}
