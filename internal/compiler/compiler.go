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
// 设计者视角：SourceFile 是流水线的“集装箱”——Path 定身份，Content 留作 source
// map 源文，File 装 AST，IsDecl 标 *.d.tl（只查不发）。障碍是读取失败：File 可
// 为 nil，后续 Check/Emit 须 nil 宽容而非崩。候选：存 AST vs 存文本；选双存，
// 发射与映射各取所需。不变量：IsDecl 由后缀定，贯穿 Check/Emit。排查现场：
// .d.tl 被发射先查 IsDecl 判定，映射源文缺失先查 Content 是否被置空。
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
// 设计者视角：Program 是“厂长办公室”——Cfg 管配置发现，Files 按径排序保确定性，
// Diags 聚合全阶段，shorts 藏 manifest 短名映射。障碍是并行解析乱序：结果按索
// 引回填（见 Load），确定性优先于速度。候选：多 Program 复用 vs 一次一建；选
// 一次一建，会话复用另有 Session（见 incremental.go）。不变量：Files 恒有序。
// 排查现场：输出抖动先查排序是否被绕过，短名解析错先查 shorts 构建。
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
//
// 设计者视角：NewProgram 只做“挂牌”——装 Cfg，不碰磁盘，让 Load/Check/Emit 三段
// 显式可测。障碍是空 Program 误调 Check：Files 为空即零文件检查，不崩。取舍：
// 构造极薄，IO 全留 Load。不变量：返回后 shorts 为 nil，Check 时重建。排查现场：
// 空输出先查是否忘调 Load。
func NewProgram(cfg *config.Config) *Program {
	return &Program{Cfg: cfg}
}

// Load 读取并解析所有文件，收集词法/语法诊断。
// 文件并行解析（有界 worker 池）；结果按路径排序组装，
// 因此 Files/Diags 与顺序加载完全一致。
//
// 设计者视角：Load 是“进货+分拣”——ListFiles 定成员，parallelDo 并行读+
// parser.ParseFile 纯解析，末按 list 序组装。障碍是 parser 非线程安全误解：
// ParseFile 纯函数无包级可变，故可并行；IsDecl 按后缀判，.d.tl 亦入 Files 供
// ambient。候选：串行 vs 并行；选并行但按索引回填保序。不变量：Files 与 list
// 同序；读失败记 CannotReadFile 且不建 File。排查现场：文件漏载先查 ListFiles，
// 乱序先查回填索引。
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
//
// 设计者视角：LoadSources 是“内存进货口”——无磁盘 IO，直接 ParseFile 入 Files，
// 供测试与 LSP 复用。障碍是路径排序：map 迭代随机，须显式 sort 保确定性。
// 不变量：语义同 Load（IsDecl 同判、诊断同收）。排查现场：测试抖动先查排序。
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
//
// 设计者视角：Check 是“监理进场”——把 Cfg 开关译为 checker.Options，经
// shortModules 建短名，再托 checkAll 做 memo DFS。障碍是开关透传遗漏：
// Target 忘传致版本 API 误报，故此处四开关齐抄。候选：每文件独立 Options vs
// 共享；选共享只读。不变量：Check 可重调（shorts 重建）。排查现场：开关不生效
// 先查 Cfg 访问器，require 类型丢失先查 require.go 驱动。
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
//
// 设计者视角：HasErrors 是“放行闸”——扫 Diags 见 Error 即 true，警告不拦构建。
// 障碍是严重级别混淆：Warn 不应阻断 Emit，故只认 Error。不变量：纯读。排查现场：
// 构建误放行先查诊断级别标错。
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
// 设计者视角：Emit 是“发货+铺路”——先 NeedsModPath 判是否铺 prelude，再
// collect→compute（并行纯算）→write（串行落盘保错序），末补原生资产。障碍一是
// 并行写乱序掩盖首错：故算写分离，写恒按序；障碍二是 mtime 抖动致下游重建：
// writeFileIfChanged 内容比对保稳；障碍三是空文件：仍发货保构建形状对称。
// 图：jobs(纯) → 并算 → 串写 → 资产。候选：写亦并行 vs 串行；选串行，错序确定。
// 不变量：跳过仍计 emitted。排查现场：首错不对先查 jobs 序，并发产物缺 prelude
// 先查 NeedsModPath。
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
//
// 设计者视角：emitJob 是“发货单”——sf/dst 定去向，out/mapRaw 装算后字节，
// wantMap/mapLabel 控映射开关与页脚。障碍是算写分离需中转：jobs 切片传址，
// compute 并写各槽、write 串读。不变量：IsDecl 永不进 jobs（见 collect）。
// 排查现场：映射缺失先查 wantMap，页脚错位先查 mapLabel 追加点。
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
//
// 设计者视角：collect 是“列清单”——跳 IsDecl、经 OutputForFile 定 dst、按需挂
// map 名与页脚，保序。障碍是相对路径：须相对 RootDir 算 srcRel 供映射源名，
// 失败退 Base。候选：调用方散算 vs 集中；选集中，watch 清理复用同函数。不变量：
// 输出序即 Files 序。排查现场：.d.tl 被发货先查此跳过，映射源名错先查 srcRel。
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
//
// 设计者视角：compute 是“并行车间”——每 job 独立渲染+铺 prelude+编码，
// 无共享故可 parallelDo。障碍是 SrcFile 回填：渲染时 File 名为解析相对名，
// 须覆为 srcRel（相对 RootDir）方与 dist 布局对齐；映射须拷贝改行（见
// PrependPrelude）防 alias。不变量：失败只记 mapErr 不抛，交 write 按序报。
// 排查现场：映射源错先查回填，并行崩先查共享写。
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
//
// 设计者视角：write 是“串行出库”——mkdir→判 mapErr→写 map→缀页脚→写 lua，
// 故障序与旧顺序发射器一致，调用方可比对。障碍是部分写：首错即返，已写不清，
// 调用方凭 emitted 续跑。候选：并写 vs 串写；选串写，错序即契约。不变量：
// emittedSet 恒填 Clean(dst)。排查现场：map 与 lua 不配对先查页脚追加点，
// 碰撞未守先查 emittedSet。
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
//
// 设计者视角：writeFileIfChanged 是 mtime 的“稳压器”——字节等即跳写，省下游
// 重建。障碍是读赛：文件不存在即写；读到即比，全等方跳。候选：恒写 vs 比对；
// 选比对，watch 循环靠 mtime 活。不变量：返 (false,nil) 即跳过。排查现场：
// 下游频重建先查是否绕过此函数直写。
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
//
// 设计者视角：EmitToString 是测试的“直达梯”——跳过 prelude/映射/落盘，直调
// EmitFile 判文本。障碍是与真实 Emit 分叉：prelude 差异须另测（见 bootstrap
// 测试）。不变量：IsDecl 跳过同 Emit。排查现场：测试与产物不一先查 prelude
// 是否被忽略。
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
//
// 设计者视角：FormatDiags 是“稳定视图”——拷贝后按文件-行-列排，屏蔽并行与增量
// 的原始乱序。障碍是原切片须不动：append 空切片拷贝防副作用。候选：调用方排 vs
// 此处排；选此处，CLI/watch/LSP 同视图。不变量：纯读。排查现场：输出乱序先查
// 是否绕过此函数直读 Diags。
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
