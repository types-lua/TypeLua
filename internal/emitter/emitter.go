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
func DefaultOptions() Options { return Options{Target: "5.4"} }

// EmitFile 把已解析文件渲染为 Lua 源码（类型擦除）。
func EmitFile(f *ast.File, opt Options) string {
	out, _ := EmitFileWithMap(f, opt)
	return out
}

// EmitFileWithMap 渲染 Lua 源码及 source-map 条目（语句粒度、rune 列）。
// 条目始终收集；仅当调用方需要映射时才编码（开销近似为零）。
// 使用协作并发（spawn/await/select/通道）的文件获得调度器前导与
// `__tl_run(function() ... end)` 代码块包装（可重入，故被 require 的
// 模块继承调用方的任务）。
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
	span ast.Node
	name string
}

type emitter struct {
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
	if len(e.classStack) == 0 {
		return nil
	}
	return &e.classStack[len(e.classStack)-1]
}

func (e *emitter) write(s string) {
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
func (e *emitter) mark(n ast.Node, name string) {
	e.pending = &pendingMark{span: n, name: name}
}

// stmtName 提取函数定义的调试名。
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
