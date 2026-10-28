package parser

import (
	"github.com/typelua/typelua/internal/ast"
	"github.com/typelua/typelua/internal/token"
)

// ---------- function bodies ----------

// 函数体物理布局：
//
//	parseFuncBody
//	  ├── expect '(' ──► parseParams (含 ...vararg)
//	  ├── 返回类型：'->' 多返回元组  或  ':' 单类型
//	  └── parseFuncBlock
//	        ├── '{' ... '}'  花括号体（纯语法糖，AST 与 end 体一致）
//	        └── ... 'end'    经典体
//
// 两形状产出相同 AST，检查/发射/工具只看规范形式。

// parseFuncBody 解析函数签名+体：'(' params ')' [返回类型] body，是所有函数形态的汇合点。
//
// 设计者视角：parseFuncBody 是函数装配线的“总装台”——全局 function、local function、
// 方法、匿名字面量、async 变体最终都汇到这里，统一产出 *ast.FuncLit。障碍是两种
// 返回注记并存：`-> R`（多返回元组语义）与 `: T`（单类型、全类型语法）；前者走
// parseReturnType（`-> ()` 即 void、`-> (A,B)` 即多返回），后者走 parseType。
// 取舍：`:` 用全类型解析让 `: () -> T` 仍是函数类型，而 `->` 保留 Lua 多返回直觉。
// 不变量：入口 cur 必为 '('（由 expect 兜底），出口 S 覆盖签名到体尾。
// 排查现场：若返回类型吞了函数体，先看 ':' 与 '->' 分支是否把 body 的 '{' 误当类型吃掉。
func (p *Parser) parseFuncBody(isMethod bool) *ast.FuncLit {
	start := p.cur()
	if _, ok := p.expect(token.LParen, "'('"); !ok {
		return &ast.FuncLit{Body: &ast.Block{}, S: start.Span}
	}
	params, hasVA, vaName, vaTy := p.parseParams()
	p.expect(token.RParen, "')'")
	var retTy ast.TypeNode
	if p.at(token.Arrow) {
		p.next()
		retTy = p.parseReturnType()
	} else if p.at(token.Colon) {
		// `:` returns use full type parsing so `: () -> T` is a func type,
		// while `-> (A, B)` keeps multi-return tuple semantics.
		p.next()
		retTy = p.parseType()
	}
	body := p.parseFuncBlock()
	return &ast.FuncLit{Params: params, HasVarArg: hasVA, VarArgName: vaName, VarArgType: vaTy, ReturnType: retTy, Body: body, IsMethod: isMethod, S: token.Span{Start: start.Span.Start, End: p.prevEnd(), File: p.file}}
}

// parseFuncBlock 是函数体双开门：`{…}` 糖门与 `…end` 木门通同一客厅如 `{…}`/`…end`，排查看 Span。
// 同一客厅详见实现，排查看 Span。
// 糖体错了同步到 `}`，经典体错了同步到 `end`；本函数把分叉收敛在入口，parseBlock
// 复用同一套循环，synchronize 的 RBrace/End 双哨兵恰好各保一门。取舍：AST 不留
// 痕（无 IsBrace 标记），换工具链零分支；代价是格式化回写无法还原原门。
// 不变量：两路都返回非 nil Block。排查现场：若 brace 体漏报缺 `}`，先看 expect(RBrace)
// 是否被误写成 expect(End)。
func (p *Parser) parseFuncBlock() *ast.Block {
	if p.at(token.LBrace) {
		p.next() // {
		body := p.parseBlock()
		p.expect(token.RBrace, "'}'")
		return body
	}
	body := p.parseBlock()
	p.expect(token.End, "'end'")
	return body
}

// parseParams 解析形参列 `name[: T], ..., ...name[: T]`，返回 (params, hasVA, vaName, vaTy)。
//
// 设计者视角：parseParams 是签名台的“点名册”——逐个收 `名[:类型]`，`...` 一出现即转
// 变参（`...: T` / `...name: T`），随后 break（变参必居末）。障碍是软关键字可作参名
// （`type`、`self` 们）：parseIdentAllowSoft 照单收，合法性后置。候选曾是“变参后还允
// 续参”，但 Lua 语义变参恒居末，解析期即截断可早报错。不变量：RParen 不消费，留给
// 调用方 expect；变参只可能出现一次。排查现场：若 `f(a, ...: T)` 的 vaTy 为空，
// 先看 Colon 分支是否在 break 前正确解析。
func (p *Parser) parseParams() ([]*ast.Param, bool, *ast.Ident, ast.TypeNode) {
	var params []*ast.Param
	var vaName *ast.Ident
	var vaTy ast.TypeNode
	hasVA := false
	for !p.at(token.RParen) && !p.at(token.EOF) {
		if p.at(token.DotDotDot) {
			p.next()
			hasVA = true
			if p.at(token.Ident) || isSoftKw(p.cur().Kind) {
				t := p.next()
				lit := t.Literal
				if lit == "" {
					lit = t.Kind.String()
				}
				vaName = &ast.Ident{Name: lit, S: t.Span}
			}
			if p.at(token.Colon) {
				p.next()
				vaTy = p.parseType()
			}
			break
		}
		n := p.parseIdentAllowSoft()
		if n == nil {
			break
		}
		var ty ast.TypeNode
		if p.at(token.Colon) {
			p.next()
			ty = p.parseType()
		}
		params = append(params, &ast.Param{Name: n, Type: ty, S: token.Span{Start: n.Span().Start, End: p.prevEnd(), File: p.file}})
		if !p.at(token.Comma) {
			break
		}
		p.next()
		// 尾逗号后 `, …` 亦容，如 `f(a, …)`。
		if p.at(token.DotDotDot) {
			continue
		}
	}
	return params, hasVA, vaName, vaTy
}

// parseReturnType 解析 `->` 后的返回类型：`()` 即 void，`(A,B)` 即多返回 Tuple，单 T 即单返回。
//
// 设计者视角：它是 `->` 的“翻译官”——把 Lua 多返回直觉译成 AST 方言：空括号译作
// NamedType(void)（零返回的显式说法），多元素译作 TupleType（checker 按多值展开），
// 单元素译作 ParenType（括号语义，防与 Tuple 混淆）。障碍是 `(A)` 既像括号又像单元组；
// 取舍是 len==1 即 Paren，让 `(A) ->` 与 `A ->` 同义。不变量：永不返回 nil（空位由
// parseType 兜 any）。排查现场：若多返回被当单元组，先看此处 len 分支，再看
// returnTypeToList 是否二次拆包。
func (p *Parser) parseReturnType() ast.TypeNode {
	// `-> ()` => void; `-> (A, B)` => multi Tuple; `-> T` => single.
	if p.at(token.LParen) {
		start := p.next()
		if p.at(token.RParen) {
			e := p.next()
			return &ast.NamedType{Name: "void", S: token.Span{Start: start.Span.Start, End: e.Span.End, File: p.file}}
		}
		var elems []ast.TypeNode
		for {
			elems = append(elems, p.parseType())
			if !p.at(token.Comma) {
				break
			}
			p.next()
		}
		p.expect(token.RParen, "')'")
		if len(elems) == 1 {
			return &ast.ParenType{Elem: elems[0], S: token.Span{Start: start.Span.Start, End: p.prevEnd(), File: p.file}}
		}
		return &ast.TupleType{Elems: elems, S: token.Span{Start: start.Span.Start, End: p.prevEnd(), File: p.file}}
	}
	return p.parseType()
}
