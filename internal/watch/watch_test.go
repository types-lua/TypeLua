package watch

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/typelua/typelua/internal/config"
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

// bumpMtime forces a distinct mtime so tests never depend on FS granularity.
func bumpMtime(t *testing.T, path string, tm time.Time) {
	t.Helper()
	if err := os.Chtimes(path, tm, tm); err != nil {
		t.Fatal(err)
	}
}

func testProject(t *testing.T) (string, *config.Config) {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "tlua.json"), `{
  "compilerOptions": {"target": "5.4", "strict": true, "rootDir": ".", "outDir": "dist"},
  "include": ["**/*.tl"],
  "exclude": ["dist"]
}
`)
	writeFile(t, filepath.Join(dir, "a.tl"), "local x: number = 1\n")
	writeFile(t, filepath.Join(dir, "b.tl"), "local y: number = 2\n")
	cfg, err := config.Load(filepath.Join(dir, "tlua.json"))
	if err != nil {
		t.Fatal(err)
	}
	return dir, cfg
}

func changeKinds(changes []Change) map[string]ChangeKind {
	m := map[string]ChangeKind{}
	for _, c := range changes {
		m[c.Path] = c.Kind
	}
	return m
}

func TestCollectSeesSourcesAndConfig(t *testing.T) {
	dir, cfg := testProject(t)
	snap, err := Collect(cfg)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"a.tl", "b.tl", "tlua.json"} {
		if _, ok := snap[filepath.Join(dir, want)]; !ok {
			t.Fatalf("snapshot missing %s: %v", want, keys(snap))
		}
	}
	if !snap[filepath.Join(dir, "tlua.json")].Config {
		t.Fatalf("tlua.json must be marked Config")
	}
	if snap[filepath.Join(dir, "a.tl")].Config {
		t.Fatalf("a.tl must not be marked Config")
	}
}

func keys(s Snapshot) []string {
	var out []string
	for k := range s {
		out = append(out, k)
	}
	return out
}

func TestDiffModifiedAddedRemoved(t *testing.T) {
	dir, cfg := testProject(t)
	base, err := Collect(cfg)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	writeFile(t, filepath.Join(dir, "a.tl"), "local x: number = 2\n")
	bumpMtime(t, filepath.Join(dir, "a.tl"), now.Add(2*time.Second))
	writeFile(t, filepath.Join(dir, "c.tl"), "local z: number = 3\n")
	if err := os.Remove(filepath.Join(dir, "b.tl")); err != nil {
		t.Fatal(err)
	}
	cur, err := Collect(cfg)
	if err != nil {
		t.Fatal(err)
	}
	got := changeKinds(Diff(base, cur))
	if got[filepath.Join(dir, "a.tl")] != Modified {
		t.Fatalf("a.tl should be Modified: %v", got)
	}
	if got[filepath.Join(dir, "c.tl")] != Added {
		t.Fatalf("c.tl should be Added: %v", got)
	}
	if got[filepath.Join(dir, "b.tl")] != Removed {
		t.Fatalf("b.tl should be Removed: %v", got)
	}
}

func TestDiffNoChange(t *testing.T) {
	_, cfg := testProject(t)
	base, err := Collect(cfg)
	if err != nil {
		t.Fatal(err)
	}
	cur, err := Collect(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if changes := Diff(base, cur); len(changes) != 0 {
		t.Fatalf("expected no changes, got %v", changes)
	}
}

func TestCollectExcludesOutDir(t *testing.T) {
	dir, cfg := testProject(t)
	// Even when explicitly listed, files under outDir are never watched,
	// so emitting can never retrigger a rebuild.
	gen := filepath.Join(dir, "dist", "gen.tl")
	writeFile(t, gen, "local g = 1\n")
	cfg.Files = []string{gen, filepath.Join(dir, "a.tl")}
	snap, err := Collect(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := snap[gen]; ok {
		t.Fatalf("outDir file must not be watched: %v", keys(snap))
	}
	if _, ok := snap[filepath.Join(dir, "a.tl")]; !ok {
		t.Fatalf("a.tl must be watched: %v", keys(snap))
	}
}

func TestCollectTracksTomlAndLock(t *testing.T) {
	dir, cfg := testProject(t)
	base, err := Collect(cfg)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, "tlua.toml"), "[package]\nname = \"app\"\n")
	cur, err := Collect(cfg)
	if err != nil {
		t.Fatal(err)
	}
	changes := Diff(base, cur)
	if len(changes) != 1 || changes[0].Kind != Added || !changes[0].Config {
		t.Fatalf("tlua.toml creation must be an Added config change: %v", changes)
	}
	if !HasConfigChange(changes) {
		t.Fatalf("HasConfigChange must be true: %v", changes)
	}
	// Touching a source is not a config change.
	writeFile(t, filepath.Join(dir, "a.tl"), "local x: number = 9\n")
	bumpMtime(t, filepath.Join(dir, "a.tl"), time.Now().Add(2*time.Second))
	cur2, err := Collect(cfg)
	if err != nil {
		t.Fatal(err)
	}
	srcChanges := Diff(base, cur2)
	if HasConfigChange(filterConfig(srcChanges, false)) {
		t.Fatalf("source-only changes must not flag config: %v", srcChanges)
	}
}

func filterConfig(changes []Change, want bool) []Change {
	var out []Change
	for _, c := range changes {
		if c.Config == want {
			out = append(out, c)
		}
	}
	return out
}

func TestDiffSorted(t *testing.T) {
	base := Snapshot{}
	cur := Snapshot{
		"/p/z.tl": {Size: 1, ModTime: 1},
		"/p/a.tl": {Size: 1, ModTime: 1},
		"/p/m.tl": {Size: 1, ModTime: 1},
	}
	changes := Diff(base, cur)
	if len(changes) != 3 {
		t.Fatalf("got %v", changes)
	}
	for i := 1; i < len(changes); i++ {
		if changes[i-1].Path >= changes[i].Path {
			t.Fatalf("not sorted: %v", changes)
		}
	}
}

func TestNextDetectsChange(t *testing.T) {
	_, cfg := testProject(t)
	base, err := Collect(cfg)
	if err != nil {
		t.Fatal(err)
	}
	w := &Watcher{Poll: 50 * time.Millisecond, Debounce: 150 * time.Millisecond,
		Collect: func() (Snapshot, error) { return Collect(cfg) }}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	type result struct {
		snap    Snapshot
		changes []Change
		err     error
	}
	done := make(chan result, 1)
	go func() {
		snap, changes, err := w.Next(ctx, base)
		done <- result{snap, changes, err}
	}()
	time.Sleep(150 * time.Millisecond)
	aPath := filepath.Join(cfg.Dir, "a.tl")
	writeFile(t, aPath, "local x: number = 42\n")
	bumpMtime(t, aPath, time.Now().Add(2*time.Second))
	res := <-done
	if res.err != nil {
		t.Fatalf("Next failed: %v", res.err)
	}
	if changeKinds(res.changes)[aPath] != Modified {
		t.Fatalf("expected Modified for a.tl: %v", res.changes)
	}
	if len(Diff(res.snap, mustCollect(t, cfg))) != 0 {
		t.Fatalf("returned snapshot must be current")
	}
}

func TestNextDebounceCoalescesBurst(t *testing.T) {
	_, cfg := testProject(t)
	base, err := Collect(cfg)
	if err != nil {
		t.Fatal(err)
	}
	w := &Watcher{Poll: 50 * time.Millisecond, Debounce: 300 * time.Millisecond,
		Collect: func() (Snapshot, error) { return Collect(cfg) }}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	done := make(chan []Change, 1)
	go func() {
		_, changes, err := w.Next(ctx, base)
		if err != nil {
			done <- nil
			return
		}
		done <- changes
	}()
	time.Sleep(120 * time.Millisecond)
	tm := time.Now().Add(2 * time.Second)
	writeFile(t, filepath.Join(cfg.Dir, "a.tl"), "local x: number = 10\n")
	bumpMtime(t, filepath.Join(cfg.Dir, "a.tl"), tm)
	time.Sleep(100 * time.Millisecond) // inside the debounce window
	writeFile(t, filepath.Join(cfg.Dir, "b.tl"), "local y: number = 20\n")
	bumpMtime(t, filepath.Join(cfg.Dir, "b.tl"), tm)
	changes := <-done
	got := changeKinds(changes)
	if got[filepath.Join(cfg.Dir, "a.tl")] != Modified || got[filepath.Join(cfg.Dir, "b.tl")] != Modified {
		t.Fatalf("burst must coalesce into one rebuild covering both: %v", changes)
	}
}

func TestNextCtxCancel(t *testing.T) {
	_, cfg := testProject(t)
	base, err := Collect(cfg)
	if err != nil {
		t.Fatal(err)
	}
	w := &Watcher{Poll: 50 * time.Millisecond,
		Collect: func() (Snapshot, error) { return Collect(cfg) }}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := w.Next(ctx, base); err == nil {
		t.Fatalf("expected context error")
	}
}

func mustCollect(t *testing.T, cfg *config.Config) Snapshot {
	t.Helper()
	snap, err := Collect(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return snap
}
