package mod

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Project is a directory with a manifest and optional lock.
type Project struct {
	Dir      string
	Manifest *Manifest
	Lock     *Lock
	// HasLock reports whether tlua.lock existed on load.
	HasLock bool
}

// FindManifest searches dir upward for tlua.toml.
func FindManifest(dir string) string {
	abs, err := filepath.Abs(dir)
	if err != nil {
		abs = dir
	}
	for {
		p := filepath.Join(abs, ManifestName)
		if _, err := os.Stat(p); err == nil {
			return p
		}
		parent := filepath.Dir(abs)
		if parent == abs {
			return ""
		}
		abs = parent
	}
}

// LoadProject loads the manifest (required) and lock (optional) for dir.
func LoadProject(dir string) (*Project, error) {
	mp := FindManifest(dir)
	if mp == "" {
		return nil, fmt.Errorf("no %s found (run `tlua mod init`)", ManifestName)
	}
	raw, err := os.ReadFile(mp)
	if err != nil {
		return nil, err
	}
	m, err := ParseManifest(string(raw))
	if err != nil {
		return nil, fmt.Errorf("bad %s: %v", mp, err)
	}
	p := &Project{Dir: filepath.Dir(mp), Manifest: m, Lock: NewLock()}
	if lraw, err := os.ReadFile(filepath.Join(p.Dir, LockName)); err == nil {
		l, err := ParseLock(string(lraw))
		if err != nil {
			return nil, fmt.Errorf("bad %s: %v", LockName, err)
		}
		p.Lock = l
		p.HasLock = true
	}
	return p, nil
}

// SaveManifest writes tlua.toml.
func (p *Project) SaveManifest() error {
	return os.WriteFile(filepath.Join(p.Dir, ManifestName), []byte(EncodeManifest(p.Manifest)), 0o644)
}

// SaveLock writes tlua.lock.
func (p *Project) SaveLock() error {
	return os.WriteFile(filepath.Join(p.Dir, LockName), []byte(EncodeLock(p.Lock)), 0o644)
}

// replaceDir resolves a local replace target to an absolute directory.
func (p *Project) replaceDir(path string) (string, bool) {
	tgt, ok := p.Manifest.Replace[path]
	if !ok {
		return "", false
	}
	if !filepath.IsAbs(tgt) {
		tgt = filepath.Join(p.Dir, tgt)
	}
	return tgt, true
}

// pinsForFetch seeds exact pins from the lock for requirements the lock
// still satisfies (so fetch reproduces instead of upgrading).
func pinsForFetch(m *Manifest, l *Lock) map[string]string {
	pins := map[string]string{}
	if l == nil {
		return pins
	}
	for _, e := range l.Entries {
		d, ok := m.Deps[e.Path]
		if !ok {
			continue
		}
		q, err := ParseQuery(d.Query)
		if err != nil {
			continue
		}
		switch q.Kind {
		case QueryExact:
			if e.Version == q.Text || equalVersion(e.Version, q.Base) {
				pins[e.Path] = e.Version
			}
		case QueryCaret:
			if v, err := ParseVersion(e.Version); err == nil && q.Matches(v) {
				pins[e.Path] = e.Version
			}
		case QueryLatest:
			pins[e.Path] = e.Version
		case QueryRevision:
			if e.Version == q.Rev {
				pins[e.Path] = e.Version
			}
		}
	}
	return pins
}

func equalVersion(tag string, v Version) bool {
	o, err := ParseVersion(tag)
	if err != nil {
		return false
	}
	return o.Compare(v) == 0
}

// registryFor builds the resolving registry (replace-aware).
func (p *Project) registryFor(cache *Cache) Registry {
	return ReplaceRegistry(&GitRegistry{Cache: cache}, p.Manifest.Replace, p.Dir)
}

// Get adds/updates requirements, resolves, downloads, and writes the lock.
// Each spec is `path` or `path@query` (default: keep manifest query, else
// latest). With no specs, all direct requirements re-resolve (upgrade).
func Get(ctx context.Context, p *Project, cache *Cache, specs []string) (*Lock, error) {
	if err := GitAvailable(ctx); err != nil {
		return nil, err
	}
	upgradeAll := len(specs) == 0
	var upgrade []string
	for _, s := range specs {
		path, query := splitSpec(s)
		if !ValidPath(path) {
			return nil, fmt.Errorf("invalid module path %q", path)
		}
		d := p.Manifest.Deps[path]
		if d == nil {
			d = &Dep{}
			p.Manifest.Deps[path] = d
		}
		if query != "" {
			if _, err := ParseQuery(query); err != nil {
				return nil, fmt.Errorf("module %q: %v", path, err)
			}
			d.Query = query
		} else if d.Query == "" {
			d.Query = "latest"
		}
		upgrade = append(upgrade, path)
	}
	if err := p.SaveManifest(); err != nil {
		return nil, err
	}
	// Locked pins elsewhere (reproduce); named modules (or all, when no
	// specs) resolve fresh.
	pins := pinsForFetch(p.Manifest, p.Lock)
	if upgradeAll {
		pins = map[string]string{}
	} else {
		for _, u := range upgrade {
			delete(pins, u)
		}
	}
	l, sources, err := p.resolveLocked(ctx, cache, pins)
	if err != nil {
		return nil, err
	}
	if !upgradeAll {
		// Pinned bystanders must not drift either (only named modules move).
		locked := NewLock()
		for _, e := range p.Lock.Entries {
			keep := true
			for _, u := range upgrade {
				if u == e.Path {
					keep = false
					break
				}
			}
			if keep {
				locked.Set(e)
			}
		}
		if err := verifyLockCommits(locked, l); err != nil {
			return nil, err
		}
	}
	if err := p.downloadAll(ctx, cache, l, sources); err != nil {
		return nil, err
	}
	p.Lock = l
	if err := p.SaveLock(); err != nil {
		return nil, err
	}
	return l, nil
}

// resolveLocked resolves with pins, returning the lock and the fetch
// source (explicit URL or "") per path. Locally-forced content (root
// replaces and discovered transitive path links) bypasses lookup inside
// the resolver; backfillLockDirs records where it came from.
func (p *Project) resolveLocked(ctx context.Context, cache *Cache, pins map[string]string) (*Lock, map[string]string, error) {
	reg := p.registryFor(cache)
	cp := &Manifest{Name: p.Manifest.Name, Version: p.Manifest.Version, Deps: map[string]*Dep{}, Replace: p.Manifest.Replace}
	for path, d := range p.Manifest.Deps {
		nd := &Dep{Query: d.Query, URL: d.URL, Path: d.Path}
		if v, ok := pins[path]; ok {
			if _, isForced := reg.LocalDir(path); !isForced {
				nd.Query = v
			}
		}
		cp.Deps[path] = nd
	}
	l, sources, err := resolveWith(ctx, cp, reg)
	if err != nil {
		return nil, nil, err
	}
	p.backfillLockDirs(reg, l)
	return l, sources, nil
}

// backfillLockDirs records project-relative source dirs for local entries
// (portable across checkouts; standalone vendor/graph need no resolver).
func (p *Project) backfillLockDirs(reg Registry, l *Lock) {
	for i := range l.Entries {
		e := &l.Entries[i]
		if e.Commit != "local" {
			e.Dir = ""
			continue
		}
		if dir, ok := reg.LocalDir(e.Path); ok {
			if rel, err := filepath.Rel(p.Dir, dir); err == nil {
				e.Dir = filepath.ToSlash(rel)
			} else {
				e.Dir = dir
			}
		}
	}
}

// splitSpec splits `path[@query]` at the first `@`
// (paths never contain `@`; queries never contain `/`).
func splitSpec(s string) (string, string) {
	// A query never contains `/`; the path never contains `@`.
	if i := strings.Index(s, "@"); i >= 0 {
		return s[:i], s[i+1:]
	}
	return s, ""
}

// Fetch re-resolves (respecting lock pins), verifies upstream commits,
// downloads everything, and writes the lock.
func Fetch(ctx context.Context, p *Project, cache *Cache) (*Lock, error) {
	if err := GitAvailable(ctx); err != nil {
		return nil, err
	}
	l, sources, err := p.resolveLocked(ctx, cache, pinsForFetch(p.Manifest, p.Lock))
	if err != nil {
		return nil, err
	}
	if err := verifyLockCommits(p.Lock, l); err != nil {
		return nil, err
	}
	if err := p.downloadAll(ctx, cache, l, sources); err != nil {
		return nil, err
	}
	p.Lock = l
	if err := p.SaveLock(); err != nil {
		return nil, err
	}
	return l, nil
}

// verifyLockCommits fails when a locked module's upstream tag moved
// (explicit `get` upgrades; fetch reproduces).
func verifyLockCommits(old, newl *Lock) error {
	if old == nil {
		return nil
	}
	for _, e := range newl.Entries {
		o := old.Lookup(e.Path)
		if o == nil || o.Commit == "local" || e.Commit == "local" {
			continue
		}
		if o.Version == e.Version && o.Commit != e.Commit {
			return fmt.Errorf("module %s@%s changed upstream (%s -> %s); run `tlua mod get %s` to upgrade",
				e.Path, e.Version, shortSHA(o.Commit), shortSHA(e.Commit), e.Path)
		}
	}
	return nil
}

func shortSHA(s string) string {
	if len(s) > 12 {
		return s[:12]
	}
	return s
}

// localSourceDir resolves a locked local entry to an absolute directory:
// the lock's recorded dir first, then the root replace table (old locks).
func (p *Project) localSourceDir(e LockEntry) (string, bool) {
	if e.Commit != "local" {
		return "", false
	}
	if e.Dir != "" {
		dir := e.Dir
		if !filepath.IsAbs(dir) {
			dir = filepath.Join(p.Dir, filepath.FromSlash(dir))
		}
		return dir, true
	}
	return p.replaceDir(e.Path)
}

// downloadAll ensures every locked module tree is present.
// sources maps paths to explicit fetch URLs (may be empty per path).
func (p *Project) downloadAll(ctx context.Context, cache *Cache, l *Lock, sources map[string]string) error {
	for _, e := range l.Entries {
		if dir, ok := p.localSourceDir(e); ok {
			if _, err := os.Stat(dir); err != nil {
				return fmt.Errorf("replace target for %q unavailable: %v", e.Path, err)
			}
			continue // vendored/copied on demand; nothing cached
		}
		// Present trees need no URL (keeps offline vendor working).
		if dst := cache.TreeDir(e.Path, e.Version); cache.TreeCommit(dst) == e.Commit {
			continue
		}
		url := sources[e.Path]
		if url == "" {
			var err error
			url, err = p.sourceURL(ctx, cache, e.Path)
			if err != nil {
				return err
			}
		}
		if _, err := cache.EnsureTree(ctx, url, e.Path, e.Version, e.Commit); err != nil {
			return fmt.Errorf("downloading %s@%s: %v", e.Path, e.Version, err)
		}
	}
	return nil
}

// sourceURL returns the fetch URL for a locked path.
func (p *Project) sourceURL(ctx context.Context, cache *Cache, path string) (string, error) {
	if d := p.Manifest.Deps[path]; d != nil && d.URL != "" {
		return d.URL, nil
	}
	// Re-derive (probing). Transitive deps have no manifest entry;
	// the lock does not store URLs, so rediscover deterministically.
	reg := &GitRegistry{Cache: cache}
	return reg.urlFor(ctx, path, Dep{})
}

// VendorDir returns the project's vendor directory.
func (p *Project) VendorDir() string {
	return filepath.Join(p.Dir, VendorDir)
}

// Vendor copies locked module sources into tlua_modules/<path>/ plus a
// modules.txt manifest. Vendored assets: *.tl (incl. *.d.tl type contracts),
// *.lua shims (the cross-language runtime contract: init.lua hides
// ffi.load/C-module/IPC differences), and native libraries (*.so/*.dylib/
// *.dll). Headers and other files stay in the module cache only.
func (p *Project) Vendor(ctx context.Context, cache *Cache) error {
	if p.Lock == nil || len(p.Lock.Entries) == 0 {
		return fmt.Errorf("nothing to vendor (empty lock; run `tlua mod fetch` first)")
	}
	if err := p.downloadAll(ctx, cache, p.Lock, nil); err != nil {
		return err
	}
	vdir := p.VendorDir()
	if err := os.MkdirAll(vdir, 0o755); err != nil {
		return err
	}
	var lines []string
	for _, e := range SortedEntries(p.Lock) {
		var src string
		if dir, ok := p.localSourceDir(e); ok {
			src = dir
		} else {
			dst := cache.TreeDir(e.Path, e.Version)
			if cache.TreeCommit(dst) == "" {
				return fmt.Errorf("%s@%s not downloaded (run `tlua mod fetch` first)", e.Path, e.Version)
			}
			src = dst
		}
		dst := filepath.Join(vdir, filepath.FromSlash(e.Path))
		if err := copyTlTree(src, dst); err != nil {
			return fmt.Errorf("vendoring %s: %v", e.Path, err)
		}
		lines = append(lines, e.Path+"@"+e.Version+"="+e.Commit)
	}
	sort.Strings(lines)
	return os.WriteFile(filepath.Join(vdir, "modules.txt"), []byte(strings.Join(lines, "\n")+"\n"), 0o644)
}

// SortedEntries returns lock entries ordered by path.
func SortedEntries(l *Lock) []LockEntry {
	out := append([]LockEntry(nil), l.Entries...)
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

// copyTlTree copies module runtime assets (preserving layout) from src to dst:
// *.tl (incl. *.d.tl), *.lua shims, and native libs (*.so/*.dylib/*.dll).
// Dotfiles and .git stay out; everything else (headers, docs) stays cached.
func copyTlTree(src, dst string) error {
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	return filepath.Walk(src, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		name := info.Name()
		if info.IsDir() {
			if name == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasPrefix(name, ".") {
			return nil
		}
		lower := strings.ToLower(name)
		isTl := strings.HasSuffix(lower, ".tl")
		isLua := strings.HasSuffix(lower, ".lua")
		isNative := strings.HasSuffix(lower, ".so") || strings.HasSuffix(lower, ".dylib") || strings.HasSuffix(lower, ".dll")
		if !isTl && !isLua && !isNative {
			return nil
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		return copyFile(p, target)
	})
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	_, err = io.Copy(out, in)
	if cerr := out.Close(); cerr != nil && err == nil {
		err = cerr
	}
	return err
}

// Graph describes locked modules and their inter-edges.
type Graph struct {
	Nodes []LockEntry
	// Edges maps module path -> required module paths (queries resolved).
	Edges map[string][]string
}

// ModuleGraph loads each locked module's manifest (from cache trees or
// replace dirs) to describe the dependency graph offline.
func ModuleGraph(ctx context.Context, p *Project, cache *Cache) (*Graph, error) {
	g := &Graph{Edges: map[string][]string{}}
	for _, e := range SortedEntries(p.Lock) {
		g.Nodes = append(g.Nodes, e)
		var m *Manifest
		if dir, ok := p.localSourceDir(e); ok {
			m = readLocalManifest(dir)
		} else {
			dst := cache.TreeDir(e.Path, e.Version)
			if cache.TreeCommit(dst) == "" {
				return nil, fmt.Errorf("%s@%s not downloaded (run `tlua mod fetch` first)", e.Path, e.Version)
			}
			m = readLocalManifest(dst)
		}
		if m == nil {
			continue
		}
		var deps []string
		for dp := range m.Deps {
			deps = append(deps, dp)
		}
		sort.Strings(deps)
		g.Edges[e.Path] = deps
	}
	return g, nil
}

func readLocalManifest(dir string) *Manifest {
	raw, err := os.ReadFile(filepath.Join(dir, ManifestName))
	if err != nil {
		return nil
	}
	m, err := ParseManifest(string(raw))
	if err != nil {
		return nil
	}
	return m
}
