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

// CommitFile 记录固定的 commit（vendor 标记）。
const CommitFile = ".tlua-commit"

// ModCacheEnv 覆盖模块缓存位置。
const ModCacheEnv = "TLUA_MODCACHE"

// OfflineEnv 禁用所有网络使用（仅缓存/镜像）。
const OfflineEnv = "TLUA_OFFLINE"

// DefaultDir 返回模块缓存目录。
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

// Cache 存放镜像与解压后的模块树。
//
// 物理布局：
//
//	Cache
//	├── Root    string // 缓存根目录
//	└── Offline bool   // 是否禁止网络
type Cache struct {
	// Root 是模块缓存根目录（~/.tlua/pkg/mod 或 TLUA_MODCACHE）。
	Root string
	// Offline 为真时禁止网络访问，只复用已有镜像/树。
	Offline bool
}

// NewCache 创建缓存，读取 TLUA_OFFLINE。
func NewCache(root string) *Cache {
	off := false
	if v := strings.TrimSpace(os.Getenv(OfflineEnv)); v != "" && v != "0" && !strings.EqualFold(v, "false") {
		off = true
	}
	return &Cache{Root: root, Offline: off}
}

// escapePath 把模块路径映射为缓存目录（Go 风格：
// 大写字母变为 `!`+小写；斜杠保留）。
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

// mirrorDir 命名 URL 的裸镜像（内容寻址）。
func (c *Cache) mirrorDir(url string) string {
	sum := sha256.Sum256([]byte(url))
	return filepath.Join(c.Root, "cache", "download", fmt.Sprintf("%x", sum))
}

// MirrorURL 在镜像旁记录源 URL（调试辅助）。
func (c *Cache) MirrorURL(mirrorDir string) string {
	raw, err := os.ReadFile(filepath.Join(mirrorDir, "tlua-url"))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(raw))
}

// Sync 克隆或刷新 url 的镜像，返回其目录。
// 离线模式复用已有镜像，否则报错。
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

// TreeDir 返回 path@version 的解压树目录。
func (c *Cache) TreeDir(path, version string) string {
	return filepath.Join(c.Root, escapePath(path)+"@"+version)
}

// TreeCommit 读取解压树记录的 commit（不存在返回 ""）。
func (c *Cache) TreeCommit(dir string) string {
	raw, err := os.ReadFile(filepath.Join(dir, CommitFile))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(raw))
}

// EnsureTree 把 url@commit 物化到树目录（验证复用，
// 通过临时目录 + 重命名原子替换）。返回树目录。
func (c *Cache) EnsureTree(ctx context.Context, url, path, version, commit string) (string, error) {
	dst := c.TreeDir(path, version)
	if c.TreeCommit(dst) == commit {
		return dst, nil
	}
	mirror, err := c.Sync(ctx, url)
	if err != nil {
		return "", err
	}
	// 解析（并规范化）相对于镜像的 commit。
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

// CandidateURLs 为模块路径导出获取 URL，最长优先：
// 完整路径，然后逐步缩短前缀（最少 host + 1）。
// 调用方逐个尝试直到有响应。
func CandidateURLs(path string) []string {
	segs := strings.Split(path, "/")
	var out []string
	for n := len(segs); n >= 2; n-- {
		out = append(out, "https://"+strings.Join(segs[:n], "/"))
	}
	return out
}

// ProbeURL 通过最长前缀探测为 path 找仓库根。
// 返回 repo URL（.git 后缀由 git 自身处理）。
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

// GitRegistry 通过缓存解析并读取模块。
type GitRegistry struct {
	// Cache 是底层镜像/树缓存。
	Cache *Cache
	// probes 备忘录：进程内已发现的 repo URL，避免重复探测。
	probes map[string]string
}

func (g *GitRegistry) urlFor(ctx context.Context, path string, dep Dep) (string, error) {
	if g.probes == nil {
		g.probes = map[string]string{}
	}
	// 显式覆盖优先且固定（每路径一个源；解析器在我们看到前
	// 就拒绝冲突）。
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

// Tags 通过镜像标签列表实现 Registry。
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

// Manifest 通过读取 commit 处的 tlua.toml 实现 Registry
// （文件缺失 = 叶模块，nil manifest）。
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
		// 缺失 manifest：视为无依赖的叶模块。
		return nil, nil
	}
	m, err := ParseManifest(string(raw))
	if err != nil {
		return nil, fmt.Errorf("bad %s in %s@%s: %v", ManifestName, path, commit, err)
	}
	return m, nil
}

// ResolveRev 实现 Registry。
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

// LocalDir 实现 Registry（基础 registry 无本地内容）。
func (g *GitRegistry) LocalDir(_ string) (string, bool) {
	return "", false
}

// replaceRegistry 覆盖本地目录替换，并记录传递性 path 链接
// （相对于每个 manifest 自身目录，Cargo 风格）供本次解析运行。
type replaceRegistry struct {
	// base 是被包装的底层 Registry。
	base Registry
	// replace 是根 manifest 的 replace 指令（项目相对路径）。
	replace map[string]string
	// rootDir 是项目根目录，用于解析相对 replace 目标。
	rootDir string
	// recorded 是解析过程中发现的传递性 path 链接（绝对目录）。
	recorded map[string]string
}

// ReplaceRegistry 用 replace 指令包装 reg。即使为空也包装，
// 使传递性 path 链接统一记录。
func ReplaceRegistry(reg Registry, replace map[string]string, rootDir string) Registry {
	if replace == nil {
		replace = map[string]string{}
	}
	return &replaceRegistry{base: reg, replace: replace, rootDir: rootDir, recorded: map[string]string{}}
}

func (r *replaceRegistry) localDir(path string) (string, bool) {
	if tgt, ok := r.replace[path]; ok {
		if !filepath.IsAbs(tgt) {
			tgt = filepath.Join(r.rootDir, tgt)
		}
		return tgt, true
	}
	if tgt, ok := r.recorded[path]; ok {
		return tgt, true
	}
	return "", false
}

// LocalDir 实现 Registry。
func (r *replaceRegistry) LocalDir(path string) (string, bool) {
	return r.localDir(path)
}

func (r *replaceRegistry) readLocal(path, dir string) *Manifest {
	m, err := r.readLocalStrict(path, dir)
	if err != nil {
		return NewManifest(path)
	}
	return m
}

// readLocalStrict 读取并校验本地 manifest（错误显式）。
func (r *replaceRegistry) readLocalStrict(path, dir string) (*Manifest, error) {
	raw, err := os.ReadFile(filepath.Join(dir, ManifestName))
	if err != nil {
		return nil, err
	}
	m, err := ParseManifest(string(raw))
	if err != nil {
		return nil, fmt.Errorf("bad %s in %s: %v", ManifestName, dir, err)
	}
	m.Name = path
	return m, nil
}

// recordLinks 记住相对于 manifest 自身目录的传递性 path 链接
// （Cargo 风格相对路径）。
func (r *replaceRegistry) recordLinks(dir string, m *Manifest) {
	for path, dep := range m.Deps {
		if dep.Path == "" {
			continue
		}
		if _, ok := r.replace[path]; ok {
			continue // 根 replace 优先
		}
		tgt := dep.Path
		if !filepath.IsAbs(tgt) {
			tgt = filepath.Join(dir, tgt)
		}
		r.recorded[path] = tgt
	}
}

// Tags 实现 Registry：本地替换返回本地版本，否则委托 base。
func (r *replaceRegistry) Tags(ctx context.Context, path string, dep Dep) (map[string]string, error) {
	if dir, ok := r.localDir(path); ok {
		m := r.readLocal(path, dir)
		ver := m.Version
		if _, err := ParseVersion(ver); err != nil {
			ver = "v0.0.0-local"
		}
		return map[string]string{ver: "local"}, nil
	}
	return r.base.Tags(ctx, path, dep)
}

// Manifest 实现 Registry：本地替换读本地 manifest 并记录传递链接。
func (r *replaceRegistry) Manifest(ctx context.Context, path, commit string) (*Manifest, error) {
	if dir, ok := r.localDir(path); ok {
		m, err := r.readLocalStrict(path, dir)
		if err != nil {
			return nil, err
		}
		r.recordLinks(dir, m)
		return m, nil
	}
	m, err := r.base.Manifest(ctx, path, commit)
	if err != nil || m == nil {
		return m, err
	}
	// 远程 manifest：path 链接只与版本组合（Cargo 发布规则）；
	// 裸的显式命名其修正。操作副本
	//（registry 可能共享 manifest 对象）。
	cp := &Manifest{Name: m.Name, Version: m.Version, Deps: map[string]*Dep{}, Replace: m.Replace}
	for dp, dd := range m.Deps {
		nd := &Dep{Query: dd.Query, URL: dd.URL}
		if dd.Path != "" {
			if dd.Query == "" {
				return nil, fmt.Errorf("path dependency %q of %q needs a version to be usable transitively", dp, path)
			}
			// 版本远程治理；path 保持仅本地。
		}
		cp.Deps[dp] = nd
	}
	return cp, nil
}

// ResolveRev 实现 Registry：本地替换直接返回 "local"。
func (r *replaceRegistry) ResolveRev(ctx context.Context, path string, dep Dep, rev string) (string, error) {
	if _, ok := r.localDir(path); ok {
		return "local", nil
	}
	return r.base.ResolveRev(ctx, path, dep, rev)
}

// SortedModulePaths 按顺序列出锁定路径（稳定输出）。
func SortedModulePaths(l *Lock) []string {
	var out []string
	for _, e := range l.Entries {
		out = append(out, e.Path)
	}
	sort.Strings(out)
	return out
}
