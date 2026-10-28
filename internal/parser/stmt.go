package parser

import (
	"github.com/typelua/typelua/internal/ast"
	"github.com/typelua/typelua/internal/diag"
	"github.com/typelua/typelua/internal/token"
)

// ---------- blocks & statements ----------

// 语句/块物理布局：
//
//	parseBlock
//	  ├── 循环：EOF/End/Else/Elseif/Until/RBrace 停止；';' 作空语句
//	  ├── Return 必须末尾：parseReturn 后 break
//	  └── 其余 parseStmt 分派
//
//	parseStmt 按首 Token 分派：
//	  import/defer/spawn/select/async  (软关键字，isXxxStart 判定)
//	  do/if/while/repeat/for/break/goto/label
//	  function/local/export/declare/macro/type/interface/enum/class/abstract
//	  默认：parseExprStmtOrAssign（赋值或调用语句）
//
// Block.Ret 独立存放 return，结构上保证 return 只在块末。

// parseBlock 解析语句块：循环收语句，Return 居末，遇 EOF/End/Else/Elseif/Until/RBrace 即停。
//
// 设计者视角：parseBlock 是块的“装配循环”——它是全 parser 最忙的 for，一边分发
// parseStmt，一边守着六个哨兵详见实现，排查看 Span。
// 障碍有二：一是 `return` 在 AST 里不住 Stmts 而住 Block.Ret（结构上锁死“末尾唯一”）；
// 二是深度熔断详见实现，排查看 Span。
// 只容 ';' 即 break，让 `return 1; foo()` 的 foo 不被吞；parseStmt 回 nil（撞哨兵）
// 即无声收尾。不变量：返回 Block 永非 nil，S 覆盖首语句到 Ret 尾（空块即零宽）。
// 排查现场：若 return 后语句神秘消失，先看 Ret 分支的 break；若 brace 体提前收尾，
// 先看 RBrace 哨兵。
func (p *Parser) parseBlock() *ast.Block {
	start := p.cur()
	p.enterDepth(start.Span)
	defer p.leaveDepth()
	var stmts []ast.Stmt
	var ret *ast.ReturnStmt
	for {
		k := p.cur().Kind
		if k == token.EOF || k == token.End || k == token.Else || k == token.Elseif || k == token.Until || k == token.RBrace {
			break
		}
		if k == token.Semi {
			s := p.next()
			stmts = append(stmts, &ast.EmptyStmt{S: s.Span})
			continue
		}
		if k == token.Return {
			r := p.parseReturn()
			ret = r
			// return 锁尾：只容身后 `;`，`return 1; foo()` 的 foo 不进块。
			for p.at(token.Semi) {
				p.next()
			}
			break
		}
		st := p.parseStmt()
		if st == nil {
			// 撞哨兵 End 即收尾，不消费留给上层。
			break
		}
		stmts = append(stmts, st)
	}
	end := p.cur().Span
	if len(p.toks) > 0 && p.pos > 0 {
		end = p.toks[p.pos-1].Span
	}
	_ = end
	sp := token.Span{Start: start.Span.Start, End: start.Span.Start, File: p.file}
	if len(stmts) > 0 {
		sp = token.Span{Start: stmts[0].Span().Start, End: stmts[0].Span().End, File: p.file}
		for _, s := range stmts {
			sp.End = s.Span().End
		}
		if ret != nil {
			sp.End = ret.Span().End
		}
	} else if ret != nil {
		sp = ret.Span()
	} else {
		sp = token.Span{Start: start.Span.Start, End: start.Span.Start, File: p.file}
	}
	return &ast.Block{Stmts: stmts, Ret: ret, S: sp}
}

// parseReturn 解析 `return [exprlist]`，裸 return（后接块尾/';'）即空返回。
//
// 设计者视角：parseReturn 是块尾的“封口机”——先吃 `return`，再看下一 Token 是否为
// 块尾哨兵详见实现，排查看 Span。
// 障碍是 select-case 体里的 `return` 后跟 `case`：那不是返回值而是下节标题，
// parseSelectCaseBlock 另有特判，此处只认通用哨兵。取舍：Span 空返回即 start 本身，
// 有值即裹到末 Expr 尾，让“return 圈哪”一眼可判。不变量：永不消费哨兵本身。
// 排查现场：若 `return` 吞了下句，先看 cur 是否为可开表达式的 Token 再断。
func (p *Parser) parseReturn() *ast.ReturnStmt {
	start := p.next() // return
	var exprs []ast.Expr
	// 裸 return：下个是块尾哨兵即空返回，如 `return end`。
	switch p.cur().Kind {
	case token.EOF, token.End, token.Else, token.Elseif, token.Until, token.RBrace, token.Semi:
		break
	default:
		exprs = p.parseExprList()
	}
	sp := token.Span{Start: start.Span.Start, End: start.Span.Start, File: p.file}
	if len(exprs) > 0 {
		sp = token.Span{Start: start.Span.Start, End: exprs[len(exprs)-1].Span().End, File: p.file}
	} else {
		sp = start.Span
	}
	return &ast.ReturnStmt{S: sp, Exprs: exprs}
}

// parseStmt 按首 Token 分发单条语句：软关键字先行详见实现，排查看 Span。
//
// 设计者视角：parseStmt 是语句的“总机房”——先查四路软线（isXxxStart 前瞻，同行才算），
// 再查硬关键字详见实现，排查看 Span。
// 障碍是软关键字平时仍是标识符：`spawn(x)` 是调用而 `spawn f(x)` 是语句，全靠
// isSpawnStart 等的形状+同行双判；`macro Name` 才是定义，`macro(` 即调用。
// 取舍：End/Else/Elseif/Until/EOF/RBrace 回 nil（告诉 parseBlock 收尾），而非造 EmptyStmt，
// 让块边界零噪音。不变量：除哨兵 nil 外永返非 nil Stmt。排查现场：若软关键字语句被当
// 赋值吞，先打印 isXxxStart 的前瞻 Kind 与 sameLine。
func (p *Parser) parseStmt() ast.Stmt {
	if p.isImportStart() {
		return p.parseImport()
	}
	if p.isDeferStart() {
		return p.parseDefer()
	}
	if p.isSpawnStart() {
		return p.parseSpawn()
	}
	if p.isSelectStart() {
		return p.parseSelect()
	}
	if p.isAsyncFuncStart() {
		return p.parseAsyncGlobal()
	}
	switch p.cur().Kind {
	case token.Semi:
		s := p.next()
		return &ast.EmptyStmt{S: s.Span}
	case token.Do:
		return p.parseDo()
	case token.If:
		return p.parseIf()
	case token.While:
		return p.parseWhile()
	case token.Repeat:
		return p.parseRepeat()
	case token.For:
		return p.parseFor()
	case token.Function:
		return p.parseGlobalFunc()
	case token.Local:
		return p.parseLocal()
	case token.Break:
		s := p.next()
		return &ast.BreakStmt{S: s.Span}
	case token.Goto:
		return p.parseGoto()
	case token.ColonColon:
		return p.parseLabel()
	case token.TypeKw, token.InterfaceKw, token.EnumKw, token.ClassKw, token.AbstractKw:
		if p.isTypeDeclStart() {
			if p.at(token.ClassKw) || p.at(token.AbstractKw) {
				return p.parseClassDecl(false, false, false)
			}
			return p.parseTypeDecl(false, false)
		}
		return p.parseExprStmtOrAssign()
	case token.MacroKw:
		// `macro Name` 才是定义，`macro(` 即普通调用。
		if p.peek(1).Kind == token.Ident {
			return p.parseMacroDef()
		}
		return p.parseExprStmtOrAssign()
	case token.ExportKw:
		return p.parseExport()
	case token.DeclareKw:
		return p.parseDeclare()
	case token.End, token.Else, token.Elseif, token.Until, token.EOF, token.RBrace:
		return nil
	default:
		return p.parseExprStmtOrAssign()
	}
}

// isImportStart 是 import 糖试纸：同行 `import "p"`/`import Name from "p"` 才算导入，`import("x")` 仍是调用，换行字符串不粘连。
// 只有同行跟 String如 `import "p"`/`Name from`/`Name: T from`，排查看 Span。
// 障碍是 `import("x")` 仍是函数调用：peek(1)==LParen 即判 false，保住调用语义；
// 次行字符串更不许粘连（sameLine 卡死），避免 `import\n"str"` 被误缝合。
// 取舍：`import Name` 单名无 from 即 false（合法导入必有 from），纯前瞻零消费。
// 排查现场：若导入被当赋值吞，先看 n1/n2 的 Kind 与行号。
func (p *Parser) isImportStart() bool {
	if p.cur().Kind != token.Ident || p.cur().Literal != "import" {
		return false
	}
	n1 := p.peek(1)
	if n1.Span.Start.Line != p.cur().Span.Start.Line {
		return false
	}
	if n1.Kind == token.String {
		return true
	}
	if n1.Kind == token.Ident || isSoftKw(n1.Kind) {
		n2 := p.peek(2)
		// `import Name from`/`import Name: T from` 才算，单 `import Name` 永不成表达式故冒号无需深看。
		if (n2.Kind == token.Ident && n2.Literal == "from") || n2.Kind == token.Colon {
			return true
		}
	}
	return false
}

// parseImport 是 require 糖拆包机：`import "p"`/`import Name from "p"`/`import Name: T from "p"` 归一为 ImportStmt。
// （Path 必有，Name/Type 可选），checker 按 Path 寻模块、按 Type 定名类型。
// 障碍是 Name 可为软关键字（如 `import type from "m"`）：parseIdentAllowSoft 放行。
// 取舍：缺 `from`/缺路径串即记账+同步恢复，不硬凑 AST；Type 冒号后走全 parseType。
// 不变量：成功节点的 S 从 import 起到路径串尾。排查现场：若 `import A: T from "p"`
// 的 T 为空，先看 Colon 分支是否在 from 检查前正确解析。
func (p *Parser) parseImport() ast.Stmt {
	start := p.next() // import
	if p.at(token.String) {
		t := p.next()
		return &ast.ImportStmt{Path: &ast.StringLit{Literal: t.Literal, S: t.Span}, S: token.Span{Start: start.Span.Start, End: p.prevEnd(), File: p.file}}
	}
	name := p.parseIdentAllowSoft()
	if name == nil {
		p.synchronize()
		return &ast.EmptyStmt{S: start.Span}
	}
	var ty ast.TypeNode
	if p.at(token.Colon) {
		p.next()
		ty = p.parseType()
	}
	if !p.at(token.Ident) || p.cur().Literal != "from" {
		p.errorf(diag.ParseUnexpectedSyntax, p.cur().Span, "expected 'from' after import name, got %s", p.cur().Kind)
		p.synchronize()
		return &ast.EmptyStmt{S: start.Span}
	}
	p.next() // from
	if !p.at(token.String) {
		p.errorf(diag.ParseUnexpectedSyntax, p.cur().Span, "expected module path string after 'from', got %s", p.cur().Kind)
		p.synchronize()
		return &ast.EmptyStmt{S: start.Span}
	}
	t := p.next()
	return &ast.ImportStmt{Name: name, Type: ty, Path: &ast.StringLit{Literal: t.Literal, S: t.Span}, S: token.Span{Start: start.Span.Start, End: p.prevEnd(), File: p.file}}
}

// isDeferStart 是 defer 嗅探器：`defer f()` 同行 Ident 才算延迟，`defer(x)` 仍是调用。
// `defer(x)`（次 Token 为括号）与孤 `defer` 一律放回标识符。障碍是方法调用
// `defer o:m()` 的 callee 首词仍是 Ident，嗅探只看首词即够，深层形状交 parseDefer
// 用 parseSuffix 复核。取舍：errdefer 同门判定（Literal 双认），纯前瞻零消费。
// 排查现场：若 `defer f()` 被当调用吞，先看 n1 是否为 Ident 且同行。
func (p *Parser) isDeferStart() bool {
	if p.cur().Kind != token.Ident {
		return false
	}
	if p.cur().Literal != "defer" && p.cur().Literal != "errdefer" {
		return false
	}
	n1 := p.peek(1)
	return n1.Kind == token.Ident &&
		n1.Span.Start.Line == p.cur().Span.Start.Line
}

// parseDefer 是延迟登记处：吃 defer 后用 parseSuffix 收 `f<T>()`/`:m()`，Call/MethodCall 才立案。
// 收 callee（含 `f<T>()` 泛型实参与 `:m()` 方法形），是 Call/MethodCall 即登记
// DeferStmt（IsErr 区分 errdefer），否则记账+同步。障碍是 callee 与普通表达式同形：
// 取舍是 switch 验形而非重解析，`defer x + 1` 当场判死刑，不猜测。
// 不变量：成功 S 从 defer 起到调用尾。排查现场：若 `defer f<T>()` 的 TypeArgs 丢失，
// 先看 parseSuffix 的泛型试探是否因缺 '(' 回退。
func (p *Parser) parseDefer() ast.Stmt {
	start := p.next() // defer | errdefer
	isErr := start.Literal == "errdefer"
	call := p.parseSuffix()
	switch call.(type) {
	case *ast.CallExpr, *ast.MethodCallExpr:
		return &ast.DeferStmt{IsErr: isErr, Call: call, S: token.Span{Start: start.Span.Start, End: p.prevEnd(), File: p.file}}
	default:
		p.errorf(diag.ParseUnexpectedSyntax, p.cur().Span, "expected a function call after '%s'", start.Literal)
		p.synchronize()
		return &ast.EmptyStmt{S: start.Span}
	}
}

// parseDo 解析 `do block end` 裸块，是作用域的最小围栏。
//
// 设计者视角：parseDo 好比搭了个“样板间”——Block 产出独立作用域，checker 进出成对，
// emitter 原样回写 do/end。取舍：expect(End) 失败只记账不停工，让后文继续。
// 不变量：返回 DoBlock 永非 nil，S 从 do 到 end（含恢复后的 cur）。
// 排查现场：若 do 块 Span 尾部偏小，先看 expect 失败后 prevEnd 是否取到同步点。
func (p *Parser) parseDo() ast.Stmt {
	start := p.next()
	b := p.parseBlock()
	e, _ := p.expect(token.End, "'end'")
	_ = e
	return &ast.DoBlock{Block: b, S: token.Span{Start: start.Span.Start, End: p.prevEnd(), File: p.file}}
}

// prevEnd 返回上次消费 Token 的 End（无消费时回落 cur 起点），是 Span 收边的量尺。
//
// 设计者视角：prevEnd 是“上一步的脚印”——绝大多数节点的 S.End 都取它，保证
// Span 恰盖到已吃部分，不多不少。障碍是 pending 拆分余量不在 toks 里：此处只看
// toks[pos-1]，余量视为未落定，不计入长度。取舍：空消费回落 cur.Start 而非零值，
// 让空块/空参仍有文件内坐标。不变量：纯只读。排查现场：若节点尾多出一位，
// 先数 next 次数，再看 pending 是否有残留被误计。
func (p *Parser) prevEnd() token.Pos {
	if p.pos > 0 {
		return p.toks[p.pos-1].Span.End
	}
	return p.cur().Span.Start
}

// parseIf 解析 `if cond then b [elseif ..] [else ..] end` 完整分支链。
//
// 设计者视角：parseIf 是分支的“糖葫芦串”——then/elseif/else 各自 parseBlock，
// elseif 循环收，直到无 Elseif 即看 Else，一律 expect(End) 封口。障碍是 cond 与
// then 之间无括号分隔：parseExpr 贪吃会把 `then` 当变量？实则 `then` 为关键字，
// Pratt 的 binPrec 回 0 即停，天然分界。取舍：cond 为 nil（空条件）仍继续搭块，
// 让诊断定位到 then/end 而非整串作废。不变量：S 从 if 到 end。
// 排查现场：若 elseif 链断裂，先看 expect(Then) 失败后游标是否停在原地。
func (p *Parser) parseIf() ast.Stmt {
	start := p.next()
	cond := p.parseExpr()
	p.expect(token.Then, "'then'")
	thenB := p.parseBlock()
	var elseifs []*ast.ElseifClause
	for p.at(token.Elseif) {
		es := p.next()
		c := p.parseExpr()
		p.expect(token.Then, "'then'")
		b := p.parseBlock()
		elseifs = append(elseifs, &ast.ElseifClause{Cond: c, Block: b, S: token.Span{Start: es.Span.Start, End: b.Span().End, File: p.file}})
	}
	var elseB *ast.Block
	if p.at(token.Else) {
		p.next()
		elseB = p.parseBlock()
	}
	p.expect(token.End, "'end'")
	return &ast.IfStmt{Cond: cond, Then: thenB, Elseifs: elseifs, Else: elseB, S: token.Span{Start: start.Span.Start, End: p.prevEnd(), File: p.file}}
}

// parseWhile 解析 `while cond do block end` 循环。
//
// 设计者视角：parseWhile 是“门卫循环”——cond 在门外验详见实现，排查看 Span。
// Block 进场，end 关门，形态固定故无分流。取舍：与 parseIf 共用 cond 解析，
// 优先级语义一致（`while a and b do` 即 `(a and b)`）。不变量：S 从 while 到 end。
// 排查现场：若 `do` 缺失后块被吞，先看 expect(Do) 失败后 parseBlock 是否从错误位置起块。
func (p *Parser) parseWhile() ast.Stmt {
	start := p.next()
	c := p.parseExpr()
	p.expect(token.Do, "'do'")
	b := p.parseBlock()
	p.expect(token.End, "'end'")
	return &ast.WhileStmt{Cond: c, Block: b, S: token.Span{Start: start.Span.Start, End: p.prevEnd(), File: p.file}}
}

// parseRepeat 解析 `repeat block until cond`（cond 在块后，作用域含块内变量）。
//
// 设计者视角：parseRepeat 是“先上车后验票”——Block 先行，until 后 cond 殿后，
// 与 while 的“先验后进”镜像。障碍是 cond 可见块内 locals（Lua 语义）：AST 只保序
// （Block 在前、Cond 在后），作用域由 checker 按序建。取舍：S 收到 cond 尾而非 until，
// 让高亮盖住条件。排查现场：若 until 条件定位偏前，先看 S.End 是否取了 cond 而非 until。
func (p *Parser) parseRepeat() ast.Stmt {
	start := p.next()
	b := p.parseBlock()
	p.expect(token.Until, "'until'")
	c := p.parseExpr()
	return &ast.RepeatStmt{Block: b, Cond: c, S: token.Span{Start: start.Span.Start, End: c.Span().End, File: p.file}}
}

// parseFor 解析数字 for（`for i [:T] = s,e[,step] do`）与泛型 for（`for k[:K],v[:V] in exprs do`）双形态。
//
// 设计者视角：parseFor 是岔路最多的循环解析——先收首名+可选类型，再看 '=' 即数字路
// （s,e,step 三值），否则即泛型路（逗号续名、`in` 后 exprlist）。障碍是类型注记可
// 紧贴循环变量如 `for i: integer = ...`，排查看 Span。
// 会把 `: integer` 当比较。取舍：数字/泛型两路各产专属节点如 `: integer`，排查看 Span。
// 不共用通泛结构，让 checker 不必二次分流。不变量：S 从 for 到 end。
// 排查现场：若 `for k,v in ...` 的 v 丢失，先看逗号循环的 break 条件；若 step 被吞，
// 先看第二逗号分支。
func (p *Parser) parseFor() ast.Stmt {
	start := p.next()
	name := p.parseIdentAllowSoft()
	if name == nil {
		p.synchronize()
		return &ast.EmptyStmt{S: start.Span}
	}
	// 循环变量后可选类型：`for i: integer = …` 数字路，`for k: K, v: V in …` 泛型路。
	var firstTy ast.TypeNode
	if p.at(token.Colon) {
		p.next()
		firstTy = p.parseType()
	}
	if p.at(token.Assign) {
		// 数字路 `for i = s,e[,step] do`。
		p.next()
		s := p.parseExpr()
		p.expect(token.Comma, "','")
		e := p.parseExpr()
		var step ast.Expr
		if p.at(token.Comma) {
			p.next()
			step = p.parseExpr()
		}
		p.expect(token.Do, "'do'")
		b := p.parseBlock()
		p.expect(token.End, "'end'")
		return &ast.NumericFor{Var: name, VarTy: firstTy, Start: s, End: e, Step: step, Block: b, S: token.Span{Start: start.Span.Start, End: p.prevEnd(), File: p.file}}
	}
	// 泛型路 `for k,v in exprs do`。
	names := []*ast.Ident{name}
	types := []ast.TypeNode{firstTy}
	for p.at(token.Comma) {
		p.next()
		n := p.parseIdentAllowSoft()
		if n == nil {
			break
		}
		var ty ast.TypeNode
		if p.at(token.Colon) {
			p.next()
			ty = p.parseType()
		}
		names = append(names, n)
		types = append(types, ty)
	}
	p.expect(token.In, "'in'")
	exprs := p.parseExprList()
	p.expect(token.Do, "'do'")
	b := p.parseBlock()
	p.expect(token.End, "'end'")
	return &ast.GenericFor{Names: names, Types: types, Exprs: exprs, Block: b, S: token.Span{Start: start.Span.Start, End: p.prevEnd(), File: p.file}}
}

// parseGoto 解析 `goto label` 跳转，目标由 parseIdentAllowSoft 接纳软关键字名。
//
// 设计者视角：parseGoto 是“贴邮票”——只记 Label 名，不验标签是否存在（未定义标签由
// checker 全文件复核，parser 不背符号表）。取舍：名缺席即 EmptyStmt 占位，保证块不断。
// 不变量：S 从 goto 到名尾。排查现场：若 goto 目标错位，先看 Label 名的 Span 是否含冒号。
func (p *Parser) parseGoto() ast.Stmt {
	start := p.next()
	n := p.parseIdentAllowSoft()
	if n == nil {
		return &ast.EmptyStmt{S: start.Span}
	}
	return &ast.GotoStmt{Label: n, S: token.Span{Start: start.Span.Start, End: n.Span().End, File: p.file}}
}

// parseLabel 解析 `::name::` 标签定义，前后双冒号必须配对。
//
// 设计者视角：parseLabel 是 goto 的“收件地址”——`::` 开门、名居中、`::` 关门，
// 三件套齐才算合法地址。障碍是单冒号 `:label:` 与类型冒号同形：此处 expect 的是
// ColonColon 双冒号 Token，单冒号直接判死刑。取舍：名缺席即 EmptyStmt，不硬凑。
// 不变量：S 从首 `::` 到尾 `::`。排查现场：若标签 Span 尾部缺半，先看尾 expect 是否失败。
func (p *Parser) parseLabel() ast.Stmt {
	start := p.next() // ::
	n := p.parseIdentAllowSoft()
	if n == nil {
		return &ast.EmptyStmt{S: start.Span}
	}
	p.expect(token.ColonColon, "'::'")
	return &ast.LabelStmt{Name: n, S: token.Span{Start: start.Span.Start, End: p.prevEnd(), File: p.file}}
}

// parseGlobalFunc 解析 `function a.b:c<T>(...) ... end` 全局函数（含泛型试探与方法 self）。
//
// 设计者视角：parseGlobalFunc 是全局函数的“上户”——parseFuncName 先定户名
// （点链+冒号方法），`<T>` 泛型试探（后接 '(' 才落户，否则按比较回退），
// parseFuncBody 再按 Method 是否非空定 IsMethod（含隐式 self）。障碍是 `<` 双义：
// 取舍是 saveCursor+诊断回退的零副作用试探，错判成本仅一次小回退。
// 不变量：S 从 function 到体尾，Generics 为 nil 即无泛型。排查现场：
// 若 `function f < T` 被吞成比较，先看试探后 at(LParen) 是否为真。
func (p *Parser) parseGlobalFunc() ast.Stmt {
	start := p.next() // function
	name := p.parseFuncName()
	if name == nil {
		p.synchronize()
		return &ast.EmptyStmt{S: start.Span}
	}
	var generics []*ast.GenericParam
	if p.at(token.Lt) {
		// `<T>` 后须跟 `(` 才算泛型，否则按比较回退，如 `function f<T>(x)`。
		save := p.saveCursor()
		nd := len(p.diags)
		g := p.parseGenericParams()
		if g != nil && p.at(token.LParen) {
			generics = g
		} else {
			p.restoreCursor(save)
			p.diags = p.diags[:nd]
		}
	}
	fn := p.parseFuncBody(name.Method != nil)
	fn.S = token.Span{Start: start.Span.Start, End: fn.Span().End, File: p.file}
	return &ast.FuncDefStmt{Name: name, Generics: generics, Func: fn, S: token.Span{Start: start.Span.Start, End: fn.Span().End, File: p.file}}
}

// parseFuncName 解析 `a.b.c:m` 函数全名：点链为 Prefix，冒号后为 Method（隐式 self）。
//
// 设计者视角：parseFuncName 是函数名的“门牌拆解”——`a.b:c` 拆成 Prefix=[a b]+Method=c，
// 纯 `f` 即单 Prefix 无 Method。障碍是残缺名（`function a.` 缺尾）：取舍是即时回
// FuncName（已有 Prefix 可用），而非全盘 nil，让诊断能圈住已吃部分。
// 不变量：首 Ident 缺席即回 nil（调用方同步恢复）；Method 最多一个。
// 排查现场：若方法 self 丢失，先看 Method 是否非空，再看 parseFuncBody 的 isMethod 入参。
func (p *Parser) parseFuncName() *ast.FuncName {
	first := p.parseIdentAllowSoft()
	if first == nil {
		return nil
	}
	parts := []*ast.Ident{first}
	for p.at(token.Dot) {
		p.next()
		n := p.parseIdentAllowSoft()
		if n == nil {
			return &ast.FuncName{Prefix: parts, S: token.Span{Start: first.Span().Start, End: p.prevEnd(), File: p.file}}
		}
		parts = append(parts, n)
	}
	var method *ast.Ident
	if p.at(token.Colon) {
		p.next()
		method = p.parseIdentAllowSoft()
	}
	end := p.prevEnd()
	return &ast.FuncName{Prefix: parts, Method: method, S: token.Span{Start: first.Span().Start, End: end, File: p.file}}
}

// parseLocal 解析 local 全家桶：local function/async/type/class/macro 优先分流，余者为多变量赋值。
//
// 设计者视角：parseLocal 是 local 的“分诊台”——async function、type/interface/enum、
// class、macro 各有专诊（且各有“像声明才进”的二次确认，如 `local type = 1` 仍是变量），
// 都不是才进通用多变量路（`name [attr] [: T]` 逗号列 + 可选 `= exprlist`）。
// 障碍有二：一是 `x <attr>: T` 与 `x: T <attr>` 两种注记顺序都要吃；二是 attr 的
// `<const>`/`<close>` 与泛型 `<T>` 同形，需 parseAttr 三步硬判如 `<const>`/`<close>`/`<T>`，排查看 Span。
// 取舍：声明分流用 peek 二三步前瞻零消费，误判宁可走变量路（后验可纠），不提前吞关键字。
// 不变量：names 为空即记账+同步回 EmptyStmt。排查现场：若 `local type Foo = ...`
// 未进声明，先看 peek(1)/peek(2) 的 Kind 是否满足 =/</{/extends 四选一。
func (p *Parser) parseLocal() ast.Stmt {
	start := p.next() // local
	if p.isAsyncFuncStart() {
		return p.parseAsyncLocalFunc(start)
	}
	if p.at(token.Function) {
		p.next()
		name := p.parseIdentAllowSoft()
		if name == nil {
			p.synchronize()
			return &ast.EmptyStmt{S: start.Span}
		}
		var generics []*ast.GenericParam
		if p.at(token.Lt) {
			save := p.saveCursor()
			nd := len(p.diags)
			g := p.parseGenericParams()
			if g != nil && p.at(token.LParen) {
				generics = g
			} else {
				p.restoreCursor(save)
				p.diags = p.diags[:nd]
			}
		}
		fn := p.parseFuncBody(false)
		sp := token.Span{Start: start.Span.Start, End: fn.Span().End, File: p.file}
		return &ast.LocalFuncDef{Name: name, Generics: generics, Func: fn, S: sp}
	}
	if p.at(token.TypeKw) || p.at(token.InterfaceKw) || p.at(token.EnumKw) {
		// `local type Foo = ...` is a decl; `local type = ...` is a variable named `type`.
		// Decl requires: keyword + Ident + (`=`/`<`/`{`/extends).
		if p.peek(1).Kind == token.Ident {
			k2 := p.peek(2).Kind
			if k2 == token.Assign || k2 == token.Lt || k2 == token.LBrace || k2 == token.ExtendsKw {
				return p.parseTypeDecl(false, false)
			}
		}
	}
	if p.at(token.ClassKw) && p.peek(1).Kind == token.Ident {
		return p.parseClassDecl(false, false, true)
	}
	if p.at(token.AbstractKw) && p.peek(1).Kind == token.ClassKw && p.peek(2).Kind == token.Ident {
		return p.parseClassDecl(false, false, true)
	}
	if p.at(token.MacroKw) && p.peek(1).Kind == token.Ident {
		return p.parseMacroDef()
	}
	// local name [attr] [: type] [, ...] [= exprlist]
	var names []*ast.Ident
	var attrs []string
	var types []ast.TypeNode
	for {
		n := p.parseIdentAllowSoft()
		if n == nil {
			break
		}
		attr := p.parseAttr()
		var ty ast.TypeNode
		// Support both `x <attr>: T` and `x: T <attr>`.
		if p.at(token.Colon) {
			p.next()
			ty = p.parseType()
			if attr == "" {
				attr = p.parseAttr()
			}
		}
		names = append(names, n)
		attrs = append(attrs, attr)
		types = append(types, ty)
		if !p.at(token.Comma) {
			break
		}
		p.next()
	}
	var values []ast.Expr
	if p.at(token.Assign) {
		p.next()
		values = p.parseExprList()
	}
	if len(names) == 0 {
		p.errorf(diag.ParseExpectedLocalName, p.cur().Span, "expected local variable name")
		p.synchronize()
		return &ast.EmptyStmt{S: start.Span}
	}
	end := p.prevEnd()
	return &ast.LocalAssign{Names: names, Attrs: attrs, Types: types, Values: values, S: token.Span{Start: start.Span.Start, End: end, File: p.file}}
}

// parseAttr 是 local 邮戳检查：`<`+Ident+`>` 三连且名在 const/close 白名单才算，余者零消费退回。
// const/close 白名单才算戳，否则零消费原样退回（把 `<` 留给泛型/比较）。
// 障碍是 `<` 高度复用：取舍是 peek(1)/peek(2) 硬判三件套，不满足即回 ""，绝不试探
// 消费。候选曾是复用泛型解析，但属性语法极简，专用三步更快。不变量：成功必消费三 Token；
// 未知属性名仍消费但追加诊断（错名不掩盖后文）。排查现场：若 `<const>` 被当比较吞，
// 先看 peek(2) 是否为 Gt。
func (p *Parser) parseAttr() string {
	if !p.at(token.Lt) {
		return ""
	}
	// 须 Ident 后紧跟 `>` 才算，`>>` 亦不认。
	if p.peek(1).Kind != token.Ident {
		return ""
	}
	// 属性极简只认单个 `>`，`>>`/`>=` 到此即止。
	if p.peek(2).Kind != token.Gt {
		return ""
	}
	p.next() // <
	name := p.next()
	p.next() // >
	if name.Literal != "const" && name.Literal != "close" {
		p.errorf(diag.ParseUnknownAttribute, name.Span, "unknown attribute <%s>, expected <const> or <close>", name.Literal)
	}
	return name.Literal
}
