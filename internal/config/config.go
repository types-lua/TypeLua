package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// CompilerOptions mirrors tsconfig.compilerOptions (subset).
type CompilerOptions struct {
	Target          string `json:"target"` // "5.4" | "5.1"
	Strict          *bool  `json:"strict"`
	StrictNilChecks *bool  `json:"strictNilChecks"`
	NoImplicitAny   *bool  `json:"noImplicitAny"`
	OutDir          string `json:"outDir"`
	RootDir         string `json:"rootDir"`
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
		return out, nil
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
		return out, nil
	}
	return all, nil
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
