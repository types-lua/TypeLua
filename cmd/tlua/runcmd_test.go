package main

import (
	"path/filepath"
	"testing"
)

func TestRunPassesArgsWithoutSeparator(t *testing.T) {
	// `tlua run main.tl -- a b` is parsed as rest = ["main.tl","--","a","b"];
	// runEntry strips the separator before handing args to the script.
	dir := t.TempDir()
	writeProject(t, dir)
	writeFile(t, filepath.Join(dir, "main.tl"), "print(arg[1], arg[2])\n")
	t.Chdir(dir)
	if err := cmdRun([]string{"main.tl", "--", "alpha", "beta"}); err != nil {
		t.Fatalf("run failed: %v", err)
	}
}
