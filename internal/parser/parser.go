package parser

import (
	"github.com/typelua/typelua/internal/ast"
	"github.com/typelua/typelua/internal/diag"
	"github.com/typelua/typelua/internal/lexer"
	"github.com/typelua/typelua/internal/token"
)

// Parser 是递归下降 + Pratt 表达式的语法分析器，带错误恢复。
//
// 物理布局（游标与缓冲）：
//
//	Parser
//	├── toks: []token.Token    // 词法产出的 Token 流，只读，从不原地修改
//	├── pos: int               // toks 的读取游标
//	├── pending: []token.Token // consumeGt 拆分 >> / >= 产生的待消费队列
//	├── file: string           // 源文件名（诊断地址）
//	├── diags: []diag.Diagnostic // 累积诊断，单错不掩盖后文
//	├── extendsDepth: int      // >0 表示正在解析 extends 子句（infer 用）
//	├── inMacroBody: int       // >0 表示正在解析宏体
//	└── depth: int             // 当前嵌套深度，超限触发 depthBailout
//
// 游标推进规则：cur() 先看 pending，再看 toks[pos]；next() 同样优先 pending。
// consumeGt 拆分时把余下部分追加到 pending，绝不改 toks，保证试探解析
// 可通过 saveCursor/restoreCursor 零副作用回退。
//
// 为什么把 pos 与 pending 分开：>> 与 >= 在泛型里必须拆成两个 '>'/'='，
// 若原地改 toks 会破坏试探解析的快照恢复；pending 队列让拆分可撤销。
type Parser struct {
	toks         []token.Token
	pos          int
	pending      []token.Token // split remainders from consumeGt (never mutates toks)
	file         string
	diags        []diag.Diagnostic
	extendsDepth int // >0 while parsing an extends clause (for `infer`)
	inMacroBody  int // >0 while parsing a macro definition body
	depth        int // current nesting depth (blocks, primaries, primary types)
}

// maxParseDepth 限制嵌套深度，防止对抗输入（如 10 万层括号）耗尽 goroutine 栈。
// 超限只报一次 TL2015 并产出空 AST，避免恢复产物洪水。
const maxParseDepth = 256

// enterDepth 记账一层嵌套；超限报一次并触发 depthBailout，调用方在 false 后不得继续。
func (p *Parser) enterDepth(sp token.Span) bool {
	p.depth++
	if p.depth > maxParseDepth {
		if p.depth == maxParseDepth+1 {
			p.errorf(diag.ParseNestingTooDeep, sp, "nesting too deep (maximum %d)", maxParseDepth)
		}
		panic(depthBailout{})
	}
	return true
}

// leaveDepth 退出一层嵌套。
func (p *Parser) leaveDepth() { p.depth-- }

// ParseFile 对 src 做词法+语法分析，返回 AST 与诊断。
//
// 契约：对任意输入不 panic（深度超限转为诊断）；返回节点 Span 单调可定位；
// 诊断包含词法阶段的 ldiags 与语法阶段的 diags，顺序为产生顺序。
// 入口先 Lex，再 parseFileBlock；depthBailout 在此被捕获并转为空块。
func ParseFile(filename, src string) (*ast.File, []diag.Diagnostic) {
	toks, ldiags := lexer.Lex(filename, src)
	p := &Parser{toks: toks, file: filename, diags: append([]diag.Diagnostic{}, ldiags...)}
	block := p.parseFileBlock()
	eofSpan := token.Span{File: filename}
	if len(toks) > 0 {
		eofSpan = toks[len(toks)-1].Span
	}
	// Drain to EOF.
	return &ast.File{Name: filename, Block: block, EOF: eofSpan}, p.diags
}

// depthBailout 用于深度超限的 panic 中转：文件病态嵌套时，一个 TL2015
// 加空 AST 胜过恢复产物洪水。
type depthBailout struct{}

// parseFileBlock 解析顶层块，把深度超限转为空块余量。
func (p *Parser) parseFileBlock() (block *ast.Block) {
	defer func() {
		if r := recover(); r != nil {
			if _, ok := r.(depthBailout); !ok {
				panic(r)
			}
			block = &ast.Block{S: token.Span{File: p.file}}
		}
	}()
	return p.parseBlock()
}
