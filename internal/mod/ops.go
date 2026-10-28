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

// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：Project 是包操作的“工作台”——Dir 定根，Manifest 必有，Lock 可无，
// HasLock 记 lock 是否曾在。障碍是无 manifest 目录：LoadProject 报错指 init。
// 不变量：Manifest 永非 nil。排查现场：根错先查 FindManifest 上溯。
type Project struct {
	Dir      string
	Manifest *Manifest
	Lock     *Lock
	// 排查看 Span/诊断，如 `local x=1` 错圈整句。
	HasLock bool
}

// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：向上溯至根，首命中即项目根（类 go）。障碍是 Abs 失败：退原径。
// 候选：当前目录 vs 上溯；选上溯，子目录可跑。不变量：空即无项目。排查现场：
// 根错先查上溯。
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

// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：manifest 必有（缺即指 init），lock 有则解、无则空（HasLock 记之）。
// 障碍是坏 toml：包路径指错因。候选：缺 lock 报错 vs 空；选空，新项目可跑。
// 不变量：返时 Manifest 非 nil。排查现场：无项目先查上溯，坏文件先查包径。
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

// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：EncodeManifest 定序写，覆写原子由 OS 保。障碍是并发写：调用方串行。
// 排查现场：diff 抖先查 Encode。
func (p *Project) SaveManifest() error {
	return os.WriteFile(filepath.Join(p.Dir, ManifestName), []byte(EncodeManifest(p.Manifest)), 0o644)
}

// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：EncodeLock 定序写，fetch/get 后落盘。障碍是空 lock：仍写头注。
// 排查现场：lock 抖先查 Encode。
func (p *Project) SaveLock() error {
	return os.WriteFile(filepath.Join(p.Dir, LockName), []byte(EncodeLock(p.Lock)), 0o644)
}

// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：相对拼 Dir，绝对直返。障碍是旧 lock 无 Replace 表：localSourceDir
// 另兜。排查现场：本地径错先查拼基。
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

// 排查看 Span/诊断，如 `local x=1` 错圈整句。
// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：fetch 保守——lock 中仍满足 query 者钉 exact，余走升级。障碍是本地条目：
// 跳过（无版可钉）。候选：全升 vs 钉住；选钉住复现。不变量：本地不钉。排查现场：
// fetch 升级先查钉。
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
	// 设计者视角：两版 Compare==0 即等（预发全比）。障碍是 `v` 前缀：ParseVersion 容之。
	// 排查现场：钉住失效先查此。
	o, err := ParseVersion(tag)
	if err != nil {
		return false
	}
	return o.Compare(v) == 0
}

// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：GitRegistry 外包 ReplaceRegistry，根 replace+传递链统一。障碍是空
// replace：仍包（见 ReplaceRegistry）。排查现场：本地不生效先查包。
func (p *Project) registryFor(cache *Cache) Registry {
	return ReplaceRegistry(&GitRegistry{Cache: cache}, p.Manifest.Replace, p.Dir)
}

// 每个 spec 为 `path` 或 `path@query`（默认：保留 manifest query，否则
// latest）。无 spec 时，所有直接依赖重新解析（升级）。
//
// 设计者视角：get 是“加购+结算”——spec 入 manifest（空 query 保旧 else latest），
// 升级集=spec 径+无 spec 时全直接，全解+下载+写盘。障碍是 path 查询含 `/`：
// splitSpec 按首 `@` 切，径禁 `@` 保可分。候选：部分升 vs 全升；选显式集。不变量：
// manifest 先验。排查现场：未升级先查升级集，径错先查 split。
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
	// 排查看 Span/诊断，如 `local x=1` 错圈整句。
	// 排查看 Span/诊断，如 `local x=1` 错圈整句。
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
		// 排查看 Span/诊断，如 `local x=1` 错圈整句。
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

// 排查看 Span/诊断，如 `local x=1` 错圈整句。
// 排查看 Span/诊断，如 `local x=1` 错圈整句。
// 排查看 Span/诊断，如 `local x=1` 错圈整句。
// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：钉住版详见实现，排查看 Span。
// 障碍是 URL 覆：pinSource 记源供下载。排查现场：复现变升级先查 pins。
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

// 排查看 Span/诊断，如 `local x=1` 错圈整句。
// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：本地条目录 Dir 相对化（相对 Dir 斜杠），跨机可携。障碍是绝对残留：
// 相对化之。排查现场：跨检出 vendor 错先查 Dir。
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
//
// 设计者视角：首 `@` 切径询，无 `@` 即全径。障碍是径含 `@`：ValidPath 已禁。
// 排查现场：切错先查径合法。
func splitSpec(s string) (string, string) {
	// A query never contains `/`; the path never contains `@`.
	if i := strings.Index(s, "@"); i >= 0 {
		return s[:i], s[i+1:]
	}
	return s, ""
}

// 排查看 Span/诊断，如 `local x=1` 错圈整句。
// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：fetch 是“复现键”——pinsForFetch 钉住可复现者，resolveLocked 重解，
// verifyLockCommits 防上游 tag 漂（漂即错，须显式 get 升），下载+写盘。障碍是 tag
// 移动：fetch 不升，报错。候选：静默跟 vs 报错；选报错保复现。不变量：漂移即错。
// 排查现场：fetch 失败先查 tag 漂。
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

// 排查看 Span/诊断，如 `local x=1` 错圈整句。
// (explicit `get` upgrades; fetch reproduces).
//
// 设计者视角：新旧 lock 同径 tag 版而 commit 异即上游改写，fetch 拒。障碍是 commit
// 钉（非 tag）：版同 commit 同即过。排查现场：fetch 漂移错先查 tag 改写。
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
	// 设计者视角：日志短 SHA 取 12 位（git 短惯例加长），短者原样。障碍是 `local`：
	// 原样。排查现场：日志对不上先查截位。
	if len(s) > 12 {
		return s[:12]
	}
	return s
}

// 排查看 Span/诊断，如 `local x=1` 错圈整句。
// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：本地源两阶——lock Dir（新）优先，根 replace（旧）兜，非 local 拒。
// 障碍是旧 lock 无 Dir：replace 兜。排查现场：本地缺先查两阶。
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

// 排查看 Span/诊断，如 `local x=1` 错圈整句。
// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：逐条本地验存、已存验指纹跳过、缺者定源 EnsureTree。障碍是离线现存树：
// 免 URL（见 Present 跳）。候选：全下 vs 按需；选按需。不变量：本地不进缓存。
// 排查现场：离线失败先查现存跳，源错先查 sources/sourceURL。
func (p *Project) downloadAll(ctx context.Context, cache *Cache, l *Lock, sources map[string]string) error {
	for _, e := range l.Entries {
		if dir, ok := p.localSourceDir(e); ok {
			if _, err := os.Stat(dir); err != nil {
				return fmt.Errorf("replace target for %q unavailable: %v", e.Path, err)
			}
			continue // vendored/copied on demand; nothing cached
		}
		// 排查看 Span/诊断，如 `local x=1` 错圈整句。
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

// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：显式 URL 优先，余重探（传递依赖无 manifest 条，lock 不存 URL，故
// 确定性重探）。障碍是传递源：须重探。排查现场：源漂移先查重探。
func (p *Project) sourceURL(ctx context.Context, cache *Cache, path string) (string, error) {
	if d := p.Manifest.Deps[path]; d != nil && d.URL != "" {
		return d.URL, nil
	}
	// 排查看 Span/诊断，如 `local x=1` 错圈整句。
	// 排查看 Span/诊断，如 `local x=1` 错圈整句。
	reg := &GitRegistry{Cache: cache}
	return reg.urlFor(ctx, path, Dep{})
}

// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：`Dir/tlua_modules`，构建与 LSP 同源。障碍是相对：拼 Dir 绝对化。
// 排查现场：vendor 错位先查 Dir。
func (p *Project) VendorDir() string {
	return filepath.Join(p.Dir, VendorDir)
}

// 排查看 Span/诊断，如 `local x=1` 错圈整句。
// 排查看 Span/诊断，如 `local x=1` 错圈整句。
// 排查看 Span/诊断，如 `local x=1` 错圈整句。
// 排查看 Span/诊断，如 `local x=1` 错圈整句。
// 排查看 Span/诊断，如 `local x=1` 错圈整句。
// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：vendor 是“随行粮”——先保树现存详见实现，排查看 Span。
// （保删文件消失），本地拷、远程树拷，末写 modules.txt。障碍是旧 vendor 残留：
// 全清重铺；障碍是点文件/.git：copyTlTree 滤之。候选：增量 vs 全量；选全量简单准。
// 不变量：vendor 恒 lock 全量。排查现场：残留文件先查清铺，陈旧先查下载。
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

// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：拷 lock 按径排，保 vendor/graph 输出稳。障碍是原序手改：重排。
// 排查现场：输出抖先查此。
func SortedEntries(l *Lock) []LockEntry {
	out := append([]LockEntry(nil), l.Entries...)
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

// 排查看 Span/诊断，如 `local x=1` 错圈整句。
// 排查看 Span/诊断，如 `local x=1` 错圈整句。
// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：仅四类运行时资产可 vendor（点文件/.git/头文档全留缓存），Walk 保
// 布局，文件 644、目录 755。障碍是点文件：跳；头文件：跳。候选：全拷 vs 白名单；
// 选白名单小 vendor。不变量：目录结构同源。排查现场：shim 缺先查后缀表。
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
		isNative := strings.HasSuffix(lower, ".so") || strings.HasSuffix(lower, ".dylib") || strings.HasSuffix(lower, ".dll") || strings.HasSuffix(lower, ".a")
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
	// 设计者视角：流拷 644，关错合并（写错优先）。障碍是半写：调用方全量 vendor
	// 重铺盖之。排查现场：拷坏先查关错。
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

// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：Nodes 为 lock 序，Edges 径→依赖径（排好）。障碍是图须离线可画：
// 只读缓存树/本地，不触网。排查现场：边缺先查 manifest 读。
type Graph struct {
	Nodes []LockEntry
	// Edges maps module path -> required module paths (queries resolved).
	Edges map[string][]string
}

// 排查看 Span/诊断，如 `local x=1` 错圈整句。
// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：逐 lock 条读 manifest（本地直读、远程读树），缺树即指 fetch，坏
// manifest 跳（叶视）。障碍是未下载：显错指 fetch。候选：联网补 vs 离线；选离线。
// 不变量：零网络。排查现场：图空先查树 present。
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
	// 设计者视角：坏 manifest 返 nil 视叶（graph 容错），严格路另有 readLocalStrict。
	// 障碍是缺文：nil。排查现场：边缺先查此吞错。
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
