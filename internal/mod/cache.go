package mod

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// CacheDirName is the vendor marker; CommitFile records the pinned commit.
const CommitFile = ".tlua-commit"

// ModCacheEnv overrides the module cache location.
const ModCacheEnv = "TLUA_MODCACHE"

// OfflineEnv disables all network use (cache/mirrors only).
const OfflineEnv = "TLUA_OFFLINE"

// DefaultDir returns the module cache directory.
func DefaultDir() (string, error) {
	if v := os.Getenv(ModCacheEnv); v != "" {
		return v, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cannot locate home directory (set %s): %v", ModCacheEnv, err)
	}
	return filepath.Join(home, ".tlua", "pkg", "mod"), nil
}

// Cache stores mirrors and extracted module trees.
type Cache struct {
	Root    string
	Offline bool
}

// NewCache creates a cache, reading TLUA_OFFLINE.
func NewCache(root string) *Cache {
	off := false
	if v := strings.TrimSpace(os.Getenv(OfflineEnv)); v != "" && v != "0" && !strings.EqualFold(v, "false") {
		off = true
	}
	return &Cache{Root: root, Offline: off}
}

// escapePath maps a module path to cache directories (Go-style:
// uppercase letters become `!` + lowercase; slashes stay).
func escapePath(p string) string {
	var sb strings.Builder
	for _, r := range p {
		if r >= 'A' && r <= 'Z' {
			sb.WriteByte('!')
			sb.WriteRune(r - 'A' + 'a')
		} else {
			sb.WriteRune(r)
		}
	}
	return sb.String()
}

// mirrorDir names the bare mirror for a URL (content-addressed).
func (c *Cache) mirrorDir(url string) string {
	sum := sha256.Sum256([]byte(url))
	return filepath.Join(c.Root, "cache", "download", fmt.Sprintf("%x", sum))
}

// MirrorURL records the source URL beside a mirror (debugging aid).
func (c *Cache) MirrorURL(mirrorDir string) string {
	raw, err := os.ReadFile(filepath.Join(mirrorDir, "tlua-url"))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(raw))
}

// Sync clones or refreshes the mirror for url, returning its directory.
// Offline mode reuses existing mirrors and errors otherwise.
func (c *Cache) Sync(ctx context.Context, url string) (string, error) {
	if err := GitAvailable(ctx); err != nil {
		return "", err
	}
	dir := c.mirrorDir(url)
	if _, err := os.Stat(filepath.Join(dir, "HEAD")); err == nil {
		if c.Offline {
			return dir, nil
		}
		if err := RemoteUpdate(ctx, dir); err != nil {
			return "", fmt.Errorf("updating %q: %v", url, err)
		}
		return dir, nil
	}
	if c.Offline {
		return "", fmt.Errorf("module %q not in cache (offline mode; run `tlua mod fetch` online first)", url)
	}
	if err := CloneMirror(ctx, url, dir); err != nil {
		return "", fmt.Errorf("cloning %q: %v", url, err)
	}
	_ = os.WriteFile(filepath.Join(dir, "tlua-url"), []byte(url+"\n"), 0o644)
	return dir, nil
}

// TreeDir returns the extracted-tree directory for path@version.
func (c *Cache) TreeDir(path, version string) string {
	return filepath.Join(c.Root, escapePath(path)+"@"+version)
}

// TreeCommit reads the recorded commit of an extracted tree ("", if absent).
func (c *Cache) TreeCommit(dir string) string {
	raw, err := os.ReadFile(filepath.Join(dir, CommitFile))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(raw))
}

// EnsureTree materializes url@commit into the tree dir (verified reuse,
// atomic replace via temp dir + rename). Returns the tree dir.
func (c *Cache) EnsureTree(ctx context.Context, url, path, version, commit string) (string, error) {
	dst := c.TreeDir(path, version)
	if c.TreeCommit(dst) == commit {
		return dst, nil
	}
	mirror, err := c.Sync(ctx, url)
	if err != nil {
		return "", err
	}
	// Resolve (and canonicalize) the commit against the mirror.
	full, err := RevParse(ctx, mirror, commit)
	if err != nil {
		return "", fmt.Errorf("resolving %s@%s: %v", url, commit, err)
	}
	if c.TreeCommit(dst) == full {
		return dst, nil
	}
	tmp, err := func() (string, error) {
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return "", err
		}
		return os.MkdirTemp(filepath.Dir(dst), ".tmp-extract-*")
	}()
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmp)
	if err := Extract(ctx, mirror, full, tmp); err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(tmp, CommitFile), []byte(full+"\n"), 0o644); err != nil {
		return "", err
	}
	_ = os.RemoveAll(dst)
	if err := os.Rename(tmp, dst); err != nil {
		return "", err
	}
	return dst, nil
}

// CandidateURLs derives fetch URLs for a module path, longest first:
// full path, then progressively shorter prefixes (minimum host + 1).
// Callers try each until one answers.
func CandidateURLs(path string) []string {
	segs := strings.Split(path, "/")
	var out []string
	for n := len(segs); n >= 2; n-- {
		out = append(out, "https://"+strings.Join(segs[:n], "/"))
	}
	return out
}

// ProbeURL finds the repository root for path by longest-prefix probing.
// Returns the repo URL (without .git suffix handling by git itself).
func ProbeURL(ctx context.Context, path string) (string, error) {
	if err := GitAvailable(ctx); err != nil {
		return "", err
	}
	var errs []string
	for _, url := range CandidateURLs(path) {
		if _, _, err := LsRemote(ctx, url); err == nil {
			return url, nil
		} else {
			errs = append(errs, url+": "+firstLine(err.Error()))
		}
	}
	return "", fmt.Errorf("no git repository found for %q (tried %d prefixes)", path, len(CandidateURLs(path)))
}

// GitRegistry resolves and reads modules through the cache.
type GitRegistry struct {
	Cache *Cache
	// probe memoizes discovered repo URLs per process run.
	probes map[string]string
}

func (g *GitRegistry) urlFor(ctx context.Context, path string, dep Dep) (string, error) {
	if g.probes == nil {
		g.probes = map[string]string{}
	}
	// Explicit overrides win and stick (one source per path; the
	// resolver rejects conflicts before we ever see them).
	if dep.URL != "" {
		g.probes[path] = dep.URL
		return dep.URL, nil
	}
	if u, ok := g.probes[path]; ok {
		return u, nil
	}
	if g.Cache != nil && g.Cache.Offline {
		return "", fmt.Errorf("module %q not cached (offline mode; add an explicit url or run online first)", path)
	}
	u, err := ProbeURL(ctx, path)
	if err != nil {
		return "", err
	}
	g.probes[path] = u
	return u, nil
}

// Tags implements Registry via mirror tag listing.
func (g *GitRegistry) Tags(ctx context.Context, path string, dep Dep) (map[string]string, error) {
	url, err := g.urlFor(ctx, path, dep)
	if err != nil {
		return nil, err
	}
	mirror, err := g.Cache.Sync(ctx, url)
	if err != nil {
		return nil, err
	}
	return MirrorTags(ctx, mirror)
}

// Manifest implements Registry by reading tlua.toml at the commit
// (missing file = leaf module, nil manifest).
func (g *GitRegistry) Manifest(ctx context.Context, path, commit string) (*Manifest, error) {
	url, err := g.urlFor(ctx, path, Dep{})
	if err != nil {
		return nil, err
	}
	mirror, err := g.Cache.Sync(ctx, url)
	if err != nil {
		return nil, err
	}
	raw, err := ShowFile(ctx, mirror, commit, ManifestName)
	if err != nil {
		// Missing manifest: treat as dependency-free leaf.
		return nil, nil
	}
	m, err := ParseManifest(string(raw))
	if err != nil {
		return nil, fmt.Errorf("bad %s in %s@%s: %v", ManifestName, path, commit, err)
	}
	return m, nil
}

// ResolveRev implements Registry.
func (g *GitRegistry) ResolveRev(ctx context.Context, path string, dep Dep, rev string) (string, error) {
	url, err := g.urlFor(ctx, path, dep)
	if err != nil {
		return "", err
	}
	mirror, err := g.Cache.Sync(ctx, url)
	if err != nil {
		return "", err
	}
	return RevParse(ctx, mirror, rev)
}

// replaceRegistry overlays local-directory replacements.
type replaceRegistry struct {
	base    Registry
	replace map[string]string // path -> local dir or URL
	rootDir string
}

// ReplaceRegistry wraps reg with replace directives (local dirs or URLs).
func ReplaceRegistry(reg Registry, replace map[string]string, rootDir string) Registry {
	if len(replace) == 0 {
		return reg
	}
	return &replaceRegistry{base: reg, replace: replace, rootDir: rootDir}
}

func (r *replaceRegistry) localDir(path string) (string, bool) {
	tgt, ok := r.replace[path]
	if !ok {
		return "", false
	}
	if isURLTarget(tgt) {
		return "", false
	}
	if !filepath.IsAbs(tgt) {
		tgt = filepath.Join(r.rootDir, tgt)
	}
	return tgt, true
}

func isURLTarget(t string) bool {
	return strings.Contains(t, "://") || strings.HasPrefix(t, "git@")
}

func (r *replaceRegistry) readLocal(path, dir string) *Manifest {
	raw, err := os.ReadFile(filepath.Join(dir, ManifestName))
	if err != nil {
		return NewManifest(path)
	}
	m, err := ParseManifest(string(raw))
	if err != nil {
		return NewManifest(path)
	}
	m.Name = path
	return m
}

func (r *replaceRegistry) Tags(ctx context.Context, path string, dep Dep) (map[string]string, error) {
	if dir, ok := r.localDir(path); ok {
		m := r.readLocal(path, dir)
		ver := m.Version
		if _, err := ParseVersion(ver); err != nil {
			ver = "v0.0.0-local"
		}
		return map[string]string{ver: "local"}, nil
	}
	if tgt, ok := r.replace[path]; ok && isURLTarget(tgt) {
		dep.URL = tgt
	}
	return r.base.Tags(ctx, path, dep)
}

func (r *replaceRegistry) Manifest(ctx context.Context, path, commit string) (*Manifest, error) {
	if dir, ok := r.localDir(path); ok {
		return r.readLocal(path, dir), nil
	}
	return r.base.Manifest(ctx, path, commit)
}

func (r *replaceRegistry) ResolveRev(ctx context.Context, path string, dep Dep, rev string) (string, error) {
	if _, ok := r.localDir(path); ok {
		return "local", nil
	}
	if tgt, ok := r.replace[path]; ok && isURLTarget(tgt) {
		dep.URL = tgt
	}
	return r.base.ResolveRev(ctx, path, dep, rev)
}

// SortedModulePaths lists locked paths in order (for stable output).
func SortedModulePaths(l *Lock) []string {
	var out []string
	for _, e := range l.Entries {
		out = append(out, e.Path)
	}
	sort.Strings(out)
	return out
}
