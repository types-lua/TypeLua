package mod

import (
	"strings"
	"testing"
)

func TestManifestRoundTrip(t *testing.T) {
	m := NewManifest("myapp")
	m.Version = "v0.1.0"
	m.Deps["github.com/foo/bar"] = &Dep{Query: "^v1.2.0"}
	m.Deps["example.com/x/y"] = &Dep{Query: "v0.3.1", URL: "https://git.example.com/x/y.git"}
	m.Replace["github.com/foo/bar"] = "../local-bar"
	out := EncodeManifest(m)
	back, err := ParseManifest(out)
	if err != nil {
		t.Fatalf("reparse failed: %v\n%s", err, out)
	}
	if back.Name != "myapp" || back.Version != "v0.1.0" {
		t.Fatalf("bad package: %+v", back)
	}
	if d := back.Deps["github.com/foo/bar"]; d == nil || d.Query != "^v1.2.0" {
		t.Fatalf("bad dep: %+v", back.Deps)
	}
	if d := back.Deps["example.com/x/y"]; d == nil || d.URL != "https://git.example.com/x/y.git" {
		t.Fatalf("bad url dep: %+v", back.Deps)
	}
	if back.Replace["github.com/foo/bar"] != "../local-bar" {
		t.Fatalf("bad replace: %+v", back.Replace)
	}
	// Deterministic: encode twice identical.
	if out2 := EncodeManifest(back); out2 != out {
		t.Fatalf("not deterministic:\n%s\n---\n%s", out, out2)
	}
}

func TestManifestErrors(t *testing.T) {
	for _, src := range []string{
		"[package]\n",                                 // empty name
		"[package]\nname = 1\n",                       // non-string name
		"[package]\nname = \"x\"\nbogus = 1\n",        // unknown key
		"[package]\nname = \"x\"\n[unknown]\na = 1\n", // unknown table
		"[package]\nname = \"x\"\n[dependencies.\"bad path!\"]\nversion = \"v1.0.0\"\n",
		"[package]\nname = \"x\"\n[dependencies.\"a/b\"]\n", // missing query
		"[package]\nname = \"x\"\n[dependencies.\"a/b\"]\nversion = \"nope..bad query!\"\n",
		"[package]\nname = \"x\"\n[dependencies.\"a/b\"]\nversion = \"v1.0.0\"\nextra = 1\n",
		"[[module]]\npath = \"x\"\n", // arrays rejected in manifest
	} {
		if _, err := ParseManifest(src); err == nil {
			t.Fatalf("expected error for %q", src)
		}
	}
	// Empty section headers are valid.
	if _, err := ParseManifest("[package]\nname = \"x\"\n[dependencies]\n"); err != nil {
		t.Fatalf("empty dep table should be valid: %v", err)
	}
}

func TestLockRoundTrip(t *testing.T) {
	l := NewLock()
	l.Set(LockEntry{Path: "github.com/b/z", Version: "v1.0.0", Commit: "cc", Direct: false})
	l.Set(LockEntry{Path: "github.com/a/y", Version: "v2.0.0", Commit: "aa", Direct: true})
	out := EncodeLock(l)
	back, err := ParseLock(out)
	if err != nil {
		t.Fatalf("reparse failed: %v\n%s", err, out)
	}
	if len(back.Entries) != 2 || back.Entries[0].Path != "github.com/a/y" {
		t.Fatalf("lock must sort by path: %+v", back.Entries)
	}
	if !back.Entries[0].Direct || back.Entries[1].Direct {
		t.Fatalf("bad direct flags: %+v", back.Entries)
	}
	if e := back.Lookup("github.com/b/z"); e == nil || e.Commit != "cc" {
		t.Fatalf("bad lookup: %+v", back.Entries)
	}
}

func TestLockErrors(t *testing.T) {
	for _, src := range []string{
		"[[module]]\npath = \"a/b\"\n", // missing version+commit
		"[[module]]\npath = \"a/b\"\nversion = \"v1\"\ncommit = \"c\"\n[[module]]\npath = \"a/b\"\nversion = \"v2\"\ncommit = \"d\"\n",
		"[[module]]\npath = \"a/b\"\nversion = \"v1\"\ncommit = \"c\"\nbogus = 1\n",
		"[stray]\nx = 1\n",
	} {
		if _, err := ParseLock(src); err == nil {
			t.Fatalf("expected error for %q", src)
		}
	}
}

func TestSemver(t *testing.T) {
	for _, s := range []string{"v1.2.3", "1.2.3", "v0.0.0", "v10.20.30-alpha.1", "v1.2.3+build"} {
		if _, err := ParseVersion(s); err != nil {
			t.Fatalf("valid %q rejected: %v", s, err)
		}
	}
	for _, s := range []string{"", "v1", "v1.2", "1.2.3.4", "v01.2.3", "v1.2.3-", "v1.2.3-..", "v1.2.x", "foo"} {
		if _, err := ParseVersion(s); err == nil {
			t.Fatalf("invalid %q accepted", s)
		}
	}
	cmp := func(a, b string, want int) {
		t.Helper()
		va, _ := ParseVersion(a)
		vb, _ := ParseVersion(b)
		if got := va.Compare(vb); got != want {
			t.Fatalf("compare %s %s: want %d got %d", a, b, want, got)
		}
	}
	cmp("v1.2.3", "v1.2.3", 0)
	cmp("v1.2.3", "v1.2.4", -1)
	cmp("v2.0.0", "v10.0.0", -1)
	cmp("v1.0.0-alpha", "v1.0.0", -1)
	cmp("v1.0.0-alpha", "v1.0.0-beta", -1)
	cmp("v1.0.0-alpha.1", "v1.0.0-alpha.2", -1)
	cmp("v1.0.0-1", "v1.0.0-a", -1) // numeric < alphanumeric
	// Caret matching (npm rules).
	matches := func(q, v string, want bool) {
		t.Helper()
		qq, err := ParseQuery(q)
		if err != nil {
			t.Fatalf("bad query %q: %v", q, err)
		}
		vv, _ := ParseVersion(v)
		if got := qq.Matches(vv); got != want {
			t.Fatalf("query %q vs %q: want %v", q, v, want)
		}
	}
	matches("^v1.2.3", "v1.9.0", true)
	matches("^v1.2.3", "v2.0.0", false)
	matches("^v1.2.3", "v1.2.2", false)
	matches("^v0.2.3", "v0.2.9", true)
	matches("^v0.2.3", "v0.3.0", false)
	matches("^v0.0.3", "v0.0.3", true)
	matches("^v0.0.3", "v0.0.4", false)
	matches("v1.2.3", "v1.2.3", true)
	matches("v1.2.3", "v1.2.4", false)
	for _, q := range []string{"latest", "^v1.2.3", "v1.2.3", "main", "abc123def456"} {
		if _, err := ParseQuery(q); err != nil {
			t.Fatalf("valid query %q rejected: %v", q, err)
		}
	}
	if _, err := ParseQuery(""); err == nil {
		t.Fatalf("empty query accepted")
	}
}

func TestValidPath(t *testing.T) {
	for _, p := range []string{"github.com/a/b", "example.com/x/y/z", "gitee.com/o/r"} {
		if !ValidHostPath(p) {
			t.Fatalf("valid %q rejected", p)
		}
	}
	for _, p := range []string{"", "a//b", "../x", "a b/c", "github.com/a/"} {
		if ValidPath(p) {
			t.Fatalf("invalid %q accepted", p)
		}
	}
	// Non-dotted paths pass ValidPath but need an explicit url in manifests.
	if !ValidPath("nodots/a") || ValidHostPath("nodots/a") {
		t.Fatalf("nodots/a must pass ValidPath but fail ValidHostPath")
	}
	if !ValidPath("local/mod") || ValidHostPath("local/mod") {
		t.Fatalf("non-dotted path must pass ValidPath but fail ValidHostPath")
	}
	if !strings.Contains(EncodeManifest(NewManifest("x")), "tlua.toml") {
		t.Fatalf("manifest header comment missing")
	}
}
