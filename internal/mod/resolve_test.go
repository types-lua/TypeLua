package mod

import (
	"context"
	"fmt"
	"testing"
)

// fakeReg is an in-memory Registry: tags and manifests per module path.
type fakeReg struct {
	tags map[string]map[string]string    // path -> tag -> commit
	mani map[string]map[string]*Manifest // path -> commit -> manifest
	revs map[string]map[string]string    // path -> rev -> commit
}

func (f *fakeReg) Tags(_ context.Context, path string, _ Dep) (map[string]string, error) {
	t, ok := f.tags[path]
	if !ok {
		return nil, fmt.Errorf("unknown module %q", path)
	}
	out := map[string]string{}
	for k, v := range t {
		out[k] = v
	}
	return out, nil
}

func (f *fakeReg) Manifest(_ context.Context, path, commit string) (*Manifest, error) {
	if m, ok := f.mani[path][commit]; ok {
		return m, nil
	}
	return nil, nil // leaf without manifest
}

func (f *fakeReg) ResolveRev(_ context.Context, path string, _ Dep, rev string) (string, error) {
	if c, ok := f.revs[path][rev]; ok {
		return c, nil
	}
	return "", fmt.Errorf("unknown rev %q for %q", rev, path)
}

func lockMap(l *Lock) map[string]LockEntry {
	m := map[string]LockEntry{}
	for _, e := range l.Entries {
		m[e.Path] = e
	}
	return m
}

func TestResolveBasic(t *testing.T) {
	ctx := context.Background()
	reg := &fakeReg{
		tags: map[string]map[string]string{
			"example.com/a": {"v1.0.0": "a1", "v1.2.0": "a2", "v2.0.0": "a3"},
			"example.com/b": {"v1.1.0": "b1"},
		},
		mani: map[string]map[string]*Manifest{
			"example.com/a": {"a2": {Deps: map[string]*Dep{"example.com/b": {Query: "^v1.0.0"}}}},
		},
	}
	root := NewManifest("root")
	root.Deps["example.com/a"] = &Dep{Query: "^v1.0.0"}
	l, err := Resolve(ctx, root, reg)
	if err != nil {
		t.Fatal(err)
	}
	m := lockMap(l)
	if m["example.com/a"].Version != "v1.2.0" || m["example.com/a"].Commit != "a2" || !m["example.com/a"].Direct {
		t.Fatalf("bad a: %+v", m)
	}
	if m["example.com/b"].Version != "v1.1.0" || m["example.com/b"].Direct {
		t.Fatalf("bad b (must be indirect): %+v", m)
	}
}

func TestResolveMVSMax(t *testing.T) {
	ctx := context.Background()
	reg := &fakeReg{
		tags: map[string]map[string]string{
			"example.com/a": {"v1.0.0": "a1"},
			"example.com/b": {"v1.0.0": "b1", "v1.1.0": "b2", "v1.5.0": "b3"},
		},
		mani: map[string]map[string]*Manifest{
			"example.com/a": {"a1": {Deps: map[string]*Dep{"example.com/b": {Query: "^v1.0.0"}}}},
		},
	}
	root := NewManifest("root")
	root.Deps["example.com/a"] = &Dep{Query: "v1.0.0"}
	root.Deps["example.com/b"] = &Dep{Query: "^v1.1.0"}
	l, err := Resolve(ctx, root, reg)
	if err != nil {
		t.Fatal(err)
	}
	m := lockMap(l)
	if m["example.com/b"].Version != "v1.5.0" {
		t.Fatalf("MVS must pick max: %+v", m)
	}
}

func TestResolveExactPin(t *testing.T) {
	ctx := context.Background()
	reg := &fakeReg{
		tags: map[string]map[string]string{
			"example.com/a": {"v1.2.3": "a1", "v1.5.0": "a2"},
		},
	}
	root := NewManifest("root")
	root.Deps["example.com/a"] = &Dep{Query: "v1.2.3"}
	l, err := Resolve(ctx, root, reg)
	if err != nil {
		t.Fatal(err)
	}
	if got := lockMap(l)["example.com/a"].Version; got != "v1.2.3" {
		t.Fatalf("exact must pin, got %q", got)
	}
}

func TestResolveErrors(t *testing.T) {
	ctx := context.Background()
	mkreg := func() *fakeReg {
		return &fakeReg{
			tags: map[string]map[string]string{
				"example.com/a": {"v1.2.3": "a1", "v1.5.0": "a2"},
				"example.com/b": {"v1.0.0": "b1"},
				"example.com/p": {"v1.0.0": "p1"},
			},
			mani: map[string]map[string]*Manifest{
				"example.com/a": {"a1": {Deps: map[string]*Dep{"example.com/b": {Query: "^v2.0.0"}}}},
				"example.com/p": {
					"p1": {Deps: map[string]*Dep{
						"example.com/a": {Query: "v1.5.0"},
						"example.com/c": {Query: "dev"},
					}},
				},
			},
			revs: map[string]map[string]string{"example.com/c": {"main": "c0", "dev": "c1"}},
		}
	}
	resolve := func(deps map[string]*Dep, reg *fakeReg) error {
		root := NewManifest("root")
		root.Deps = deps
		_, err := Resolve(ctx, root, reg)
		return err
	}
	// Unknown module.
	if err := resolve(map[string]*Dep{"example.com/nope": {Query: "latest"}}, mkreg()); err == nil {
		t.Fatalf("unknown module accepted")
	}
	// No matching version.
	if err := resolve(map[string]*Dep{"example.com/a": {Query: "^v9.0.0"}}, mkreg()); err == nil {
		t.Fatalf("unsatisfiable query accepted")
	}
	// Transitive unsatisfiable (a@1.2.3 needs b@^2.0.0, only 1.0.0).
	if err := resolve(map[string]*Dep{"example.com/a": {Query: "v1.2.3"}}, mkreg()); err == nil {
		t.Fatalf("transitive unsatisfiable accepted")
	}
	// Conflicting exact pins (root 1.2.3 vs p's 1.5.0).
	if err := resolve(map[string]*Dep{
		"example.com/a": {Query: "v1.2.3"},
		"example.com/p": {Query: "v1.0.0"},
	}, mkreg()); err == nil {
		t.Fatalf("conflicting exacts accepted")
	}
	// Conflicting revisions (root main vs p's dev).
	if err := resolve(map[string]*Dep{
		"example.com/c": {Query: "main"},
		"example.com/p": {Query: "v1.0.0"},
	}, mkreg()); err == nil {
		t.Fatalf("conflicting revisions accepted")
	}
	// Mixed revision and semver for one path.
	reg := mkreg()
	reg.tags["example.com/c"] = map[string]string{"v1.0.0": "c2"}
	reg.mani["example.com/p"]["p1"].Deps["example.com/c"] = &Dep{Query: "^v1.0.0"}
	root := NewManifest("root")
	root.Deps["example.com/c"] = &Dep{Query: "main"}
	root.Deps["example.com/p"] = &Dep{Query: "v1.0.0"}
	if _, err := Resolve(ctx, root, reg); err == nil {
		t.Fatalf("mixed revision+semver accepted")
	}
	// Single revision resolves.
	root2 := NewManifest("root2")
	root2.Deps["example.com/c"] = &Dep{Query: "main"}
	if _, err := Resolve(ctx, root2, mkreg()); err != nil {
		t.Fatalf("single revision should resolve: %v", err)
	}
}
