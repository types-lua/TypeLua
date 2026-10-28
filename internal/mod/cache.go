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
//
// 设计者视角：缓存根三级——显式 TLUA_MODCACHE 优先，余落 `~/.tlua/pkg/mod`。
// 障碍是无 HOME：报错指引设 env。候选：相对 vs 家目录；选家目录全局共享。不变量：
// 成功恒非空。排查现场：缓存错位先查 env。
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
// 设计者视角：Cache 是“仓库+货架”——mirrorDir 存裸镜像（内容寻址防 URL 特殊符），
// TreeDir 存 `path@version` 解压树，Offline 闸断网。障碍是 URL 含特殊符作目录名
// 必崩；故 sha256 内容寻址。候选：URL 明文 vs 哈希；选哈希。不变量：Offline 下零
// 网络。排查现场：离线失败先查 Offline 旗。
//
// 物理布局：
//
//	├── Root    string // 缓存根目录
//	└── Offline bool   // 是否禁止网络
type Cache struct {
	// Root 是模块缓存根目录（~/.tlua/pkg/mod 或 TLUA_MODCACHE）。
	Root string
	// Offline 为真时禁止网络访问，只复用已有镜像/树。
	Offline bool
}

// NewCache 创建缓存，读取 TLUA_OFFLINE。
//
// 设计者视角：Offline 判宽松——非空且非 `0`/`false` 即离线。障碍是 env 大小写：
// EqualFold 容 `False`。候选：严 vs 宽；选宽。不变量：纯造。排查现场：离线未生效
// 先查 env 值。
func NewCache(root string) *Cache {
	off := false
	if v := strings.TrimSpace(os.Getenv(OfflineEnv)); v != "" && v != "0" && !strings.EqualFold(v, "false") {
		off = true
	}
	return &Cache{Root: root, Offline: off}
}

// escapePath 把模块路径映射为缓存目录（Go 风格：
// 大写字母变为 `!`+小写；斜杠保留）。
//
// 设计者视角：逃逸保大小写文件系统可共存（`Foo` vs `foo`），学 Go mod。障碍是
// Windows 保留字：斜杠保留、余原样，够用。排查现场：缓存碰撞先查此。
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
//
// 设计者视角：sha256(URL) 作目录，防特殊符与过长。障碍是 URL 别名（同仓多名）：
// 多镜像并存，耗盘换正确。排查现场：镜像膨胀先查别名。
func (c *Cache) mirrorDir(url string) string {
	sum := sha256.Sum256([]byte(url))
	return filepath.Join(c.Root, "cache", "download", fmt.Sprintf("%x", sum))
}

// MirrorURL 在镜像旁记录源 URL（调试辅助）。
//
// 设计者视角：`tlua-url` 旁注源址， humans 可查。障碍是旧镜像无文件：返 "" 不错。
// 排查现场：镜像来源不明先看此文件。
func (c *Cache) MirrorURL(mirrorDir string) string {
	raw, err := os.ReadFile(filepath.Join(mirrorDir, "tlua-url"))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(raw))
}

// Sync 克隆或刷新 url 的镜像，返回其目录。
// 离线模式复用已有镜像，否则报错。
//
// 设计者视角：Sync 是镜像“有无分流”——有 HEAD 则在线 remote update、离线直返，
// 无则离线报错、在线 clone+记 url。障碍是 git 缺失：首判 GitAvailable。候选：
// fetch vs update；选 update 保全。不变量：返径恒有 HEAD。排查现场：离线缺镜像
// 先看错文，更新失败先查网络。
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
//
// 设计者视角：树径=逃逸径+`@`+版，版含 `local` 亦可。障碍是版含 `/`：上游版恒
// semver/`local`，无 `/`。排查现场：树错位先查逃逸。
func (c *Cache) TreeDir(path, version string) string {
	return filepath.Join(c.Root, escapePath(path)+"@"+version)
}

// TreeCommit 读取解压树记录的 commit（不存在返回 ""）。
//
// 设计者视角：`.tlua-commit` 即树指纹，缺即 "" 触发重物化。障碍是手改树：指纹不
// 对即重铺。排查现场：反复重下先查指纹文件。
func (c *Cache) TreeCommit(dir string) string {
	raw, err := os.ReadFile(filepath.Join(dir, CommitFile))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(raw))
}

// EnsureTree 把 url@commit 物化到树目录（验证复用，
// 通过临时目录 + 重命名原子替换）。返回树目录。
//
// 设计者视角：两阶验复用（短 commit 与 RevParse 全 commit 各验一次）+临时解压+
// 写指纹+原子 rename。障碍是并发半写：tmp+rename 保原子；障碍是短 SHA：RevParse
// 规范化再验。候选：直写 vs 原子；选原子。不变量：出口树指纹==full。排查现场：
// 半写残留先查 tmp 清理。
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
//
// 设计者视角：去中心化寻仓——`a/b/c` 先试全径、再 `a/b`，首命中即仓根（见
// ProbeURL）。障碍是单段径：无候选（n>=2）。候选：DNS TXT vs 路径试探；选路径试探
// 零外部。不变量：首候选恒全径。排查现场：仓根错先查前缀序。
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
//
// 设计者视角：逐候选 LsRemote 嗅探，首通即返（errs 仅聚错文）。障碍是离线：
// LsRemote 必败，上游 offline 已拦。候选：clone 试 vs ls-remote；选后者轻。
// 不变量：错含尝试数。排查现场：探测慢先查候选数。
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
//
// 设计者视角：Registry 的 git 实现——probes 备忘仓根，urlFor 定源（显式 URL 钉死），
// Tags/Manifest/ResolveRev 经 Sync 镜像办。障碍是显式 URL 冲突：解析器前置拒，
// 此处钉死。候选：每次探测 vs 备忘；选备忘。不变量：同 path 同 URL。排查现场：
// 源漂移先查 probes。
type GitRegistry struct {
	// Cache 是底层镜像/树缓存。
	Cache *Cache
	// probes 备忘录：进程内已发现的 repo URL，避免重复探测。
	probes map[string]string
}

func (g *GitRegistry) urlFor(ctx context.Context, path string, dep Dep) (string, error) {
	// 设计者视角：显式 URL 钉死并备忘，余查备忘、离线拦、在线探。障碍是离线无备忘：
	// 须显式 URL 或先在线。排查现场：离线失败先查备忘。
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
//
// 设计者视角：定源→Sync 镜像→MirrorTags 列版。障碍是空仓：空图。排查现场：版缺失
// 先查镜像更新。
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
//
// 设计者视角：按 commit 读 manifest，缺即叶（nil），坏即错。障碍是无 manifest 仓：
// 叶模块零依赖合法。候选：报错 vs 叶；选叶。不变量：缺恒 nil。排查现场：依赖凭空
// 消失先查 ShowFile。
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
//
// 设计者视角：任意 rev详见实现，排查看 Span。
// 解析即快照，lock 钉之。排查现场：分支漂移先查 lock。
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
//
// 设计者视角：基 registry 无本地，恒 false；本地能力由 ReplaceRegistry 叠加。
// 障碍是分层：base 不知 replace。排查现场：本地未生效先查包装。
func (g *GitRegistry) LocalDir(_ string) (string, bool) {
	return "", false
}

// replaceRegistry 覆盖本地目录替换，并记录传递性 path 链接
// （相对于每个 manifest 自身目录，Cargo 风格）供本次解析运行。
//
// 设计者视角：两表定本地——replace（根显式）优先，recorded（传递 path 链）次之，
// 相对径按各自 manifest 目录解。障碍是相对基准：根用 rootDir，传递用其 manifest
// dir（见 recordLinks）。候选：绝对化存 vs 相对存；选绝对化内存+相对化 lock。
// 不变量：根优先。排查现场：本地错位先查基准。
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
//
// 设计者视角：恒包一层，统一传递链记录口（空亦包）。障碍是 nil 图：代空图。
// 不变量：永非 nil。排查现场：传递 path 丢失先查是否绕过包装。
func ReplaceRegistry(reg Registry, replace map[string]string, rootDir string) Registry {
	if replace == nil {
		replace = map[string]string{}
	}
	return &replaceRegistry{base: reg, replace: replace, rootDir: rootDir, recorded: map[string]string{}}
}

func (r *replaceRegistry) localDir(path string) (string, bool) {
	// 设计者视角：根表先、记录次，相对拼 rootDir。障碍是绝对径：直返。排查现场：
	// 相对错先查 rootDir。
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
//
// 设计者视角：薄转 localDir，保接口。排查现场：同上。
func (r *replaceRegistry) LocalDir(path string) (string, bool) {
	return r.localDir(path)
}

func (r *replaceRegistry) readLocal(path, dir string) *Manifest {
	// 设计者视角：坏本地 manifest 兜底空壳（保解析不断），严格版另有 readLocalStrict
	// 报错。障碍是容错 vs 显错：Tags 路容错，解析路显错。排查现场：版错先查此处兜底。
	m, err := r.readLocalStrict(path, dir)
	if err != nil {
		return NewManifest(path)
	}
	return m
}

// readLocalStrict 读取并校验本地 manifest（错误显式）。
//
// 设计者视角：读+Parse+改名（Name=path），错透。障碍是 Name 须为请求径而非文件内名。
// 排查现场：名错先查此覆盖。
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
//
// 设计者视角：扫 manifest Deps 的 Path，根已覆跳过，余按 manifest dir 解绝对存录。
// 障碍是根优先：已 replace 不记。排查现场：传递链错先查 dir 基。
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
//
// 设计者视角：本地版读 manifest，坏版兜 `v0.0.0-local` 保可选。障碍是无版：兜底。
// 排查现场：本地版错先查 manifest Version。
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
//
// 设计者视角：本地读+记链；远程取后须“洗 path”（见注释）：path 依赖须带版方传，
// 拷副本防污染共享。障碍是裸 path 传递：无版即错（Cargo 发布律）。候选：透传 vs
// 洗；选洗。不变量：返前必记链（本地）/洗（远程）。排查现场：远程 path 错先查洗。
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
//
// 设计者视角：本地无 commit，以 "local" 占位（lock 记之，Dir 另存真址）。障碍是
// 版选：Tags 给版，此处给 commit 占位。排查现场：本地 commit 异先查此。
func (r *replaceRegistry) ResolveRev(ctx context.Context, path string, dep Dep, rev string) (string, error) {
	if _, ok := r.localDir(path); ok {
		return "local", nil
	}
	return r.base.ResolveRev(ctx, path, dep, rev)
}

// SortedModulePaths 按顺序列出锁定路径（稳定输出）。
//
// 设计者视角：lock Entries 抽径排序，保 vendor/graph 输出稳。障碍是 Entries 本已有序：
// 再排防手改。排查现场：输出抖先查此。
func SortedModulePaths(l *Lock) []string {
	var out []string
	for _, e := range l.Entries {
		out = append(out, e.Path)
	}
	sort.Strings(out)
	return out
}
