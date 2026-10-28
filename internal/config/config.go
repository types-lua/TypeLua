package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// CompilerOptions 镜像 tsconfig.compilerOptions（子集）。
type CompilerOptions struct {
	Target          string   `json:"target"` // "5.4" | "5.1"
	Strict          *bool    `json:"strict"`
	StrictNilChecks *bool    `json:"strictNilChecks"`
	NoImplicitAny   *bool    `json:"noImplicitAny"`
	OutDir          string   `json:"outDir"`
	RootDir         string   `json:"rootDir"`
	SourceMap       bool     `json:"sourceMap"`
	TreeShake       bool     `json:"treeShake,omitempty"`
	Types           []string `json:"types,omitempty"`     // ambient .d.tl 允许列表（nil/空 = 全部）
	TypeRoots       []string `json:"typeRoots,omitempty"` // 额外 .d.tl 根（默认：types + tlua_modules/@types）
}

// Config 是 tlua.json 的内存表示。
//
// 物理布局：
//
//	Config
//	├── CompilerOptions  // 目标/严格/输出/根目录/SourceMap/TreeShake/Types/TypeRoots
//	├── Include []string // 包含 glob
//	├── Exclude []string // 排除 glob
//	├── Files   []string // 显式文件列表（优先）
//	├── Dir     string   // 配置文件所在目录（不序列化）
//	└── Path    string   // 配置文件绝对路径（不序列化）
type Config struct {
	// CompilerOptions 是 tlua.json 的 compilerOptions 子集。
	CompilerOptions CompilerOptions `json:"compilerOptions"`
	// Include 是包含模式列表（glob）。
	Include []string `json:"include"`
	// Exclude 是排除模式列表（glob）。
	Exclude []string `json:"exclude"`
	// Files 是显式文件列表（优先于 include/exclude）。
	Files []string `json:"files"`
	// Dir 是配置文件所在目录（不序列化）。
	Dir string `json:"-"`
	// Path 是配置文件绝对路径（不序列化）。
	Path string `json:"-"`
}

// Default 返回一个带默认值的 Config（target 5.4、strict、outDir dist、rootDir .）。
func Default(dir string) *Config {
	t := true
	_ = t
	strict := true
	return &Config{
		CompilerOptions: CompilerOptions{Target: "5.4", Strict: &strict, OutDir: "dist", RootDir: "."},
		Dir:             dir,
	}
}

// 带默认值的有效值。
// Target 返回有效目标 Lua 版本（默认 5.4）。
func (c *Config) Target() string {
	if c.CompilerOptions.Target == "" {
		return "5.4"
	}
	return c.CompilerOptions.Target
}

// Strict 返回是否启用严格模式（默认 true）。
func (c *Config) Strict() bool {
	if c.CompilerOptions.Strict != nil {
		return *c.CompilerOptions.Strict
	}
	return true
}

// StrictNilChecks 返回是否启用严格 nil 检查（默认继承 Strict）。
func (c *Config) StrictNilChecks() bool {
	if c.CompilerOptions.StrictNilChecks != nil {
		return *c.CompilerOptions.StrictNilChecks
	}
	return c.Strict()
}

// NoImplicitAny 返回是否禁止隐式 any（默认 false）。
func (c *Config) NoImplicitAny() bool {
	if c.CompilerOptions.NoImplicitAny != nil {
		return *c.CompilerOptions.NoImplicitAny
	}
	return false
}

// OutDir 返回有效输出目录（默认 dist）。
func (c *Config) OutDir() string {
	if c.CompilerOptions.OutDir == "" {
		return "dist"
	}
	return c.CompilerOptions.OutDir
}

// RootDir 返回有效源根目录（默认 .）。
func (c *Config) RootDir() string {
	if c.CompilerOptions.RootDir == "" {
		return "."
	}
	return c.CompilerOptions.RootDir
}

// TypeRoots 列出额外的 ambient 类型根（类似 tsconfig typeRoots）。
// 相对条目相对于配置目录解析；缺失目录被 ListFiles 忽略。
// 默认覆盖两个常规位置。
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

// AmbientTypes 返回 `types` 允许列表（nil/空 = 包含所有
// ambient .d.tl 文件，镜像 tsconfig 行为）。
func (c *Config) AmbientTypes() []string {
	return c.CompilerOptions.Types
}

// IsAmbientIncluded 报告 .d.tl 路径在给定允许列表下是否作为 ambient 参与。
// 空允许列表包含全部。
func IsAmbientIncluded(path string, allow []string) bool {
	if len(allow) == 0 {
		return true
	}
	slash := filepath.ToSlash(path)
	base := filepath.Base(slash) // 例如 foo.d.tl
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
		// 常规布局：.../@types/<t>/...、.../types/<t>...
		if strings.Contains(slash, "/@types/"+t+"/") || strings.Contains(slash, "/types/"+t+"/") {
			return true
		}
		if strings.HasSuffix(slash, "/@types/"+t+".d.tl") || strings.HasSuffix(slash, "/types/"+t+".d.tl") {
			return true
		}
	}
	return false
}

// Load 在 path 读取 tlua.json。
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

// FindUp 从 dir 向上搜索 tlua.json。
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

// ListFiles 按 include/exclude/files 解析文件。
// 类型根贡献 ambient *.d.tl 文件，即使在 include 之外
// （类似 tsconfig typeRoots）；缺失根被忽略。调用方去重。
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
	// 遍历 root 查找 .tl 文件。
	var all []string
	err := filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			// 跳过排除目录 + 常见忽略。
			rel, _ := filepath.Rel(c.Dir, p)
			if rel == c.OutDir() || rel == "dist" || rel == ".git" || rel == "node_modules" {
				// 允许显式包含？默认保持跳过。
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

// ListNativeAssets 列出 RootDir 与 tlua_modules 下的静态跨语言资产
// （*.lua shim、*.so/*.dylib/*.dll 共享库、*.a 静态归档），排除 outDir。
// 头文件/文档从不列出；缺失目录忽略。构建直通与 watch 触发
// 共用，使双方对输入集一致。
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
				// 镜像 ListFiles：来自旧 outDir 的陈旧默认 `dist/`
				// 绝不能作为源资产被吸回。
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
	// RootDir 之外的 vendor 需要单独遍历（内部已覆盖）。
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
		// 静态归档与共享库走相同的直通/vendor/watch 路径
		//（构建时链接到宿主，运行时 ffi.C）。
		// 它们从不进入 package.cpath（见 bootstrap.go）。
		strings.HasSuffix(lower, ".a")
}

// listTypeRootDecls 查找 typeRoots 下尚未列出的 ambient *.d.tl。
// 只包含声明文件（从不发射）；排除规则适用。
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

// matchGlob 支持 *、?、** 与字符类。
func matchGlob(pat, s string) bool {
	pat = filepath.ToSlash(pat)
	// 快路径：精确或目录前缀。
	if pat == s {
		return true
	}
	return globMatch(pat, s)
}

func globMatch(pat, s string) bool {
	px, sx := 0, 0
	var starPat, starStr = -1, -1
	// ** 单独处理：拆分。
	if strings.Contains(pat, "**") {
		parts := strings.Split(pat, "**")
		// 所有部分必须按顺序出现。
		pos := 0
		for i, part := range parts {
			part = strings.Trim(part, "/")
			if part == "" {
				continue
			}
			if i == 0 && !strings.HasPrefix(pat, "**") {
				// 必须匹配前缀。
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
		// 除非 pat 以 ** 结尾，最后一部分必须匹配后缀。
		if !strings.HasSuffix(pat, "**") {
			last := strings.Trim(parts[len(parts)-1], "/")
			if last != "" && !suffixGlob(last, s) {
				return false
			}
		}
		return true
	}
	// 无 **：对每段用 path.Match？回退到分段匹配。
	pSeg := strings.Split(pat, "/")
	sSeg := strings.Split(s, "/")
	if len(pSeg) != len(sSeg) {
		// 允许尾随 * 匹配？否。
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
	// filepath.Match 不考虑分隔符（单段）。
	return filepath.Match(pat, s)
}

func prefixGlob(pat, s string) bool {
	// pat 是到下一个 / 的前缀。
	seg := pat
	if i := strings.Index(pat, "/"); i >= 0 {
		seg = pat[:i]
	}
	if len(s) < len(seg) {
		return false
	}
	ok, _ := filepath.Match(seg, s[:len(seg)])
	if !ok {
		// 尝试分段匹配。
		sSeg := strings.SplitN(s, "/", 2)[0]
		ok, _ = filepath.Match(seg, sSeg)
		return ok
	}
	return true
}

func globPrefixLen(pat, s string) int {
	// pat 前缀在 s 中消耗的长度（近似：到匹配段末尾）。
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
			// 计算字节偏移。
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
