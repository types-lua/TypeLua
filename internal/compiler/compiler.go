package compiler

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/typelua/typelua/internal/ast"
	"github.com/typelua/typelua/internal/checker"
	"github.com/typelua/typelua/internal/config"
	"github.com/typelua/typelua/internal/diag"
	"github.com/typelua/typelua/internal/emitter"
	"github.com/typelua/typelua/internal/parser"
	"github.com/typelua/typelua/internal/sourcemap"
)

// SourceFile 是一个加载单元（路径、内容、AST、是否声明文件）。
//
// 物理布局：
//
//	SourceFile
//	├── Path    string      // 绝对/相对源路径
//	├── Content string      // 原始源文本（供 source map）
//	├── File    *ast.File   // 解析后的 AST（nil 若读取失败）
//	└── IsDecl  bool        // *.d.tl 声明文件：只检查、不发货
type SourceFile struct {
	Path    string
	Content string
	File    *ast.File
	IsDecl  bool // *.d.tl
}

// Program 是编译流水线的总控（类似 tsc Program）。
//
// 物理布局：
//
//	Program
//	├── Cfg    *config.Config      // 配置与文件发现
//	├── Files  []*SourceFile        // 按路径排序的加载单元
//	├── Diags  []diag.Diagnostic   // 聚合诊断（按文件-行-列排序输出）
//	└── shorts map[string]string    // manifest 短名 -> 全路径（内部）
//
// 数据流：Load/LoadSources -> Check -> Emit；声明文件 IsDecl=true 不发货。
type Program struct {
	Cfg   *config.Config
	Files []*SourceFile
	Diags []diag.Diagnostic
	// 每文件检查器诊断（与 Diags 聚合相同）。
	// shorts 把 manifest 短名映射到本次运行的完整模块路径。
	shorts map[string]string
}

// NewProgram 用 cfg 创建一个空 Program（Files/Diags 尚未填充）。
// 调用方随后按 Load -> Check -> Emit 驱动流水线。
func NewProgram(cfg *config.Config) *Program {
	return &Program{Cfg: cfg}
}

// Load 读取并解析所有文件，收集词法/语法诊断。
// 文件并行解析（有界 worker 池）；结果按路径排序组装，
// 因此 Files/Diags 与顺序加载完全一致。
func (p *Program) Load() error {
	list, err := p.Cfg.ListFiles()
	if err != nil {
		return err
	}
	sort.Strings(list)
	type parsed struct {
		data    []byte
		readErr error
		file    *ast.File
		pdiags  []diag.Diagnostic
	}
	results := make([]parsed, len(list))
	parallelDo(len(list), func(i int) {
		fp := list[i]
		data, err := os.ReadFile(fp)
		if err != nil {
			results[i].readErr = err
			return
		}
		rel := fp
		if r, err := filepath.Rel(p.Cfg.Dir, fp); err == nil {
			rel = r
		}
		// parser.ParseFile 是纯函数（无包级可变状态）。
		f, pdiags := parser.ParseFile(rel, string(data))
		results[i] = parsed{data: data, file: f, pdiags: pdiags}
	})
	for i, fp := range list {
		r := results[i]
		if r.readErr != nil {
			p.Diags = append(p.Diags, diag.Diagnostic{Code: diag.CannotReadFile, Message: fmt.Sprintf("cannot read file: %v", r.readErr)})
			continue
		}
		p.Diags = append(p.Diags, r.pdiags...)
		p.Files = append(p.Files, &SourceFile{Path: fp, Content: string(r.data), File: r.file, IsDecl: strings.HasSuffix(fp, ".d.tl")})
	}
	return nil
}

// LoadSources 供测试/内存使用。
func (p *Program) LoadSources(srcs map[string]string) {
	var names []string
	for n := range srcs {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		f, pdiags := parser.ParseFile(n, srcs[n])
		p.Diags = append(p.Diags, pdiags...)
		p.Files = append(p.Files, &SourceFile{Path: n, Content: srcs[n], File: f, IsDecl: strings.HasSuffix(n, ".d.tl")})
	}
}

// Check 按依赖顺序对所有文件运行类型检查（见 require.go）：
// 每个文件看到其 require 的模块类型，未解析的边退化为 `any`。
func (p *Program) Check() {
	opt := checker.Options{
		Strict:          p.Cfg.Strict(),
		StrictNilChecks: p.Cfg.StrictNilChecks(),
		NoImplicitAny:   p.Cfg.NoImplicitAny(),
		Target:          p.Cfg.Target(),
	}
	p.shorts = p.shortModules()
	p.checkAll(opt)
}

// HasErrors 报告是否存在错误级别诊断。
func (p *Program) HasErrors() bool {
	for _, d := range p.Diags {
		if d.Severity == diag.Error {
			return true
		}
	}
	return false
}

// Emit 把 .lua 输出写入 outDir，返回发射路径。跳过 .d.tl 与 `declare` 主体。
// 类型-only 声明被发射器擦除；`declare function` 空体在文件级跳过？
// 为安全起见，每个非声明文件（即使空）都发射，以保持构建形状。
// 原生资产（*.lua shim、*.so/*.dylib/*.dll/*.a）随后按布局复制，
// 使 vendored 跨语言绑定无需手动步骤即可运行。
//
// 性能：发射计算（AST 渲染 + sourcemap 编码，CPU 重）并行运行；
// 文件系统写入按文件顺序串行，保持精确的错误/部分输出语义。写入前先
// 比较内容，未变化的输出保留 mtime（避免下游重建循环）；跳过的路径
// 仍计为已发射。
func (p *Program) Emit() ([]string, error) {
	withPrelude := NeedsModPath(p)
	opt := emitter.Options{Target: p.Cfg.Target(), TreeShake: p.Cfg.CompilerOptions.TreeShake}
	jobs := collectEmitJobs(p.Cfg, p.Files)
	computeEmitJobs(jobs, opt, withPrelude)
	emittedSet := map[string]bool{}
	emitted, err := writeEmitJobs(jobs, nil, emittedSet)
	if err != nil {
		return emitted, err
	}
	assets, err := p.emitNativeAssets(emittedSet)
	if err != nil {
		return emitted, err
	}
	return append(emitted, assets...), nil
}

// emitJob 是一个文件的输出单元（映射进，渲染字节出）。
type emitJob struct {
	sf       *SourceFile
	dst      string
	srcRel   string
	out      string
	mapRaw   []byte
	mapErr   error
	wantMap  bool
	mapName  string
	mapLabel string // "--# sourceMappingURL=..." 页脚，追加在 map 写入后
}

// collectEmitJobs 把非声明文件映射为按程序顺序的输出。
func collectEmitJobs(cfg *config.Config, files []*SourceFile) []emitJob {
	var jobs []emitJob
	for _, sf := range files {
		if sf.IsDecl {
			continue
		}
		dst, ok := OutputForFile(cfg, sf.Path)
		if !ok {
			continue
		}
		rel, err := filepath.Rel(filepath.Join(cfg.Dir, cfg.RootDir()), sf.Path)
		if err != nil {
			rel = filepath.Base(sf.Path)
		}
		srcRel := filepath.ToSlash(rel)
		jb := emitJob{sf: sf, dst: dst, srcRel: srcRel}
		if cfg.CompilerOptions.SourceMap {
			// `.d.tl` 已跳过；`foo.tl` -> `foo.lua`。
			base := filepath.Base(dst)
			jb.wantMap = true
			jb.mapName = dst + ".map"
			jb.mapLabel = "--# sourceMappingURL=" + base + ".map\n"
		}
		jobs = append(jobs, jb)
	}
	return jobs
}

// computeEmitJobs 并行运行纯渲染阶段：
// EmitFileWithMap、PrependPrelude 与 sourcemap.Encode 只触及每个文件
// 新鲜的切片（无共享状态）。
func computeEmitJobs(jobs []emitJob, opt emitter.Options, withPrelude bool) {
	parallelDo(len(jobs), func(i int) {
		jb := &jobs[i]
		out, mappings := emitter.EmitFileWithMap(jb.sf.File, opt)
		if withPrelude {
			out, mappings = PrependPrelude(out, mappings)
		}
		if jb.wantMap {
			for k := range mappings {
				mappings[k].SrcFile = jb.srcRel
			}
			raw, err := sourcemap.Encode(filepath.Base(jb.dst), []string{jb.srcRel}, []string{jb.sf.Content}, nil, mappings)
			if err != nil {
				jb.mapErr = err
				return
			}
			jb.mapRaw = raw
		}
		jb.out = out
	})
}

// writeEmitJobs 执行有序写入阶段：mkdir、编码错误检查、map 写入、
// lua 写入（每个文件，与原顺序发射器相同的故障顺序）。内容比较写入
// 保持 mtime 稳定；跳过的路径仍计为已发射。用每个目标填充 emittedSet
// （原生资产碰撞守卫所需）。
func writeEmitJobs(jobs []emitJob, emitted []string, emittedSet map[string]bool) ([]string, error) {
	for i := range jobs {
		jb := &jobs[i]
		// `.d.tl` 已跳过；`foo.tl` -> `foo.lua`。
		if err := os.MkdirAll(filepath.Dir(jb.dst), 0o755); err != nil {
			return emitted, err
		}
		// 与之前相同的故障顺序：mkdir、encode、map 写入、lua 写入。
		if jb.wantMap && jb.mapErr != nil {
			return emitted, jb.mapErr
		}
		if jb.wantMap {
			if _, err := writeFileIfChanged(jb.mapName, jb.mapRaw); err != nil {
				return emitted, err
			}
			jb.out += jb.mapLabel
		}
		if _, err := writeFileIfChanged(jb.dst, []byte(jb.out)); err != nil {
			return emitted, err
		}
		emitted = append(emitted, jb.dst)
		emittedSet[filepath.Clean(jb.dst)] = true
	}
	return emitted, nil
}

// writeFileIfChanged 把 data 写入 path；当当前内容字节相同则跳过写入
// （与 mtime 提升）。报告是否写入。跳过的路径仍计为已发射，
// 使 watch 过时追踪保持精确。
func writeFileIfChanged(path string, data []byte) (bool, error) {
	if cur, err := os.ReadFile(path); err == nil && string(cur) == string(data) {
		return false, nil
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return false, err
	}
	return true, nil
}

// EmitToString 供测试使用。
func (p *Program) EmitToString() map[string]string {
	opt := emitter.Options{Target: p.Cfg.Target(), TreeShake: p.Cfg.CompilerOptions.TreeShake}
	out := map[string]string{}
	for _, sf := range p.Files {
		if sf.IsDecl {
			continue
		}
		out[sf.Path] = emitter.EmitFile(sf.File, opt)
	}
	return out
}

// FormatDiags 渲染诊断为 file:line:col + message，已排序。
func (p *Program) FormatDiags() string {
	ds := append([]diag.Diagnostic{}, p.Diags...)
	sort.Slice(ds, func(i, j int) bool {
		if ds[i].Span.File != ds[j].Span.File {
			return ds[i].Span.File < ds[j].Span.File
		}
		if ds[i].Span.Start.Line != ds[j].Span.Start.Line {
			return ds[i].Span.Start.Line < ds[j].Span.Start.Line
		}
		return ds[i].Span.Start.Column < ds[j].Span.Start.Column
	})
	var sb strings.Builder
	for _, d := range ds {
		sb.WriteString(d.String())
		sb.WriteString("\n")
	}
	return sb.String()
}
