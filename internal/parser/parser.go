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
//
// 设计者视角：Parser 好比流水线上的装配工，toks 是只读料架（lexer 供料，永不返工），
// pos 是手的位置，pending 是手心临时多拿的零件（consumeGt 拆出的 '>'/'='）。
// 障碍是泛型与比较共用 '<'/'>'：`F<A>=v` 在词法层是一个 Ge Token，语义层却要
// 拆成 '>' + '='；若原地改料架，试探失败就拼不回去。取舍是 pending 优先、料架兜底，
// 让 saveCursor/restoreCursor 只需复制小队列即零副作用。不变量：toks 只增游标不改内容。
// 排查现场：若泛型解析后游标错一位，先看 pending 是否在 restore 时被别名复用（必须复制）。
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
//
// 设计者视角：这好比给递归下降系上安全绳——正常代码嵌套不过十几层，
// 256 是“慷慨又安全”的阈值（栈帧小，256 层远未到危险线；恶意输入却动辄上万层）。
// 候选曾是“无限递归靠系统栈报错”，但 panic 堆栈对用户毫无意义；取舍是只报一次
// TL2015 而非每层都报，避免诊断洪水淹没真正的第一错。不变量：depth 归零才算
// 一次 ParseFile 结束。排查现场：若大文件误报 TL2015，先看是否单行生成了深括号
// （如机器拼接的 (((...)))），再考虑调阈值而非绕过 enterDepth。
const maxParseDepth = 256

// enterDepth 记账一层嵌套；超限报一次并触发 depthBailout，调用方在 false 后不得继续。
//
// 设计者视角：它是每间“递归小屋”门口的计数器——进门 +1，出门 leaveDepth -1，
// 满员即拉闸（panic depthBailout）。障碍是 Go 没有尾递归优化，深输入必爆栈，
// 只能在 parser 层主动设卡。取舍：用 panic 做长跳而非层层返回 false，
// 让 parseBlock/parsePrimary/parsePrimaryType 三处深递归统一在 parseFileBlock 收口。
// 不变量：panic 值必为 depthBailout 类型，外层 recover 见到异物必须重抛。
// 排查现场：若看到 depth 只增不减，先查 defer leaveDepth 是否漏写。
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
//
// 设计者视角：它是 enterDepth 的归还动作，好比借书必还——绝大多数调用都以
// `defer p.leaveDepth()` 配对，保证早退（error return）也不漏账。若漏配对，
// depth 单调上涨会把正常文件误判为恶意嵌套。排查现场：depth 异常增大时先
// grep `enterDepth` 看 defer 是否成对。
func (p *Parser) leaveDepth() { p.depth-- }

// ParseFile 对 src 做词法+语法分析，返回 AST 与诊断。
//
// 契约：对任意输入不 panic（深度超限转为诊断）；返回节点 Span 单调可定位；
// 诊断包含词法阶段的 ldiags 与语法阶段的 diags，顺序为产生顺序。
// 入口先 Lex，再 parseFileBlock；depthBailout 在此被捕获并转为空块。
//
// 设计者视角：ParseFile 是全流水线的“总闸”——它是唯一对外承诺“任意字节都不 panic”
// 的入口，lexer 先把源码切成 Token（词法错先收进 ldiags），再由 Parser 装配 AST。
// 障碍是深度超限的 panic 必须在此收口：parseFileBlock 内部 recover 后，这里拿到空块
// 仍能拼出合法 *ast.File（Name/Block/EOF 齐全），让 checker/LSP 下游永不面对 nil。
// 取舍：EOF Span 取末 Token 的 Span 而非零值，保证空文件也有坐标可圈。
// 不变量：返回的 diags = ldiags + 语法 diags，先后即因果。排查现场：若诊断顺序错乱，
// 先看 Lex 与 parse 阶段是否各自追加而非前插。
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
//
// 设计者视角：它是一枚专用“烟雾弹”——空 struct 零分配，只为与用户代码的意外
// panic 相区分（recover 见到非 depthBailout 必重抛，绝不吞业务 panic）。
// 候选曾是逐层返回 error，但深递归有数十个返回点，漏一处就继续野跑；取舍用
// panic 长跳，一处设卡、全线熔断。不变量：它永不携带信息，信息只在 TL2015
// 诊断里。排查现场：若 recover 后仍 panic，先确认抛的是否是本类型。
type depthBailout struct{}

// parseFileBlock 解析顶层块，把深度超限转为空块余量。
//
// 设计者视角：它是 ParseFile 的“防爆舱”——正常时直通 parseBlock，爆炸（depthBailout）
// 时兜出一个带文件名的空 Block，保证 *ast.File 结构完整、Span 可定位。障碍是
// recover 必须精准：只认 depthBailout，别的 panic（如 nil 解引用）要原样上抛，
// 否则会把 bug 伪装成语法错。不变量：成功与兜底两路都返回非 nil Block。
// 排查现场：若空文件/超深文件的 AST 为 nil，先看这里的具名返回值 block 是否被覆盖。
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
