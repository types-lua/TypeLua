package mod

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func requireGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
}

// gitRepo creates a git repo, applying each tag's file snapshot cumulatively
// (tags given in order).
func gitRepo(t *testing.T, snaps map[string]map[string]string, tags ...string) string {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_CONFIG_NOSYSTEM=1",
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t",
		)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q")
	for _, tag := range tags {
		for name, content := range snaps[tag] {
			p := filepath.Join(dir, name)
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		run("add", "-A")
		run("commit", "-qm", tag)
		run("tag", tag)
	}
	return dir
}

func testCtx() context.Context {
	// Plain background: each git op carries its own timeout.
	return context.Background()
}

func writeProject(t *testing.T, dir, manifest string) *Project {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ManifestName), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	p, err := LoadProject(dir)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// TestGetFetchVendor exercises the full flow against local git repos.
func TestGetFetchVendor(t *testing.T) {
	requireGit(t)
	ctx := testCtx()
	tmp := t.TempDir()
	// repoB: v1.0.0 and v1.1.0.
	repoB := gitRepo(t, map[string]map[string]string{
		"v1.0.0": {
			"tlua.toml": "[package]\nname = \"example.com/b\"\nversion = \"v1.0.0\"\n",
			"util.tl":   "local M = {}\nfunction M.add(a: number, b: number): number return a + b end\nreturn M\n",
		},
		"v1.1.0": {
			"extra.tl": "local M = {}\nfunction M.mul(a: number, b: number): number return a * b end\nreturn M\n",
		},
	}, "v1.0.0", "v1.1.0")
	// repoA depends on B.
	repoA := gitRepo(t, map[string]map[string]string{
		"v1.0.0": {
			"tlua.toml": "[package]\nname = \"example.com/a\"\nversion = \"v1.0.0\"\n\n[dependencies.\"example.com/b\"]\nversion = \"^v1.0.0\"\nurl = \"" + repoB + "\"\n",
			"main.tl":   "local M = {}\nfunction M.x(): number return 1 end\nreturn M\n",
		},
	}, "v1.0.0")

	projDir := filepath.Join(tmp, "proj")
	p := writeProject(t, projDir, "[package]\nname = \"myapp\"\n\n[dependencies.\"example.com/a\"]\nversion = \"^v1.0.0\"\nurl = \""+repoA+"\"\n")
	cache := NewCache(filepath.Join(tmp, "modcache"))

	l, err := Get(ctx, p, cache, []string{"example.com/a"})
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(l.Entries) != 2 {
		t.Fatalf("want 2 locked modules, got %+v", l.Entries)
	}
	m := lockMap(l)
	if m["example.com/a"].Version != "v1.0.0" || !m["example.com/a"].Direct {
		t.Fatalf("bad a: %+v", m)
	}
	if m["example.com/b"].Version != "v1.1.0" || m["example.com/b"].Direct {
		t.Fatalf("bad b (want latest indirect v1.1.0): %+v", m)
	}
	if m["example.com/b"].Commit == "" {
		t.Fatalf("missing commit: %+v", m)
	}
	// Trees materialized.
	for _, e := range l.Entries {
		if cache.TreeCommit(cache.TreeDir(e.Path, e.Version)) != e.Commit {
			t.Fatalf("tree missing for %s", e.Path)
		}
	}
	// Manifest + lock persisted.
	if _, err := os.Stat(filepath.Join(projDir, LockName)); err != nil {
		t.Fatalf("lock not written: %v", err)
	}
	// Fetch reproduces (idempotent, no upgrades).
	l2, err := Fetch(ctx, p, cache)
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if len(l2.Entries) != 2 || lockMap(l2)["example.com/b"].Version != "v1.1.0" {
		t.Fatalf("fetch changed the lock: %+v", l2.Entries)
	}
	// Vendor layout.
	if err := p.Vendor(ctx, cache); err != nil {
		t.Fatalf("vendor: %v", err)
	}
	for _, want := range []string{
		"tlua_modules/example.com/a/main.tl",
		"tlua_modules/example.com/b/util.tl",
		"tlua_modules/example.com/b/extra.tl",
		"tlua_modules/modules.txt",
	} {
		if _, err := os.Stat(filepath.Join(projDir, want)); err != nil {
			t.Fatalf("missing vendored %s: %v", want, err)
		}
	}
	// Graph shows the indirect edge.
	g, err := ModuleGraph(ctx, p, cache)
	if err != nil {
		t.Fatalf("graph: %v", err)
	}
	if len(g.Edges["example.com/a"]) != 1 || g.Edges["example.com/a"][0] != "example.com/b" {
		t.Fatalf("bad graph: %+v", g.Edges)
	}
}

// TestGetReplaceLocal covers directory replacements end to end.
func TestGetReplaceLocal(t *testing.T) {
	requireGit(t)
	ctx := testCtx()
	tmp := t.TempDir()
	localB := filepath.Join(tmp, "localb")
	if err := os.MkdirAll(localB, 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(localB, "tlua.toml"), []byte("[package]\nname = \"example.com/b\"\nversion = \"v9.9.9\"\n"), 0o644)
	os.WriteFile(filepath.Join(localB, "util.tl"), []byte("local M = {}\nreturn M\n"), 0o644)
	repoA := gitRepo(t, map[string]map[string]string{
		"v1.0.0": {
			"tlua.toml": "[package]\nname = \"example.com/a\"\nversion = \"v1.0.0\"\n\n[dependencies.\"example.com/b\"]\nversion = \"^v1.0.0\"\n",
			"main.tl":   "local M = {}\nreturn M\n",
		},
	}, "v1.0.0")
	// Note: A's manifest has no url for B; replace supplies it (no probing).
	projDir := filepath.Join(tmp, "proj")
	p := writeProject(t, projDir,
		"[package]\nname = \"myapp\"\n\n[dependencies.\"example.com/a\"]\nversion = \"v1.0.0\"\nurl = \""+repoA+"\"\n\n[replace]\n\"example.com/b\" = \""+localB+"\"\n")
	cache := NewCache(filepath.Join(tmp, "modcache"))
	l, err := Get(ctx, p, cache, []string{"example.com/a"})
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	m := lockMap(l)
	b, ok := m["example.com/b"]
	if !ok {
		t.Fatalf("b missing: %+v", l.Entries)
	}
	if b.Commit != "local" || b.Direct {
		t.Fatalf("replaced b must be indirect+local: %+v", b)
	}
	if b.Version != "v9.9.9" {
		t.Fatalf("replaced version must follow local manifest, got %q", b.Version)
	}
	if err := p.Vendor(ctx, cache); err != nil {
		t.Fatalf("vendor: %v", err)
	}
	if _, err := os.Stat(filepath.Join(projDir, "tlua_modules", "example.com", "b", "util.tl")); err != nil {
		t.Fatalf("replaced file not vendored: %v", err)
	}
}

// TestGetErrors covers offline, missing, and unsatisfiable cases.
func TestGetErrors(t *testing.T) {
	requireGit(t)
	ctx := testCtx()
	tmp := t.TempDir()
	projDir := filepath.Join(tmp, "proj")
	p := writeProject(t, projDir, "[package]\nname = \"myapp\"\n")
	cache := NewCache(filepath.Join(tmp, "modcache"))
	// Unknown host (no network in tests; probing fails fast on bad host).
	if _, err := Get(ctx, p, cache, []string{"no-such.invalid/x@latest"}); err == nil {
		t.Fatalf("unknown host accepted")
	}
	// Bad spec.
	if _, err := Get(ctx, p, cache, []string{"bad path!"}); err == nil {
		t.Fatalf("bad spec accepted")
	}
	// Empty vendor.
	if err := p.Vendor(ctx, cache); err == nil {
		t.Fatalf("empty vendor accepted")
	}
	// Offline with cold cache.
	t.Setenv(OfflineEnv, "1")
	offCache := NewCache(filepath.Join(tmp, "modcache2"))
	if _, err := Get(ctx, p, offCache, []string{"example.com/a@latest"}); err == nil {
		t.Fatalf("offline cold fetch accepted")
	}
}

func TestHelpers(t *testing.T) {
	if got := strings.Join(CandidateURLs("example.com/a/b"), ","); got != "https://example.com/a/b,https://example.com/a" {
		t.Fatalf("bad candidates: %q", got)
	}
	if p, q := splitSpec("example.com/a"); p != "example.com/a" || q != "" {
		t.Fatalf("bad spec split: %q %q", p, q)
	}
	if p, q := splitSpec("example.com/a@v1.2.3"); p != "example.com/a" || q != "v1.2.3" {
		t.Fatalf("bad spec split: %q %q", p, q)
	}
	if escapePath("GitHub.com/Foo/Bar") != "!git!hub.com/!foo/!bar" {
		t.Fatalf("bad escape: %q", escapePath("GitHub.com/Foo/Bar"))
	}
	m := NewManifest("x")
	m.Deps["example.com/a"] = &Dep{Query: "^v1.0.0"}
	l := NewLock()
	l.Set(LockEntry{Path: "example.com/a", Version: "v1.1.0", Commit: "c"})
	if pins := pinsForFetch(m, l); pins["example.com/a"] != "v1.1.0" {
		t.Fatalf("bad pins: %v", pins)
	}
	l.Set(LockEntry{Path: "example.com/a", Version: "v2.0.0", Commit: "c"})
	if pins := pinsForFetch(m, l); len(pins) != 0 {
		t.Fatalf("stale pin kept: %v", pins)
	}
}
