package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/typelua/typelua/internal/config"
)

func TestPruneStaleOutputs(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "dist")
	mustWrite := func(p, c string) {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	srcA := filepath.Join(dir, "a.tl")
	srcB := filepath.Join(dir, "b.tl")
	dstA := filepath.Join(out, "a.lua")
	dstB := filepath.Join(out, "b.lua")
	mustWrite(dstA, "-- stale\n")
	mustWrite(dstA+".map", "{}\n")
	mustWrite(dstB, "-- live\n")
	mustWrite(filepath.Join(out, "notes.txt"), "user file\n")

	prev := map[string]string{srcA: dstA, srcB: dstB}
	cur := map[string]bool{absPath(srcB): true}
	deleted := pruneStaleOutputs(prev, cur)
	if len(deleted) != 2 {
		t.Fatalf("expected a.lua + map removed, got %v", deleted)
	}
	if _, err := os.Stat(dstA); !os.IsNotExist(err) {
		t.Fatalf("stale output not removed: %v", err)
	}
	if _, err := os.Stat(dstA + ".map"); !os.IsNotExist(err) {
		t.Fatalf("stale map not removed: %v", err)
	}
	if _, err := os.Stat(dstB); err != nil {
		t.Fatalf("live output must survive: %v", err)
	}
	if _, err := os.Stat(filepath.Join(out, "notes.txt")); err != nil {
		t.Fatalf("non-output files must survive: %v", err)
	}
}

func TestPruneStaleOutputsSkipsNonLua(t *testing.T) {
	dir := t.TempDir()
	weird := filepath.Join(dir, "dist", "notes.txt")
	if err := os.MkdirAll(filepath.Dir(weird), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(weird, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	prev := map[string]string{filepath.Join(dir, "gone.tl"): weird}
	deleted := pruneStaleOutputs(prev, map[string]bool{})
	if len(deleted) != 0 {
		t.Fatalf("must never delete non-.lua: %v", deleted)
	}
	if _, err := os.Stat(weird); err != nil {
		t.Fatalf("non-.lua file must survive: %v", err)
	}
}

func writeJSON(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestReloadCfgKeepsLastGoodOnBadJSON(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "tlua.json")
	writeJSON(t, cfgPath, `{"compilerOptions": {"target": "5.4", "rootDir": ".", "outDir": "dist"}}`)
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	// Break the config mid-edit (half-saved file): must keep previous.
	writeJSON(t, cfgPath, `{"compilerOptions": { broken`)
	kept, keptPath := reloadCfg(cfg, cfgPath, cliOverrides{})
	if kept != cfg {
		t.Fatalf("bad JSON must keep previous config object")
	}
	if keptPath != cfgPath {
		t.Fatalf("path must be preserved, got %q", keptPath)
	}
	// A fixed config reloads and CLI overrides still win.
	writeJSON(t, cfgPath, `{"compilerOptions": {"target": "5.1", "rootDir": ".", "outDir": "dist"}}`)
	next, _ := reloadCfg(cfg, cfgPath, cliOverrides{outDir: "custom", hasOutDir: true})
	if next == cfg {
		t.Fatalf("fixed JSON must produce a new config")
	}
	if next.Target() != "5.1" {
		t.Fatalf("new target must apply, got %q", next.Target())
	}
	if next.OutDir() != "custom" {
		t.Fatalf("CLI outDir must win over reloaded config, got %q", next.OutDir())
	}
}

func TestReloadCfgMissingKeepsPrevious(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Default(dir)
	kept, _ := reloadCfg(cfg, filepath.Join(dir, "tlua.json"), cliOverrides{})
	if kept != cfg {
		t.Fatalf("missing config must keep previous")
	}
}
