package parser

import (
	"github.com/typelua/typelua/internal/ast"
	"github.com/typelua/typelua/internal/diag"
	"github.com/typelua/typelua/internal/token"
)

// 并发全软关键字（保 Lua 超集）：`async function f() end`/`spawn f(x)`/`await x`/`select{case…}`，余位仍是变量（`local await=1`/`spawn(x)`）；判定靠 isXxxStart 同行前瞻。

// isIdentNamed 报告 Token 是否为指定名的普通标识符（Kind 必为 Ident，Literal 全等）。
//
// 设计者视角：它是软关键字的“点名器”——async/await/spawn/select 在词法层全是 Ident，
// 本函数按名点卯，Kind 先卡（关键字 Kind 不算），Literal 再对。取舍：大小写敏感全等，
// 不做折叠，让 `Async` 仍是普通变量。不变量：纯函数，无副作用。
// 排查现场：若软关键字不生效，先看 Literal 是否含前后空格（词法切分问题）而非此处。
func isIdentNamed(t token.Token, name string) bool {
	return t.Kind == token.Ident && t.Literal == name
}

// sameLine 报告两 Token 是否同行，是软关键字判定的“防粘连胶”。
//
// 设计者视角：sameLine 好比给软关键字加了“同桌 شرط”——`async` 次行跟 `function`
// 不算异步（可能是两句），`spawn` 次行跟调用不算派生。障碍是 Lua 允许换行续写：
// 取舍是并发四兄弟详见实现，排查看 Span。
// 换行即拆成两句，保住“裸词仍是变量”的超集承诺。不变量：只看 Start.Line，不看列。
// 排查现场：若行尾 `\r\n` 导致误判跨行，先看 Pos 行号计算而非此处。
func sameLine(a, b token.Token) bool {
	return a.Span.Start.Line == b.Span.Start.Line
}

// ---------- async ----------

// isAsyncFuncStart 是异步双敲门：同行 `async function` 才算，孤 `async` 仍是变量。
//
// 设计者视角：isAsyncFuncStart 是异步的“双敲门”——cur 为 `async` 且 peek(1) 为
// Function 且同行，三齐才算，`async` 单独仍是变量（`local async = 1` 活路保留）。
// 取舍：纯前瞻零消费，误判宁可 false（回落普通标识符路），不提前吞 async。
// 不变量：只读游标。排查现场：若 `async function` 被拆成两变量，先看 sameLine 与
// Function 的 Kind（是否为软关键字误标）。
func (p *Parser) isAsyncFuncStart() bool {
	if !isIdentNamed(p.cur(), "async") {
		return false
	}
	n1 := p.peek(1)
	return n1.Kind == token.Function && sameLine(p.cur(), n1)
}

// parseAsyncFuncLit 装配匿名异步：`async <T> function` 双位泛型试探+parseFuncBody，IsAsync 立旗。
//
// 设计者视角：parseAsyncFuncLit 是匿名异步的“装配台”——吃 async 后两处泛型试探
// （`async <T> function` 与 `async function <T>(` 双位，首位命中即用，次位回补），
// 再吃 function 走 parseFuncBody，最后 IsAsync 立旗、S 前移到 async。
// 障碍是泛型 `<` 与比较同形：两处皆 saveCursor+诊断回退，后接 '(' 才提交。
// 取舍：`async` 后无 function 即记账回空体 FuncLit（IsAsync 照立），不吞后文。
// 不变量：返回 FuncLit 永非 nil。排查现场：若泛型 `<T>` 丢失，先看两处试探的
// at(LParen) 提交条件。
func (p *Parser) parseAsyncFuncLit(isMethod bool) *ast.FuncLit {
	start := p.next() // async
	// 匿名异步可选泛型 `async <T> function`。
	var fgen []*ast.GenericParam
	if p.at(token.Lt) {
		save := p.saveCursor()
		nd := len(p.diags)
		g := p.parseGenericParams()
		if g != nil && p.at(token.LParen) {
			fgen = g
		} else {
			p.restoreCursor(save)
			p.diags = p.diags[:nd]
		}
	}
	// 此处 cur 应为 function，吃掉再走 parseFuncBody（与 parsePrimary 镜像）。
	if !p.at(token.Function) {
		p.errorf(diag.ParseUnexpectedSyntax, p.cur().Span, "expected 'function' after 'async'")
		return &ast.FuncLit{Body: &ast.Block{}, IsAsync: true, S: start.Span}
	}
	p.next() // function
	var fgen2 []*ast.GenericParam
	if p.at(token.Lt) {
		save := p.saveCursor()
		nd := len(p.diags)
		g := p.parseGenericParams()
		if g != nil && p.at(token.LParen) {
			fgen2 = g
		} else {
			p.restoreCursor(save)
			p.diags = p.diags[:nd]
		}
	}
	if fgen == nil {
		fgen = fgen2
	}
	fn := p.parseFuncBody(isMethod)
	fn.Generics = append(fgen, fn.Generics...)
	fn.IsAsync = true
	fn.S = token.Span{Start: start.Span.Start, End: fn.Span().End, File: p.file}
	return fn
}

// parseAsyncGlobal 给全局异步上户：`async function 名<T>(…) end`，IsAsync 立旗。
//
// 设计者视角：parseAsyncGlobal 是全局异步的“上户”——async、function 双吃后
// parseFuncName 定户名，泛型试探（后接 '(' 才落户），parseFuncBody 按 Method 定 self，
// IsAsync 立旗、S 前移到 async。障碍与取舍同 parseGlobalFunc（`<` 双义零副作用试探），
// 唯多 async 前缀。名缺席即 synchronize 回 Empty，不硬凑。不变量：S 从 async 到体尾。
// 排查现场：若 `export async function` 的 Span 漏 async，先看 parseExport 是否前移了 Start。
func (p *Parser) parseAsyncGlobal() ast.Stmt {
	start := p.next() // async
	p.next()          // function
	name := p.parseFuncName()
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
	fn := p.parseFuncBody(name.Method != nil)
	fn.IsAsync = true
	fn.S = token.Span{Start: start.Span.Start, End: fn.Span().End, File: p.file}
	return &ast.FuncDefStmt{Name: name, Generics: generics, Func: fn, S: token.Span{Start: start.Span.Start, End: fn.Span().End, File: p.file}}
}

// parseAsyncLocalFunc 给局部异步分户：`local async function 名(…) end`，Span 含 local。
//
// 设计者视角：parseAsyncLocalFunc 是局部异步的“分户”——调用方详见实现，排查看 Span。
// 并以前瞻确认 `async function`，此处吃 async/function 后 AllowSoft 取名（`type` 们
// 可作函数名），泛型试探、parseFuncBody(false)（local 无 self），S 从 local 起。
// 取舍：名缺席即同步回 Empty，local 的 Start 由入参透传，保证 Span 含 local。
// 不变量：返回 LocalFuncDef（非全局）。排查现场：若 local async 的 Span 漏 local，
// 先看入参 start 是否被正确用于 S.Start。
func (p *Parser) parseAsyncLocalFunc(start token.Token) ast.Stmt {
	astart := p.next() // async
	_ = astart
	p.next() // function
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
	fn.IsAsync = true
	sp := token.Span{Start: start.Span.Start, End: fn.Span().End, File: p.file}
	return &ast.LocalFuncDef{Name: name, Generics: generics, Func: fn, S: sp}
}

// ---------- await ----------

// isAwaitPrefix 是 await 验钞机：同行跟操作数且非 `await(x)`/`await{}` 调用形才算挂起，孤词仍是变量。
//
// 设计者视角：isAwaitPrefix 是 await 的“验钞机”——`await` 平时仍是变量，只有“同行跟操作数
// 且非调用形”才算挂起标记。障碍是 Lua 调用糖：`await(x)`/`await{}`/`await""/await./await:/await[`
// 一律判 false（调用赢），孤 await（后跟分隔符/运算符/换行/EOF）亦判 false（变量赢），
// `await -x` 例外收（常见），`await + 1` 判变量（binary 位）。取舍：纯前瞻零消费，
// 判错成本仅走错一条路（变量 vs 标记），由 parseUnary 的首判统一收口。
// 不变量：只读。排查现场：若 `await f()` 被当标记，先看 LParen 豁免分支；若 `await\ng()` 粘连，
// 先看 sameLine。
func (p *Parser) isAwaitPrefix() bool {
	if !isIdentNamed(p.cur(), "await") {
		return false
	}
	n1 := p.peek(1)
	if n1.Kind == token.EOF {
		return false
	}
	if !sameLine(p.cur(), n1) {
		return false
	}
	switch n1.Kind {
	case token.LParen, token.LBrace, token.String, token.Dot, token.Colon, token.LBrack:
		return false
	case token.Comma, token.Semi, token.RParen, token.RBrace, token.RBrack,
		token.Assign, token.Then, token.Do, token.Else, token.Elseif, token.End,
		token.Until, token.In, token.EOF:
		return false
	case token.Plus, token.Minus, token.Star, token.Slash, token.DoubleSlash,
		token.Percent, token.Caret, token.Hash, token.Amp, token.Tilde,
		token.Pipe, token.ShiftL, token.ShiftR, token.DotDot,
		token.Lt, token.Le, token.Gt, token.Ge, token.Eq, token.Ne,
		token.And, token.Or, token.ColonColon, token.Question, token.Arrow,
		token.FatArrow, token.Bang:
		// `await + 1` 按变量+1，唯 `await -x` 按挂起收（常见）。
		return false
	}
	// 余下 not/#/~/-/Ident/数字等皆为操作数。
	return true
}

// parseAwait 解析 `await <expr>` 挂起标记（以 precUnary 收操作数，缺操作数即回 Ident 占位）。
//
// 设计者视角：parseAwait 好比在表达式上盖“暂停章”——吃 await 后以 precUnary 吃操作数
// （与一元同级，保证 `await -x` 即 `await(-x)`、`await a + b` 按 Pratt 续吃）。
// 障碍是裸 await如 `local x = await`，排查看 Span。
// 让变量语义不断链。取舍：S 从 await 到操作数尾（发射期擦除为 expr，Span 仍可溯）。
// 不变量：永返非 nil Expr。排查现场：若 `await` 高亮偏小，先看 S.End 是否取到操作数尾。
func (p *Parser) parseAwait() ast.Expr {
	start := p.next() // await
	x := p.parseExprPrec(precUnary)
	if x == nil {
		p.errorf(diag.ParseExpectedExpression, p.cur().Span, "expected expression after 'await'")
		return &ast.Ident{Name: "await", S: start.Span}
	}
	return &ast.AwaitExpr{X: x, S: token.Span{Start: start.Span.Start, End: x.Span().End, File: p.file}}
}

// ---------- spawn ----------

// isSpawnStart 是 spawn 安检门：同行 `spawn f(x)` 放行，`spawn(x)`/`spawn.foo` 仍是值用；`spawn 42` 放进去再判死刑。
//
// 设计者视角：isSpawnStart 是 spawn 的“安检门”——`spawn` 后同行跟“像调用开头”的才放行
// 进 parseSpawn 复核（含 `spawn 42` 这种错写，放进去再判死刑以便定位）；
// 点/冒号/括号/花括号/串/中括号/运算符/分隔/关键字一律判 false（`spawn(x)` 仍是调用、
// `spawn.foo` 仍是索引）。障碍是误判成本不对称：false 即变量路（安全），true 即语句路
// （parseSpawn 会验形），故“拿不准的同行余项”取 true 放进去验。纯前瞻零消费。
// 排查现场：若 `spawn f(x)` 被当赋值吞，先看 n1 的 Kind 是否在豁免 switch 里。
func (p *Parser) isSpawnStart() bool {
	if !isIdentNamed(p.cur(), "spawn") {
		return false
	}
	n1 := p.peek(1)
	if n1.Kind == token.EOF {
		return false
	}
	if !sameLine(p.cur(), n1) {
		return false
	}
	switch n1.Kind {
	case token.Dot, token.Colon, token.LParen, token.LBrace, token.String,
		token.LBrack, token.Comma, token.Semi, token.RParen, token.RBrace,
		token.RBrack, token.Assign, token.Then, token.Do, token.Else,
		token.Elseif, token.End, token.Until, token.In, token.ColonColon,
		token.Question, token.Arrow, token.FatArrow, token.Bang,
		token.Plus, token.Star, token.Slash, token.DoubleSlash,
		token.Percent, token.Caret, token.Amp, token.Tilde,
		token.Pipe, token.ShiftL, token.ShiftR, token.DotDot,
		token.Lt, token.Le, token.Gt, token.Ge, token.Eq, token.Ne,
		token.And, token.Or, token.Not, token.Hash:
		return false
	}
	return true
}

// parseSpawn 解析 `spawn <call>` 派生语句：匿名 function 体隐式 async，余者须为 Call/MethodCall。
//
// 设计者视角：parseSpawn 是任务的“发射架”——`spawn function() ... end(args)` 双路：
// async 前缀走 parseAsyncFuncLit，裸 function 吃后收泛型试探+parseFuncBody 并 IsAsync 立旗
// （体跑在任务里，await/select 天然可用），尾随 callArgs 可选（无参即空片）；
// 非匿名走 parseSuffix 验形（Call/MethodCall 即 SpawnStmt，否则记账+同步回 Empty）。
// 障碍是 `spawn 42` 错写：isSpawnStart 已放行，此处 switch 即判死刑，诊断锚在 cur。
// 取舍：S 从 spawn 到调用尾（含隐式 async 体）。不变量：成功 Call 必为调用形。
// 排查现场：若 `spawn o:m()` 的方法丢失，先看 parseSuffix 的 Colon 分支。
func (p *Parser) parseSpawn() ast.Stmt {
	start := p.next() // spawn
	// 匿名 `spawn function() end(args)` 体作任务跑，隐式 async，内可用 await/select。
	if p.at(token.Function) || (isIdentNamed(p.cur(), "async") && p.peek(1).Kind == token.Function && sameLine(p.cur(), p.peek(1))) {
		var fn ast.Expr
		if isIdentNamed(p.cur(), "async") {
			fn = p.parseAsyncFuncLit(false)
		} else {
			p.next() // function
			var fgen []*ast.GenericParam
			if p.at(token.Lt) {
				save := p.saveCursor()
				nd := len(p.diags)
				g := p.parseGenericParams()
				if g != nil && p.at(token.LParen) {
					fgen = g
				} else {
					p.restoreCursor(save)
					p.diags = p.diags[:nd]
				}
			}
			fb := p.parseFuncBody(false)
			fb.Generics = fgen
			fb.IsAsync = true
			fn = fb
		}
		// `spawn function() ... end(args)`; the trailing call is optional
		// (`spawn function() ... end` spawns with no arguments).
		args := p.parseCallArgs()
		if args == nil {
			args = []ast.Expr{}
		}
		call := &ast.CallExpr{Func: fn, Args: args, S: token.Span{Start: start.Span.Start, End: p.prevEnd(), File: p.file}}
		return &ast.SpawnStmt{Call: call, S: call.Span()}
	}
	call := p.parseSuffix()
	switch call.(type) {
	case *ast.CallExpr, *ast.MethodCallExpr:
		return &ast.SpawnStmt{Call: call, S: token.Span{Start: start.Span.Start, End: p.prevEnd(), File: p.file}}
	default:
		p.errorf(diag.ParseUnexpectedSyntax, p.cur().Span, "expected a function call after 'spawn'")
		p.synchronize()
		return &ast.EmptyStmt{S: start.Span}
	}
}

// ---------- select ----------

// isSelectStart 是 select 试纸：同行 `select {` 才算多路，`select(x)` 仍是调用。
//
// 设计者视角：isSelectStart 是 select 的“试纸”——`select` 后同行跟 `{` 才算多路复用，
// `select(...)` 仍是普通调用（Lua 超集活路）。纯两步前瞻零消费，行跨即 false。
// 排查现场：若 `select\n{` 未生效，先看 sameLine（换行即拆两句是刻意取舍）。
func (p *Parser) isSelectStart() bool {
	if !isIdentNamed(p.cur(), "select") {
		return false
	}
	n1 := p.peek(1)
	return n1.Kind == token.LBrace && sameLine(p.cur(), n1)
}

// parseSelect 解析 `select { case ... default ... }` Go 式多路复用（含 default 唯一/末位校验）。
//
// 设计者视角：parseSelect 是多路的“接线总机”——`{` 后循环：`;`/`,` 跳过，`default`
// 收块（then/`:`/换行块三式皆容，重 default 即记账，seenDefault 立旗），`case`
// 交 parseSelectCase（default 后再 case 即记“default 须居末”），余者记账+synchronizeSelect
// 跳到下节。`}` 封口，零 case+无 default 即记“至少一节”。障碍是分隔符混用与 default 位置：
// 取舍是错位只记账不停工（sc 照收），让后文继续。不变量：返回 SelectStmt 永非 nil，
// S 从 select 到 `}`。排查现场：若 case 体串到下节，先看 parseSelectCaseBlock 的
// case/default 停机条件。
func (p *Parser) parseSelect() ast.Stmt {
	start := p.next() // select
	p.next()          // {
	var cases []*ast.SelectCase
	var defBlock *ast.Block
	var defSpan token.Span
	seenDefault := false
	for !p.at(token.RBrace) && !p.at(token.EOF) {
		if p.at(token.Semi) || p.at(token.Comma) {
			p.next()
			continue
		}
		if p.cur().Kind == token.Ident && p.cur().Literal == "default" {
			dstart := p.next()
			if seenDefault {
				p.errorf(diag.ParseUnexpectedSyntax, dstart.Span, "duplicate 'default' in select")
			}
			seenDefault = true
			// `default then` or `default:`? Accept `then`, `:`, or newline block.
			if p.cur().Kind == token.Then {
				p.next()
			} else if p.at(token.Colon) {
				p.next()
			}
			defBlock = p.parseSelectCaseBlock()
			defSpan = token.Span{Start: dstart.Span.Start, End: p.prevEnd(), File: p.file}
			continue
		}
		if p.cur().Kind == token.Ident && p.cur().Literal == "case" {
			if seenDefault {
				p.errorf(diag.ParseUnexpectedSyntax, p.cur().Span, "'case' after 'default' in select (default must be last)")
			}
			c := p.parseSelectCase()
			if c != nil {
				cases = append(cases, c)
			}
			continue
		}
		p.errorf(diag.ParseUnexpectedSyntax, p.cur().Span, "expected 'case' or 'default' in select, got %s", p.cur().Kind)
		p.synchronizeSelect()
	}
	p.expect(token.RBrace, "'}'")
	if len(cases) == 0 && defBlock == nil {
		p.errorf(diag.ParseUnexpectedSyntax, start.Span, "select must have at least one 'case' or 'default'")
	}
	return &ast.SelectStmt{Cases: cases, Default: defBlock, DS: defSpan, S: token.Span{Start: start.Span.Start, End: p.prevEnd(), File: p.file}}
}

// synchronizeSelect 在 select 内推进到下节（`case`/`default`/`}`/分隔符），恢复节级错误。
//
// 设计者视角：它是 select 的“跳节器”——坏节后吃到 `case`/`default`/`}` 即停（不消费，
// 留给主循环分诊），`;`/`,` 则消费即停（分隔吞掉再开）。取舍：节内残骸不细判，
// 整节丢给 parseSelectCase 重报，保外层不崩。不变量：EOF/RBrace 前必停。
// 排查现场：若坏 case 后下节仍丢失，先看 Ident 的 Literal 是否为大小写变体（case 敏感）。
func (p *Parser) synchronizeSelect() {
	for {
		k := p.cur().Kind
		if k == token.EOF || k == token.RBrace {
			return
		}
		if k == token.Ident && (p.cur().Literal == "case" || p.cur().Literal == "default") {
			return
		}
		if k == token.Semi || k == token.Comma {
			p.next()
			return
		}
		p.next()
	}
}

// parseSelectCaseBlock 是节体小 parseBlock：收到下个 `case`/`default`/`}` 即停，`return case` 不吞标题。
//
// 设计者视角：parseSelectCaseBlock 是节体的“小 parseBlock”——语句循环收，`;` 作空语句，
// `return` 居末（裸 return 特判：后跟 case/default/`}` 时不把下节标题当返回值吃，
// 直立空 Return；否则走 parseReturn），`case`/`default`/`}`/EOF 即停。
// 障碍是 Lua 的块尾哨兵不含 `case`：此处另立停机条件，与 parseBlock 解耦。
// 取舍：Span 空体即零宽 cur 处，非空即首语句到 Ret 尾。不变量：返回 Block 永非 nil。
// 排查现场：若 `return case` 把 case 吞成返回值，先看裸 return 的 peek(1) 特判。
func (p *Parser) parseSelectCaseBlock() *ast.Block {
	var stmts []ast.Stmt
	var ret *ast.ReturnStmt
	for {
		if p.at(token.EOF) || p.at(token.RBrace) {
			break
		}
		if p.cur().Kind == token.Ident && (p.cur().Literal == "case" || p.cur().Literal == "default") {
			break
		}
		if p.at(token.Semi) {
			s := p.next()
			stmts = append(stmts, &ast.EmptyStmt{S: s.Span})
			continue
		}
		if p.at(token.Return) {
			// 裸 `return` 后跟 case/default/`}` 时不吃下节标题，如 `return case` 立空返回。
			n1 := p.peek(1)
			if n1.Kind == token.RBrace || n1.Kind == token.EOF ||
				(n1.Kind == token.Ident && (n1.Literal == "case" || n1.Literal == "default")) {
				start := p.next()
				ret = &ast.ReturnStmt{S: start.Span}
			} else {
				ret = p.parseReturn()
			}
			for p.at(token.Semi) {
				p.next()
			}
			break
		}
		st := p.parseStmt()
		if st == nil {
			break
		}
		stmts = append(stmts, st)
	}
	sp := token.Span{File: p.file}
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
		sp = token.Span{Start: p.cur().Span.Start, End: p.cur().Span.Start, File: p.file}
	}
	return &ast.Block{Stmts: stmts, Ret: ret, S: sp}
}

// parseSelectCase 验单节票：`case ch:recv()/ch:send(v)/timeout(s) then 块`，send 恰 1 参 recv 恰 0 参。
//
// 设计者视角：parseSelectCase 是单节的“验票+进场”——guard 三验：`case local v[,ok] = ch:recv()`
// 本地接收路（超 2 变量即记账，最多 v,ok）；`v[,ok] = ch:recv()` 赋值路经
// tryParseSelectRecvAssign 试探；余者表达式路（MethodCall 的 send/recv、sleep/timeout 单参
// Call，余者判死刑）。`then`/`:`/`do` 皆容作分隔（缺即记账），后接 parseSelectCaseBlock，
// 尾验 send 恰 1 参、recv 恰 0 参。障碍是三路前缀相交（都以 Ident 开）：顺序即优先级，
// 本地路最先（at(Local) 硬判），赋值路次之（试探），表达式兜底。取舍：guard 坏即
// synchronizeSelect 回 nil（整节丢），体坏不断链。不变量：成功 S 从 case 到体尾。
// 排查现场：若 `case ch:recv()` 的 Recv 为空，先看 MethodCall 的 Method 名是否为 recv/send。
func (p *Parser) parseSelectCase() *ast.SelectCase {
	start := p.next() // case
	sc := &ast.SelectCase{S: token.Span{Start: start.Span.Start, End: start.Span.Start, File: p.file}}
	// `case local v = ch:recv()` form.
	if p.at(token.Local) {
		lstart := p.next()
		_ = lstart
		vars := []*ast.Ident{}
		n := p.parseIdentAllowSoft()
		if n == nil {
			p.synchronizeSelect()
			return nil
		}
		vars = append(vars, n)
		for p.at(token.Comma) {
			p.next()
			n2 := p.parseIdentAllowSoft()
			if n2 == nil {
				break
			}
			vars = append(vars, n2)
			if len(vars) > 2 {
				break
			}
		}
		if len(vars) > 2 {
			p.errorf(diag.ParseUnexpectedSyntax, p.cur().Span, "select receive binds at most 2 variables (v, ok)")
			p.synchronizeSelect()
			return nil
		}
		if !p.at(token.Assign) {
			p.errorf(diag.ParseUnexpectedSyntax, p.cur().Span, "expected '=' after select receive variables")
			p.synchronizeSelect()
			return nil
		}
		p.next()
		recv := p.parseSuffix()
		mc, ok := recv.(*ast.MethodCallExpr)
		if !ok || mc.Method.Name != "recv" {
			p.errorf(diag.ParseUnexpectedSyntax, p.cur().Span, "select 'case local v = ...' expects 'ch:recv()'")
			p.synchronizeSelect()
			return nil
		}
		sc.Recv = mc
		sc.RecvVars = vars
		sc.RecvLocal = true
	} else {
		// 先试 `v[,ok] = ch:recv()` 赋值守卫。
		if save := p.tryParseSelectRecvAssign(sc); save {
			// 赋值守卫命中 sc 已填。
		} else {
			// 表达式守卫 `ch:send(v)`/`ch:recv()`/`sleep(s)` 三选一。
			g := p.parseExpr()
			if g == nil {
				p.synchronizeSelect()
				return nil
			}
			switch n := g.(type) {
			case *ast.MethodCallExpr:
				if n.Method.Name == "send" {
					sc.Send = n
				} else if n.Method.Name == "recv" {
					sc.Recv = n
				} else {
					p.errorf(diag.ParseUnexpectedSyntax, g.Span(), "select case expects 'ch:recv()', 'ch:send(v)' or 'timeout(s)', got method ':%s'", n.Method.Name)
					p.synchronizeSelect()
					return nil
				}
			case *ast.CallExpr:
				if id, ok := n.Func.(*ast.Ident); ok && (id.Name == "sleep" || id.Name == "timeout") {
					if len(n.Args) != 1 {
						p.errorf(diag.ParseUnexpectedSyntax, g.Span(), "select timeout expects exactly 1 argument (seconds)")
						p.synchronizeSelect()
						return nil
					}
					sc.Timeout = n.Args[0]
				} else {
					p.errorf(diag.ParseUnexpectedSyntax, g.Span(), "select case expects 'ch:recv()', 'ch:send(v)' or 'timeout(s)'")
					p.synchronizeSelect()
					return nil
				}
			default:
				p.errorf(diag.ParseUnexpectedSyntax, g.Span(), "select case expects 'ch:recv()', 'ch:send(v)' or 'timeout(s)'")
				p.synchronizeSelect()
				return nil
			}
		}
	}
	// 守卫体分隔 `then` 首选，`:`/`do` 亦容，如 `case ch:recv() then`。
	if p.cur().Kind == token.Then || p.at(token.Colon) || p.cur().Kind == token.Do {
		p.next()
	} else {
		p.errorf(diag.ParseUnexpectedSyntax, p.cur().Span, "expected 'then' after select case guard")
	}
	sc.Block = p.parseSelectCaseBlock()
	sc.S = token.Span{Start: start.Span.Start, End: p.prevEnd(), File: p.file}
	// 此处验 send 恰 1 参，如 `ch:send(v)`。
	if sc.Send != nil && len(sc.Send.Args) != 1 {
		p.errorf(diag.ParseUnexpectedSyntax, sc.Send.Span(), "'ch:send' expects exactly 1 argument, got %d", len(sc.Send.Args))
	}
	if sc.Recv != nil && len(sc.Recv.Args) != 0 {
		p.errorf(diag.ParseUnexpectedSyntax, sc.Recv.Span(), "'ch:recv' expects no arguments, got %d", len(sc.Recv.Args))
	}
	return sc
}

// tryParseSelectRecvAssign 侦察赋值守卫 `v[,ok] = ch:recv()`：成填 sc，败零副作用回退交表达式路。
//
// 设计者视角：tryParseSelectRecvAssign 是赋值守卫的“侦察兵”——首 Ident（软关键字亦可，
// 但 case/default/then/do/local 五词禁入）+可选 `,次Ident` + `=` + parseSuffix，
// 须为 `:recv` 方法调用才提交，否则 saveCursor+诊断双回退按“非赋值”处理。
// 障碍是 `f(a, b)` 亦以 Ident+逗号开：次 Ident 后无 '=' 即回退交表达式路，
// 避免把调用误拆成赋值。取舍：超 2 变量、非 recv、缺 '=' 一律 false（零副作用），
// 成功即填 sc.Recv/RecvVars（diags 照单收）。不变量：false 路游标诊断全复原。
// 排查现场：若 `v, ok = ch:recv()` 走成表达式路，先看首词是否撞禁词表或 peek 逗号逻辑。
func (p *Parser) tryParseSelectRecvAssign(sc *ast.SelectCase) bool {
	save := p.saveCursor()
	nd := len(p.diags)
	var vars []*ast.Ident
	if p.cur().Kind != token.Ident && !isSoftKw(p.cur().Kind) {
		return false
	}
	first := p.next()
	lit := first.Literal
	if lit == "" {
		lit = first.Kind.String()
	}
	// `case`/`default`/`then`/`do`/`local` 永不开赋值守卫。
	if lit == "case" || lit == "default" || lit == "then" || lit == "do" || lit == "local" {
		p.restoreCursor(save)
		p.diags = p.diags[:nd]
		return false
	}
	vars = append(vars, &ast.Ident{Name: lit, S: first.Span})
	if p.at(token.Comma) {
		// 须次 Ident+`=`，否则如 `f(a,b)` 回退走表达式路。
		p.next()
		if p.cur().Kind != token.Ident && !isSoftKw(p.cur().Kind) {
			p.restoreCursor(save)
			p.diags = p.diags[:nd]
			return false
		}
		second := p.next()
		lit2 := second.Literal
		if lit2 == "" {
			lit2 = second.Kind.String()
		}
		vars = append(vars, &ast.Ident{Name: lit2, S: second.Span})
	}
	if !p.at(token.Assign) {
		p.restoreCursor(save)
		p.diags = p.diags[:nd]
		return false
	}
	if len(vars) > 2 {
		p.restoreCursor(save)
		p.diags = p.diags[:nd]
		return false
	}
	p.next() // =
	recv := p.parseSuffix()
	mc, ok := recv.(*ast.MethodCallExpr)
	if !ok || mc.Method.Name != "recv" {
		p.restoreCursor(save)
		p.diags = p.diags[:nd]
		return false
	}
	// 成功即填 sc，诊断照单收。
	_ = nd
	sc.Recv = mc
	sc.RecvVars = vars
	return true
}
