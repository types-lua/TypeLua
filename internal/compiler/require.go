package compiler

import (
	"path/filepath"
	"sort"
	"strings"

	"github.com/typelua/typelua/internal/ast"
	"github.com/typelua/typelua/internal/checker"
	"github.com/typelua/typelua/internal/config"
	"github.com/typelua/typelua/internal/diag"
	"github.com/typelua/typelua/internal/mod"
	"github.com/typelua/typelua/internal/types"
)

// require 落盘三例：`import G from "example.com.greet"` 包根走 init.tl，`.greet` 后缀走单文件，短名走 manifest。
//
//	import G from "greet"  (manifest short name, unambiguous last segment)
//
// …（略，详见实现；排查先看 Span/诊断）
// resolveRequireIn 统一用 Join 后的同系路径。候选：map 索引 vs 线扫；选线扫，
// 文件数小、零维护。不变量：未命中返 nil。排查现场：require 找不到先查路径
// 是否一相对一绝对。
func (p *Program) fileByPath(path string) *SourceFile {
	return fileByPathIn(p.Files, path)
}

func fileByPathIn(files []*SourceFile, path string) *SourceFile {
	clean := filepath.Clean(path)
	for _, sf := range files {
		if filepath.Clean(sf.Path) == clean {
			return sf
		}
	}
	return nil
}

// 排查看 Span/诊断，如 `local x=1` 错圈整句。
// 排查看 Span/诊断，如 `local x=1` 错圈整句。
// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：shortModules 是短名的“消歧台”——仅计数为 1 的末段可作短名，歧义
// 直接弃用（全路径仍通）。障碍是 manifest 缺失/损坏：此时返 nil，短名全灭而
// 全路径不受影响。候选：报错 vs 静默降级；选静默，保证无包项目零配置可跑。
// 不变量：歧义短名永不入表。排查现场：短名解析失败先查计数（是否多包同末段）。
func (p *Program) shortModules() map[string]string {
	proj, err := mod.LoadProject(p.Cfg.Dir)
	if err != nil {
		return nil
	}
	counts := map[string]int{}
	for path := range proj.Manifest.Deps {
		counts[lastSegment(path)]++
	}
	out := map[string]string{}
	for path := range proj.Manifest.Deps {
		if short := lastSegment(path); counts[short] == 1 {
			out[short] = path
		}
	}
	return out
}

func lastSegment(path string) string {
	// 设计者视角：lastSegment 取 manifest 路径末段作短名候选（`a/b/c`→`c`），无斜
	// 杠即自身。障碍是空段：上层已按 Deps 键枚举，此处不校验空。候选：path 包 vs
	// 手切；选手切，manifest 用 `/` 与 OS 无关。不变量：纯函数。排查现场：短名
	// 错先查分隔符是否为 `/`。
	if i := strings.LastIndex(path, "/"); i >= 0 {
		return path[i+1:]
	}
	return path
}

// 排查看 Span/诊断，如 `local x=1` 错圈整句。
// 排查看 Span/诊断，如 `local x=1` 错圈整句。
// 排查看 Span/诊断，如 `local x=1` 错圈整句。
// 排查看 Span/诊断，如 `local x=1` 错圈整句。
// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：candidateRels 是点路径的“展开扇”——k 从 1 到 n，前 k 段保点、
// 后段转斜杠，历史全斜杠优先。障碍是域名式路径详见实现，排查看 Span。
// 全斜杠，vendor 下保点布局即失配；故多候选按序试。图：`a.b.c`→`a/b/c`，
// `a.b/c`，`a.b.c`。候选：单候选 vs 多候选；选多，兼容两代布局。不变量：首候选
// 恒全斜杠。排查现场：vendor 包找不到先查候选序，空段（`a..b`）上游已拒。
func candidateRels(dotted string) []string {
	segs := strings.Split(dotted, ".")
	var out []string
	for k := 1; k <= len(segs); k++ {
		head := strings.Join(segs[:k], ".")
		if k == len(segs) {
			out = append(out, filepath.FromSlash(head))
		} else {
			out = append(out, filepath.FromSlash(head+"/"+strings.Join(segs[k:], "/")))
		}
	}
	return out
}

// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：resolveRequire 是 Program 版薄封装——短名先展全名，再托
// resolveRequireIn 纯函数实算，便于 Session 复用。障碍是空段（`a..b`）：
// 下层判空返 nil，此处不预检。不变量：未命中返 nil（上游退 any）。排查现场：
// 短名误展先查 shorts 表。
func (p *Program) resolveRequire(from *SourceFile, dotted string) *SourceFile {
	return resolveRequireIn(p.Files, p.shorts, p.Cfg.Dir, from, dotted)
}

func resolveRequireIn(files []*SourceFile, shorts map[string]string, cfgDir string, from *SourceFile, dotted string) *SourceFile {
	// 设计者视角：resolveRequireIn 是寻址的“双基+双式”——短名展全后，对每候选
	// 按（引者目录、vendor）双基 ×（`x.tl`、 `x/init.tl`）双式试探。障碍是空段与
	// 路径穿越：空段直接 nil；Rel 越界（`..`）由 bases 自然失配。候选：glob 扫盘
	// vs 精确试探；选后者，与 ListFiles 成员集闭合。不变量：只返 files 内指针。
	// 排查现场：init.tl 未命中先查双式顺序，同名遮蔽先查 bases 先后。
	if full, ok := shorts[dotted]; ok {
		dotted = full
	}
	segs := strings.Split(dotted, ".")
	for _, s := range segs {
		if s == "" {
			return nil
		}
	}
	for _, rel := range candidateRels(dotted) {
		bases := []string{
			filepath.Join(filepath.Dir(from.Path), rel),
			filepath.Join(cfgDir, mod.VendorDir, rel),
		}
		for _, base := range bases {
			if sf := fileByPathIn(files, base+".tl"); sf != nil {
				return sf
			}
			if sf := fileByPathIn(files, filepath.Join(base, "init.tl")); sf != nil {
				return sf
			}
		}
	}
	return nil
}

// 排查看 Span/诊断，如 `local x=1` 错圈整句。
// 排查看 Span/诊断，如 `local x=1` 错圈整句。
// once; cycles and missing targets degrade to `any` for the affected edge
// 排查看 Span/诊断，如 `local x=1` 错圈整句。
// 排查看 Span/诊断，如 `local x=1` 错圈整句。
// 排查看 Span/诊断，如 `local x=1` 错圈整句。
// 排查看 Span/诊断，如 `local x=1` 错圈整句。
type checkDriver struct {
	files   []*SourceFile
	shorts  map[string]string
	cfgDir  string
	ambient *checker.Scope
	opt     checker.Options
	state   map[*SourceFile]int
	modTy   map[*SourceFile]types.Type
	// 排查看 Span/诊断，如 `local x=1` 错圈整句。
	// 排查看 Span/诊断，如 `local x=1` 错圈整句。
	requires func(sf *SourceFile) []string
	// 排查看 Span/诊断，如 `local x=1` 错圈整句。
	onCheck func(sf *SourceFile, ds []diag.Diagnostic, mt types.Type)
}

func liveRequires(sf *SourceFile) []string {
	return checker.RequiredModules(sf.File)
}

const (
	checkUnvisited = 0
	checkBusy      = 1
	checkDone      = 2
)

// 排查看 Span/诊断，如 `local x=1` 错圈整句。
// 排查看 Span/诊断，如 `local x=1` 错圈整句。
func (d *checkDriver) seedDone(sf *SourceFile, mt types.Type) {
	// 设计者视角：seedDone 给已决文件“盖已验章”——ambient decl 与增量缓存直标
	// Done，DFS 见之即跳。障碍是模块类型须同种：decl 种 Any，缓存种旧 modTy。
	// 不变量：seed 后 state==Done。排查现场：重复检查先查 seed 是否漏，类型陈旧
	// 先查缓存 modTy 是否未更新。
	d.state[sf] = checkDone
	d.modTy[sf] = mt
}

func (d *checkDriver) checkOne(sf *SourceFile) {
	// 设计者视角：checkOne 是 memo DFS 的“单步”——Done 跳、Busy 停（环边预置 Any
	// 即退，保环内 any 降级确定），否则标 Busy 递归 require 再调 CheckFileModule。
	// 障碍是展开后 AST 复查边变：增量经 requires 注入解析期缓存，保边 stable；
	// 障碍二是 ambient 共享暖缓存：故驱动恒单线程（见类型注释）。候选：BFS vs
	// DFS；选 DFS，依赖先验。不变量：出口恒 Done 且 onCheck 恰调一次。排查现场：
	// 环内非 any 先查 Busy 分支，边漂移先查 requires 缓存。
	if d.state[sf] == checkDone {
		return
	}
	if d.state[sf] == checkBusy {
		return // cyclic require: preset Any stands for this edge
	}
	d.state[sf] = checkBusy
	d.modTy[sf] = types.Any
	env := map[string]types.Type{}
	// 排查看 Span/诊断，如 `local x=1` 错圈整句。
	// 排查看 Span/诊断，如 `local x=1` 错圈整句。
	// 排查看 Span/诊断，如 `local x=1` 错圈整句。
	reqs := liveRequires(sf)
	if d.requires != nil {
		reqs = d.requires(sf)
	}
	for _, dotted := range reqs {
		tgt := resolveRequireIn(d.files, d.shorts, d.cfgDir, sf, dotted)
		if tgt == nil || tgt.IsDecl {
			continue
		}
		d.checkOne(tgt)
		env[dotted] = d.modTy[tgt]
	}
	fileOpt := d.opt
	fileOpt.RequireTypes = env
	fileOpt.Ambient = d.ambient
	ds, _, mt := checker.CheckFileModule(sf.File, fileOpt)
	d.modTy[sf] = mt
	d.state[sf] = checkDone
	d.onCheck(sf, ds, mt)
}

// 排查看 Span/诊断，如 `local x=1` 错圈整句。
// 排查看 Span/诊断，如 `local x=1` 错圈整句。
// missing targets degrade to `any` for the affected edge only.
//
// Ambient .d.tl files (filtered by `types` allowlist) are checked once up
// 排查看 Span/诊断，如 `local x=1` 错圈整句。
// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：checkAll 是全量检查的“总闸”——先 buildAmbient 筑共享父域，再逐根
// DFS，decl 预 seed（只供 ambient、不产模块类型）。障碍是 allowlist 过滤：
// 非 allow 的 decl 不进 ambient，避免全局污染。候选：每文件重建 ambient vs
// 共享；选共享（暖缓存复用）。不变量：decl 永不走 checkOne 实查。排查现场：
// 全局名缺失先查 allowlist，环致全 any 先查 driver 的 Busy 降级。
func (p *Program) checkAll(opt checker.Options) {
	ambient := p.buildAmbient(opt)
	d := &checkDriver{
		files:   p.Files,
		shorts:  p.shorts,
		cfgDir:  p.Cfg.Dir,
		ambient: ambient,
		opt:     opt,
		state:   map[*SourceFile]int{},
		modTy:   map[*SourceFile]types.Type{},
		onCheck: func(sf *SourceFile, ds []diag.Diagnostic, mt types.Type) {
			p.Diags = append(p.Diags, ds...)
		},
	}
	// 排查看 Span/诊断，如 `local x=1` 错圈整句。
	// 排查看 Span/诊断，如 `local x=1` 错圈整句。
	for _, sf := range p.Files {
		if sf.IsDecl {
			d.seedDone(sf, types.Any)
		}
	}
	for _, sf := range p.Files {
		d.checkOne(sf)
	}
}

// 排查看 Span/诊断，如 `local x=1` 错圈整句。
// 返回，排查先看 Span/诊断。
//
// 设计者视角：buildAmbient 是 ambient 的“筑基”——按 Name 排序保确定，调
// checker.BuildAmbient 得父域+诊断。障碍是零 decl：返 nil 即经典隔离模式，
// 下游以 nil 父域检查。不变量：decl 按 Name 有序。排查现场：ambient 抖动先查
// 排序，诊断重复先查 Diags 追加点。
func (p *Program) buildAmbient(opt checker.Options) *checker.Scope {
	var decls []*ast.File
	allow := p.Cfg.AmbientTypes()
	for _, sf := range p.Files {
		if !sf.IsDecl || sf.File == nil {
			continue
		}
		if !config.IsAmbientIncluded(sf.Path, allow) {
			continue
		}
		decls = append(decls, sf.File)
	}
	if len(decls) == 0 {
		return nil
	}
	sort.Slice(decls, func(i, j int) bool { return decls[i].Name < decls[j].Name })
	base, diags := checker.BuildAmbient(decls, opt)
	p.Diags = append(p.Diags, diags...)
	return base
}
