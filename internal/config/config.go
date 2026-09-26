package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// CompilerOptions mirrors tsconfig.compilerOptions (subset).
type CompilerOptions struct {
	Target          string   `json:"target"` // "5.4" | "5.1"
	Strict          *bool    `json:"strict"`
	StrictNilChecks *bool    `json:"strictNilChecks"`
	NoImplicitAny   *bool    `json:"noImplicitAny"`
	OutDir          string   `json:"outDir"`
	RootDir         string   `json:"rootDir"`
	SourceMap       bool     `json:"sourceMap"`
	TreeShake       bool     `json:"treeShake,omitempty"`
	Types           []string `json:"types,omitempty"`     // ambient .d.tl allowlist (nil/empty = all)
	TypeRoots       []string `json:"typeRoots,omitempty"` // extra .d.tl roots (default: types + tlua_modules/@types)
}

// Config is tlua.json.
type Config struct {
	CompilerOptions CompilerOptions `json:"compilerOptions"`
	Include         []string        `json:"include"`
	Exclude         []string        `json:"exclude"`
	Files           []string        `json:"files"`
	Dir             string          `json:"-"` // config file directory
	Path            string          `json:"-"`
}

func Default(dir string) *Config {
	t := true
	_ = t
	strict := true
	return &Config{
		CompilerOptions: CompilerOptions{Target: "5.4", Strict: &strict, OutDir: "dist", RootDir: "."},
		Dir:             dir,
	}
}

// Effective values with defaults.
func (c *Config) Target() string {
	if c.CompilerOptions.Target == "" {
		return "5.4"
	}
	return c.CompilerOptions.Target
}
func (c *Config) Strict() bool {
	if c.CompilerOptions.Strict != nil {
		return *c.CompilerOptions.Strict
	}
	return true
}
func (c *Config) StrictNilChecks() bool {
	if c.CompilerOptions.StrictNilChecks != nil {
		return *c.CompilerOptions.StrictNilChecks
	}
	return c.Strict()
}
func (c *Config) NoImplicitAny() bool {
	if c.CompilerOptions.NoImplicitAny != nil {
		return *c.CompilerOptions.NoImplicitAny
	}
	return false
}
func (c *Config) OutDir() string {
	if c.CompilerOptions.OutDir == "" {
		return "dist"
	}
	return c.CompilerOptions.OutDir
}
func (c *Config) RootDir() string {
	if c.CompilerOptions.RootDir == "" {
		return "."
	}
	return c.CompilerOptions.RootDir
}

// TypeRoots lists extra ambient type roots (like tsconfig typeRoots).
// Relative entries resolve against the config directory; missing
// directories are ignored by ListFiles. Default covers the two
// conventional locations.
func (c *Config) TypeRoots() []string {
	if len(c.CompilerOptions.TypeRoots) > 0 {
		out := make([]string, 0, len(c.CompilerOptions.TypeRoots))
		for _, r := range c.CompilerOptions.TypeRoots {
			if !filepath.IsAbs(r) {
				r = filepath.Join(c.Dir, r)
			}
			out = append(out, r)
		}
		return out
	}
	return []string{
		filepath.Join(c.Dir, "types"),
		filepath.Join(c.Dir, "tlua_modules", "@types"),
	}
}

// AmbientTypes returns the `types` allowlist (nil/empty = include all
// ambient .d.tl files, mirroring tsconfig behavior).
func (c *Config) AmbientTypes() []string {
	return c.CompilerOptions.Types
}

// IsAmbientIncluded reports whether a .d.tl path participates as ambient
// under the given allowlist. Empty allowlist includes everything.
func IsAmbientIncluded(path string, allow []string) bool {
	if len(allow) == 0 {
		return true
	}
	slash := filepath.ToSlash(path)
	base := filepath.Base(slash) // e.g. foo.d.tl
	name := strings.TrimSuffix(base, ".d.tl")
	name = strings.TrimSuffix(name, ".tl")
	dirBase := filepath.Base(filepath.Dir(slash))
	for _, t := range allow {
		t = strings.TrimSpace(t)
		if t == "" {
			continue
		}
		if name == t || dirBase == t {
			return true
		}
		// Conventional layouts: .../@types/<t>/..., .../types/<t>...
		if strings.Contains(slash, "/@types/"+t+"/") || strings.Contains(slash, "/types/"+t+"/") {
			return true
		}
		if strings.HasSuffix(slash, "/@types/"+t+".d.tl") || strings.HasSuffix(slash, "/types/"+t+".d.tl") {
			return true
		}
	}
	return false
}

// Load reads tlua.json at path.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	cfg := Default(filepath.Dir(path))
	if err := json.Unmarshal(data, cfg); err != nil {
		return nil, err
	}
	cfg.Dir = filepath.Dir(path)
	cfg.Path = path
	return cfg, nil
}

// FindUp searches for tlua.json from dir upward.
func FindUp(dir string) string {
	abs, err := filepath.Abs(dir)
	if err != nil {
		abs = dir
	}
	for {
		p := filepath.Join(abs, "tlua.json")
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

// ListFiles resolves files per include/exclude/files.
// Type roots contribute ambient *.d.tl files even when outside include
// (like tsconfig typeRoots); missing roots are ignored. Callers dedupe.
func (c *Config) ListFiles() ([]string, error) {
	root := filepath.Join(c.Dir, c.RootDir())
	if len(c.Files) > 0 {
		var out []string
		for _, f := range c.Files {
			p := f
			if !filepath.IsAbs(p) {
				p = filepath.Join(c.Dir, p)
			}
			out = append(out, p)
		}
		return append(out, c.listTypeRootDecls(out)...), nil
	}
	// Walk root for .tl files.
	var all []string
	err := filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			// Skip excluded dirs + common ignores.
			rel, _ := filepath.Rel(c.Dir, p)
			if rel == c.OutDir() || rel == "dist" || rel == ".git" || rel == "node_modules" {
				// Allow if explicitly included? Keep skip for defaults.
				if len(c.Exclude) == 0 {
					return filepath.SkipDir
				}
			}
			if matchAny(c.Exclude, rel) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(p, ".tl") {
			return nil
		}
		rel, _ := filepath.Rel(c.Dir, p)
		if matchAny(c.Exclude, rel) {
			return nil
		}
		all = append(all, p)
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(c.Include) > 0 {
		var out []string
		for _, f := range all {
			rel, _ := filepath.Rel(c.Dir, f)
			if matchAny(c.Include, rel) {
				out = append(out, f)
			}
		}
		return append(out, c.listTypeRootDecls(out)...), nil
	}
	return append(all, c.listTypeRootDecls(all)...), nil
}

// ListNativeAssets lists static cross-language assets (*.lua shims,
// *.so/*.dylib/*.dll shared libraries, *.a static archives) under RootDir
// and tlua_modules, excluding outDir.
// Headers/docs never list; missing dirs are ignored. Used by build Trois
// passthrough and watch triggering alike so both agree on the input set.
func (c *Config) ListNativeAssets() ([]string, error) {
	outAbs := c.OutDir()
	if !filepath.IsAbs(outAbs) {
		outAbs = filepath.Join(c.Dir, outAbs)
	}
	outAbs = filepath.Clean(outAbs)
	var out []string
	seen := map[string]bool{}
	addWalk := func(root string) {
		_ = filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
			if err != nil {
				return nil
			}
			if info.IsDir() {
				clean := filepath.Clean(p)
				if clean == outAbs || strings.HasPrefix(clean+string(os.PathSeparator), outAbs+string(os.PathSeparator)) {
					return filepath.SkipDir
				}
				// Mirror ListFiles: a stale default `dist/` from an older
				// outDir must never be vacuumed back in as source assets.
				if rel, err := filepath.Rel(c.Dir, p); err == nil && rel == "dist" {
					return filepath.SkipDir
				}
				if info.Name() == ".git" || info.Name() == "node_modules" {
					return filepath.SkipDir
				}
				return nil
			}
			if strings.HasPrefix(info.Name(), ".") || !isNativeAssetName(info.Name()) {
				return nil
			}
			clean := filepath.Clean(p)
			if seen[clean] {
				return nil
			}
			seen[clean] = true
			out = append(out, p)
			return nil
		})
	}
	addWalk(filepath.Join(c.Dir, c.RootDir()))
	vendor := filepath.Join(c.Dir, "tlua_modules")
	// Vendor outside RootDir needs its own walk (inside is already covered).
	if filepath.Clean(vendor) != filepath.Clean(filepath.Join(c.Dir, c.RootDir())) {
		addWalk(vendor)
	}
	return out, nil
}

func isNativeAssetName(name string) bool {
	lower := strings.ToLower(name)
	return strings.HasSuffix(lower, ".lua") ||
		strings.HasSuffix(lower, ".so") ||
		strings.HasSuffix(lower, ".dylib") ||
		strings.HasSuffix(lower, ".dll") ||
		// Static archives travel the same passthrough/vendor/watch path as
		// shared libs (link into the host at build time, then ffi.C at
		// runtime). They never enter package.cpath (see bootstrap.go).
		strings.HasSuffix(lower, ".a")
}

// listTypeRootDecls finds ambient *.d.tl under typeRoots not already listed.
// Only declaration files are included (never emitted); excludes apply.
func (c *Config) listTypeRootDecls(have []string) []string {
	seen := map[string]bool{}
	for _, p := range have {
		seen[filepath.Clean(p)] = true
	}
	var out []string
	for _, root := range c.TypeRoots() {
		_ = filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
			if err != nil {
				return nil
			}
			if info.IsDir() {
				if info.Name() == ".git" || info.Name() == "node_modules" {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(p, ".d.tl") {
				return nil
			}
			if seen[filepath.Clean(p)] {
				return nil
			}
			if rel, err := filepath.Rel(c.Dir, p); err == nil {
				if matchAny(c.Exclude, filepath.ToSlash(rel)) {
					return nil
				}
			}
			seen[filepath.Clean(p)] = true
			out = append(out, p)
			return nil
		})
	}
	return out
}

func matchAny(patterns []string, rel string) bool {
	rel = filepath.ToSlash(rel)
	for _, pat := range patterns {
		if matchGlob(pat, rel) {
			return true
		}
	}
	return false
}

// matchGlob supports *, ?, ** and character classes.
func matchGlob(pat, s string) bool {
	pat = filepath.ToSlash(pat)
	// Fast path: exact or dir prefix.
	if pat == s {
		return true
	}
	return globMatch(pat, s)
}

func globMatch(pat, s string) bool {
	px, sx := 0, 0
	var starPat, starStr = -1, -1
	// Handle ** separately by splitting.
	if strings.Contains(pat, "**") {
		parts := strings.Split(pat, "**")
		// All parts must appear in order.
		pos := 0
		for i, part := range parts {
			part = strings.Trim(part, "/")
			if part == "" {
				continue
			}
			if i == 0 && !strings.HasPrefix(pat, "**") {
				// Must match prefix.
				if !prefixGlob(part, s[pos:]) {
					return false
				}
				pos += globPrefixLen(part, s[pos:])
				continue
			}
			idx := indexGlob(part, s[pos:])
			if idx < 0 {
				return false
			}
			pos += idx + globPrefixLen(part, s[pos+idx:])
		}
		// Last part must match suffix unless pat ends with **.
		if !strings.HasSuffix(pat, "**") {
			last := strings.Trim(parts[len(parts)-1], "/")
			if last != "" && !suffixGlob(last, s) {
				return false
			}
		}
		return true
	}
	// No **: use path.Match on each segment? Fall back to segment-wise.
	pSeg := strings.Split(pat, "/")
	sSeg := strings.Split(s, "/")
	if len(pSeg) != len(sSeg) {
		// Allow trailing * to match? No.
		return false
	}
	for i := range pSeg {
		ok, _ := pathMatch(pSeg[i], sSeg[i])
		if !ok {
			return false
		}
	}
	_ = px
	_ = sx
	_ = starPat
	_ = starStr
	return true
}

func pathMatch(pat, s string) (bool, error) {
	// filepath.Match without separator concerns (single segment).
	return filepath.Match(pat, s)
}

func prefixGlob(pat, s string) bool {
	// pat is prefix up to next /.
	seg := pat
	if i := strings.Index(pat, "/"); i >= 0 {
		seg = pat[:i]
	}
	if len(s) < len(seg) {
		return false
	}
	ok, _ := filepath.Match(seg, s[:len(seg)])
	if !ok {
		// Try segment-wise.
		sSeg := strings.SplitN(s, "/", 2)[0]
		ok, _ = filepath.Match(seg, sSeg)
		return ok
	}
	return true
}

func globPrefixLen(pat, s string) int {
	// Length consumed by pat prefix in s (approx: up to end of matched segment).
	if i := strings.Index(pat, "/"); i >= 0 {
		seg := pat[:i]
		sSeg := strings.SplitN(s, "/", 2)[0]
		_ = seg
		_ = sSeg
		return len(sSeg) + 1
	}
	return len(pat)
}

func indexGlob(pat, s string) int {
	seg := strings.Split(pat, "/")[0]
	sSegs := strings.Split(s, "/")
	for i, ss := range sSegs {
		ok, _ := filepath.Match(seg, ss)
		if ok {
			// Compute byte offset.
			off := 0
			for j := 0; j < i; j++ {
				off += len(sSegs[j]) + 1
			}
			return off
		}
	}
	return -1
}

func suffixGlob(pat, s string) bool {
	seg := pat
	if i := strings.LastIndex(pat, "/"); i >= 0 {
		seg = pat[i+1:]
	}
	sSeg := s
	if i := strings.LastIndex(s, "/"); i >= 0 {
		sSeg = s[i+1:]
	}
	ok, _ := filepath.Match(seg, sSeg)
	return ok
}
