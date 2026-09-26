package compiler

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTempSrc(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) (string, error) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw), err
}

// TestParallelLoadDeterministic guards the worker pool: repeated parallel
// loads must equal each other file-for-file, diag-for-diag.
func TestParallelLoadDeterministic(t *testing.T) {
	dir := t.TempDir()
	cfg := testAmbientConfig(dir)
	for i := 0; i < 120; i++ {
		writeTempSrc(t, dir, fmt.Sprintf("f%03d.tl", i), fmt.Sprintf("local v%d: number = %d\nprint(v%d)\n", i, i, i))
	}
	writeTempSrc(t, dir, "bad.tl", "local x = \n")
	snap := func() (string, string) {
		p := NewProgram(cfg)
		if err := p.Load(); err != nil {
			t.Fatal(err)
		}
		var fb strings.Builder
		for _, sf := range p.Files {
			fb.WriteString(sf.Path)
			fb.WriteString("\x00")
		}
		var db strings.Builder
		for _, d := range p.Diags {
			db.WriteString(d.String())
			db.WriteString("\n")
		}
		return fb.String(), db.String()
	}
	f0, d0 := snap()
	for i := 0; i < 5; i++ {
		f, d := snap()
		if f != f0 || d != d0 {
			t.Fatalf("parallel load diverged on iteration %d", i)
		}
	}
	if got := strings.Count(f0, ".tl\x00"); got != 121 {
		t.Fatalf("want 121 files, got %d", got)
	}
}

// TestParallelDoForwardsPanic: worker panics surface in the caller (so
// watch safeCycle still converts them), carrying the first value.
func TestParallelDoForwardsPanic(t *testing.T) {
	defer func() {
		if r := recover(); r != "boom-7" {
			t.Fatalf("want boom-7, got %v", r)
		}
	}()
	parallelDo(64, func(i int) {
		if i == 7 {
			panic("boom-7")
		}
	})
	t.Fatalf("must have panicked")
}

// TestEmitSkipsIdenticalWrites: second identical build keeps mtimes.
func TestEmitSkipsIdenticalWrites(t *testing.T) {
	dir := t.TempDir()
	cfg := testAmbientConfig(dir)
	writeTempSrc(t, dir, "a.tl", "local x: number = 1\nprint(x)\n")
	p := NewProgram(cfg)
	if err := p.Load(); err != nil {
		t.Fatal(err)
	}
	p.Check()
	emitted, err := p.Emit()
	if err != nil {
		t.Fatal(err)
	}
	mt0 := mtimeOf(t, emitted[0])
	// No sleep: ns-resolution mtimes move on any rewrite on Linux; equal
	// means the write was skipped.
	p2 := NewProgram(cfg)
	if err := p2.Load(); err != nil {
		t.Fatal(err)
	}
	p2.Check()
	if _, err := p2.Emit(); err != nil {
		t.Fatal(err)
	}
	if mtimeOf(t, emitted[0]) != mt0 {
		t.Fatalf("identical rebuild must not touch mtime")
	}
	// Real change rewrites.
	writeTempSrc(t, dir, "a.tl", "local x: number = 2\nprint(x)\n")
	p3 := NewProgram(cfg)
	if err := p3.Load(); err != nil {
		t.Fatal(err)
	}
	p3.Check()
	emitted3, err := p3.Emit()
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := readFile(t, emitted3[0])
	if !strings.Contains(raw, "= 2") {
		t.Fatalf("changed source must rewrite:\n%s", raw)
	}
}
