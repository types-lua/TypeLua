package emitter

import (
	"strings"

	"github.com/typelua/typelua/internal/ast"
	"github.com/typelua/typelua/internal/shake"
	"github.com/typelua/typelua/internal/sourcemap"
)

// 发射器核心状态、文件驱动与带 source map 的块输出。

// Options 是发射选项。
//
// 设计者视角：Options 是发射器的“方言旋钮盘”，好比印刷机的纸张档——Target
// 决定吐 5.4 还是 5.1 方言（见 EmitFileWithMap 的 <const>/<close> 剥离），
// TreeShake 决定是否先请 shake 把零引用纯局部函数撕掉。障碍是并发检测与
// 死代码的竞速：若先判 NeedsConcurrency 再 shake，死代码里的 await 会误触
// 调度器前导；故顺序铁律为 shake→检测→发射。候选：把方言判断散落各处 vs
// 集中在 Options；选集中，stmt.go 只查 opt.Target。不变量：Options 只读，
// 发射中途不改。排查现场：5.1 产物出现 <const> 先查 Target 是否透传，调度器
// 前导无故出现先查 TreeShake 是否误关。
//
// 物理布局：
//
//	Options
//	├── Target     string // "5.4" (默认) 或 "5.1"
//	└── TreeShake  bool   // 是否删除零引用纯局部函数
type Options struct {
	// Target 为 "5.4"（默认）或 "5.1"；5.1 下剥离 `<const>`/`<close>`。
	Target string
	// TreeShake 删除零引用的纯局部函数（`local function f`、单个
	// `local x = function`）。默认 false：shaking 在并发检测之前运行，
	// 因此仅在被删代码里含 await 的文件不会触发调度器前导。
	TreeShake bool
}

// DefaultOptions 返回默认发射选项：目标 Lua 5.4，不做 tree-shaking。
//
// 设计者视角：DefaultOptions 是“出厂刻度”——5.4 能力全开、零删除，保证初次
// 体验与手写 Lua 一致。障碍是激进默认（默认 shake）会吓到调试者：删掉的函数
// 在 source map 里无影无踪；故默认 false，优化显式 opt-in。不变量：每次调用
// 返回新值，可随意改 Target 而不污染全局。排查现场：局部函数莫名消失先查
// TreeShake 是否被置 true。
func DefaultOptions() Options { return Options{Target: "5.4"} }

// EmitFile 把已解析文件渲染为 Lua 源码（类型擦除）。
//
// 设计者视角：EmitFile 是“快捷窗口”——不要映射时走此门，内部委托
// EmitFileWithMap 并丢掉映射，开销近似为零（映射本就顺手收集）。障碍是有人
// 以为丢映射能省一半时间：实则 write() 里 pending 冲刷不可跳，否则得维护两套
// 路径。取舍：恒收集、按需编码，一套路径保正确。不变量：返回值与 WithMap 首
// 元完全一致。排查现场：要调 source map 却调此处，次查应换 EmitFileWithMap。
func EmitFile(f *ast.File, opt Options) string {
	out, _ := EmitFileWithMap(f, opt)
	return out
}

// EmitFileWithMap 渲染 Lua 源码及 source-map 条目（语句粒度、rune 列）。
// 条目始终收集；仅当调用方需要映射时才编码（开销近似为零）。
// 使用协作并发（spawn/await/select/通道）的文件获得调度器前导与
// `__tl_run(function() ... end)` 代码块包装（可重入，故被 require 的
// 模块继承调用方的任务）。
//
// 设计者视角：EmitFileWithMap 是发射器的“总装线”，三段工序不可调序：
// shake（可选去死代码）→ NeedsConcurrency 探针 → 分叉发射。障碍一是并发包装
// 与 return 的打架：chunk 顶层 `return M` 必须变成 `return __tl_run(...)`
// 而非丢返回值，故 hasRet 分两路拼串；障碍二是空指针：f/Block 可为 nil，
// pushDeferFrame/emitBlock 均须 nil 宽容。候选：并发包装另起文件 vs 行内
// if；选行内，保持单遍。图：
//
//	AST → [shake?] → 并发? → 前导+__tl_run 包裹 / 裸块 → buf+mappings
//
// 不变量：输出恒以换行结尾（无则补）；mappings 的 Gen 行列与 buf 同步推进
// （见 write）。排查现场：并发文件缺前导先查 NeedsConcurrency 是否漏报，
// return 丢失先查 hasRet 分支，行号漂移先查 line/col 是否被绕过 write 篡改。
func EmitFileWithMap(f *ast.File, opt Options) (string, []sourcemap.Mapping) {
	if opt.TreeShake && f != nil {
		shake.ShakeFile(f)
	}
	e := &emitter{opt: opt, line: 1}
	if f != nil && NeedsConcurrency(f) {
		e.write(concurrentPrelude)
		hasRet := f.Block != nil && f.Block.Ret != nil
		if hasRet {
			e.write("return __tl_run(function()\n")
		} else {
			e.write("__tl_run(function()\n")
		}
		e.pushDeferFrame(f.Block)
		e.emitDeferPrelude(1)
		e.emitBlock(f.Block, 1)
		e.emitDeferTrail(f.Block, 1)
		e.popDeferFrame()
		e.write("end)")
		out := e.buf.String()
		if out == "" || strings.HasSuffix(out, "\n") {
			return out, e.mappings
		}
		return out + "\n", e.mappings
	}
	e.pushDeferFrame(f.Block)
	e.emitDeferPrelude(0)
	e.emitBlock(f.Block, 0)
	e.emitDeferTrail(f.Block, 0)
	e.popDeferFrame()
	out := e.buf.String()
	if out == "" || strings.HasSuffix(out, "\n") {
		return out, e.mappings
	}
	return out + "\n", e.mappings
}

type pendingMark struct {
	// 设计者视角：pendingMark 是“一号候诊位”——单槽预约下一可见字符的映射。
	// 障碍是连续 mark：后者覆盖前者恰好实现“语句取首”语义，无需队列。
	span ast.Node
	name string
}

type emitter struct {
	// 设计者视角：emitter 是“打印机芯 + 账本”——buf 攒文本，line/col 记生成
	// 坐标，pending/mappings 记映射，classStack/deferStack 记上下文，
	// tmpCount 保证 __tl_* 临时变量文件内唯一。障碍是状态交织：并发包装、
	// 类、defer 三栈若混用极易串号；故类与 defer 各自成栈，tmpCount 全局单调。
	// 不变量：所有文本出口必经 write；tmpCount 只增不减。排查现场：临时变量
	// 重名先查 tmpCount 是否被重置，映射漂移先查 line/col 手动篡改。
	opt        Options
	buf        strings.Builder
	indent     int
	classStack []classEmitCtx
	line       int // 生成行号（1 起）
	col        int // 生成列号（0 起，rune）
	pending    *pendingMark
	mappings   []sourcemap.Mapping
	// Defer 帧（每个正在发射的函数体一个，含 chunk）。
	deferStack []*deferFrame
	tmpCount   int // 文件内唯一生成的临时变量计数
}

type classEmitCtx struct {
	name string
	base string // 基类名，无则为空
}

func (e *emitter) curClass() *classEmitCtx {
	// 设计者视角：类栈顶是 super 改写的“施工围挡”——emitClass 入栈、defer 弹栈，
	// 其间 emitExpr 遇 super 即改写为 Base.constructor(self)。障碍是嵌套类：
	// 内层 base 会遮外层，故必须栈而非单变量。候选：AST 回查 vs 栈；选栈，
	// O(1) 且与 checker 的 classStack 对仗。不变量：空栈返 nil，调用方须回退
	// 为普通 super 透传。排查现场：super 未改写先查是否在类外或 base 为空。
	if len(e.classStack) == 0 {
		return nil
	}
	return &e.classStack[len(e.classStack)-1]
}

func (e *emitter) write(s string) {
	// 设计者视角：write 是唯一的行列账本——逐 rune 推进 line/col，顺带在首个
	// 可见字符上冲刷 pending 映射（空格缩进不占映射点）。障碍是多字节 rune：
	// 若按 byte 计列，中文源码映射全偏；故 col 以 rune 计。候选：调用方手动 mark
	// vs 中心冲刷；选中心，保证“mark 后首个可见即锚点”。不变量：凡进 buf 必经
	// 此处，无旁路。排查现场：映射列偏先查是否有人直接动 buf，行漂先查 \n 计数。
	for _, r := range s {
		// 在第一个可见 rune 上冲刷挂起的语句标记。
		if e.pending != nil && r != ' ' && r != '\t' && r != '\n' && r != '\r' {
			sp := e.pending.span.Span()
			e.mappings = append(e.mappings, sourcemap.Mapping{
				GenLine: e.line,
				GenCol:  e.col,
				SrcFile: sp.File,
				SrcLine: sp.Start.Line - 1,
				SrcCol:  sp.Start.Column - 1,
				Name:    e.pending.name,
			})
			e.pending = nil
		}
		e.buf.WriteRune(r)
		if r == '\n' {
			e.line++
			e.col = 0
		} else {
			e.col++
		}
	}
}
func (e *emitter) nl() { e.write("\n") }
func (e *emitter) pad() {
	for i := 0; i < e.indent; i++ {
		e.write("    ")
	}
}

// mark 记录源节点的下一个可见输出位置。
//
// 设计者视角：mark 是 source map 的“预约挂号”——记下 AST 节点与调试名，待
// write 见首个可见 rune 才落子（见 pendingMark）。障碍是空语句/类型擦除节点：
// 若每 mark 必落，会产生悬空映射；故 emitStmt 在擦除分支显式清 pending。
// 取舍：单槽 pending（后来覆盖先来），语句粒度下够用且零队列。不变量：mark 后
// 若无可见输出，pending 应被清而非残留。排查现场：映射错位先查 mark 与 write
// 间是否隔了多余可见字符，缺映射先查是否被后续 mark 覆盖。
func (e *emitter) mark(n ast.Node, name string) {
	e.pending = &pendingMark{span: n, name: name}
}

// stmtName 提取函数定义的调试名。
//
// 设计者视角：stmtName 是映射的“人名牌”——Names 字段进 source map，供调试器
// 按名跳。障碍是方法名有两种拼法（点与冒号）：FuncDefStmt 须把 Prefix 点连、
// Method 冒号接，否则 Dog.new 与 Dog:foo 在映射里分不清。候选：全量语句命名
// vs 仅函数/类；选后者，其余留空以降噪。不变量：非函数/类恒返 ""。排查现场：
// 调试名缺失先查 AST 的 Name.Method 是否为 nil（静态 vs 实例）。
func stmtName(st ast.Stmt) string {
	switch n := st.(type) {
	case *ast.LocalFuncDef:
		return n.Name.Name
	case *ast.FuncDefStmt:
		if n.Name != nil {
			s := ""
			for i, p := range n.Name.Prefix {
				if i > 0 {
					s += "."
				}
				s += p.Name
			}
			if n.Name.Method != nil {
				s += ":" + n.Name.Method.Name
			}
			return s
		}
	case *ast.ClassDecl:
		return n.Name.Name
	}
	return ""
}

func (e *emitter) emitBlock(b *ast.Block, ind int) {
	// 设计者视角：emitBlock 是语句泵的“传送带”——暂存 indent、逐句 emitStmt、
	// 末尾统一 emitReturn（defer 改写藏在 emitReturn 内）。障碍是 indent 泄漏：
	// 嵌套块改 indent 若不恢复，外层缩进全塌；故 saved 快照必配对恢复。候选：
	// 调用方设 indent vs 内部接管；选内部接管，调用方只给目标层。不变量：nil 块
	// 直接返；出口 indent 与入口相等。排查现场：缩进错乱先查是否有提前 return
	// 忘记恢复，return 语义异常先查 emitReturn 的 defer 分支。
	if b == nil {
		return
	}
	saved := e.indent
	e.indent = ind
	for _, st := range b.Stmts {
		e.emitStmt(st)
	}
	if b.Ret != nil {
		e.emitReturn(b.Ret)
	}
	e.indent = saved
}
