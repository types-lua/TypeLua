package parser

import (
	"github.com/typelua/typelua/internal/ast"
	"github.com/typelua/typelua/internal/diag"
	"github.com/typelua/typelua/internal/token"
)

// ---------- types ----------

// 类型语法物理布局（优先级从低到高）：
//
//	parseType ──► parseConditional  (Check extends Extends ? True : False)
//	              └── parseUnion   ( | )
//	                    └── parseIntersection  ( & )
//	                          └── parseTypePostfix  (?  []  [K])
//	                                └── parsePrimaryType
//	                                      ├── 括号/元组/函数类型
//	                                      ├── [ ] 元组  { } 表类型/映射类型
//	                                      ├── 字面量/nil/...T/rest
//	                                      ├── keyof/typeof/infer
//	                                      └── 命名类型 (含泛型 <...> 与 . 限定)
//
// 条件类型 `?` 与 Optional `?` 冲突：isConditionalQ 试探（保存 pos+诊断+depth），
// 快速路径按后续 Token 是否能开类型决定，把常见 `A?|B?` 从 O(n^2) 降到 O(n)。

// parseType 解析完整类型（条件类型入口，覆盖 Union/Intersection/Postfix 全层）。
//
// 设计者视角：parseType 是类型世界的“正门”——凡要一个类型（注记、别名、泛型实参、
// 返回）都走此门，门内即 parseConditional。取舍是薄转发，让调用栈一眼分出“要类型”
// 与“要某层类型”。不变量：与 parseConditional 等价。排查现场：若某处类型优先级异常，
// 先确认调的是 parseType 还是 parseUnion 等内部层。
func (p *Parser) parseType() ast.TypeNode { return p.parseConditional() }

// parseConditional 是条件三段论：`A extends B ? C : D` 双支右结合，`?` 与 Optional 同形靠 isConditionalQ 裁决。
// 无 extends 即回（非条件）；有则 extendsDepth++ 后收 Extends（供 infer 判定作用域），
// 再 expect('?')、右结合收 True/False 双支（TS 同款）。障碍是 `?` 与 Optional 后缀
// 同形：`T? : U` 里 `?` 的归属全靠 isConditionalQ 试探，后缀层见条件问号即让路。
// 取舍：任一分支缺席即回 check（半截条件当联合类型），不硬凑 ConditionalType。
// 不变量：成功 S 从 Check 起到 False 尾。排查现场：若 `A extends B ? C : D` 的 True
// 被吞成 Optional，先看 parseTypePostfix 的 isConditionalQ 让路分支。
func (p *Parser) parseConditional() ast.TypeNode {
	check := p.parseUnion()
	if check == nil || !p.at(token.ExtendsKw) {
		return check
	}
	p.next() // extends
	p.extendsDepth++
	ext := p.parseUnion()
	p.extendsDepth--
	if ext == nil {
		return check
	}
	if _, ok := p.expect(token.Question, "'?'"); !ok {
		return check
	}
	trueBranch := p.parseConditional()
	if trueBranch == nil {
		return check
	}
	if _, ok := p.expect(token.Colon, "':'"); !ok {
		return check
	}
	falseBranch := p.parseConditional()
	if falseBranch == nil {
		return check
	}
	return &ast.ConditionalType{Check: check, Extends: ext, True: trueBranch, False: falseBranch, S: token.Span{Start: check.Span().Start, End: p.prevEnd(), File: p.file}}
}

// isConditionalQ 是问号验钞机：试吃 `? True` 后有 `:` 即条件岔口，否则是 `T?` 可选尾，零副作用回退。
// 也可能是条件的岔口，只能试探（吃 `?` 后 parseType，看后是否跟 ':'）。
// 障碍是朴素试探为 O(n^2)（`A?|B?|C?` 每问号都全量试）：快速路径先看 peek(1)
// 能否开类型（Ident/`(`/`[`/`{`/字面量/keyof/typeof/infer/`...` 才值得试，
// `|`/`,`/`)`/`]`/EOF 直接判 Optional），把常见链降到 O(n)。取舍：试探内深度熔断
// 即判“非条件”（回浅层 Optional 路），诊断与游标双回退零副作用。
// 不变量：纯只读（游标/诊断/depth 三恢复）。排查现场：若 `T ? A : B` 被判 Optional，
// 先看 peek(1) 是否在可开类型白名单。
func (p *Parser) isConditionalQ() (ok bool) {
	if !p.at(token.Question) {
		return false
	}
	// 快道：`?` 后 `|`/`,`/`)`/`]`/EOF 开不了类型即 Optional，如 `A?|B?` 跳过全量试探把 O(n²) 降到 O(n)。
	switch p.peek(1).Kind {
	case token.Ident, token.LParen, token.LBrack, token.LBrace,
		token.String, token.Number, token.True, token.False, token.Nil,
		token.DotDotDot, token.KeyofKw, token.TypeofKw, token.InferKw:
		// 能开类型才进试探。
	default:
		return false
	}
	saveCur, saveDiags := p.saveCursor(), len(p.diags)
	saveDepth := p.depth
	defer func() {
		if r := recover(); r != nil {
			if _, isBail := r.(depthBailout); !isBail {
				panic(r)
			}
			ok = false
		}
		p.restoreCursor(saveCur)
		p.diags = p.diags[:saveDiags]
		p.depth = saveDepth
	}()
	p.next() // ?
	branch := p.parseType()
	ok = branch != nil && p.at(token.Colon)
	return ok
}

// parseUnion 解析 `A | B | C` 联合类型（`|` 左结合串珠）。
//
// 设计者视角：parseUnion 是联合的“串珠机”——首珠 parseIntersection，无 '|' 即单返，
// 有则循环吃 `|` 收珠，包成 UnionType。障碍是 `|` 与位或/逻辑或的 Token 复用？
// 此处 Pipe 专属类型语境，表达式层的 Or 另属其位，天然隔离。取舍：空分支（`A | ` 缺尾）
// 仍 append nil 后续由 checker 兜，让 Span 不中断。不变量：单元素不包 Union（原样回）。
// 排查现场：若 `A?|B?` 的问号被吞，先看 parseTypePostfix 的条件让路而非此处。
func (p *Parser) parseUnion() ast.TypeNode {
	first := p.parseIntersection()
	if first == nil {
		return nil
	}
	if !p.at(token.Pipe) {
		return first
	}
	types := []ast.TypeNode{first}
	for p.at(token.Pipe) {
		p.next()
		types = append(types, p.parseIntersection())
	}
	return &ast.UnionType{Types: types, S: token.Span{Start: first.Span().Start, End: p.prevEnd(), File: p.file}}
}

// parseIntersection 解析 `A & B` 交叉类型（`&` 左结合串珠，层级高于 Union）。
//
// 设计者视角：parseIntersection 是交叉的“合股机”——parseTypePostfix 首股，无 '&'
// 即单返，有则循环合股包 IntersectionType。层级取舍：交叉高于联合（`A | B & C`
// 即 `A | (B&C)`，与 TS 一致），后缀高于交叉（`A[] & B` 即 `(A[])&B`）。
// 不变量：单元素不包节点。排查现场：若 `A & B | C` 分组反了，先看 parseUnion 与
// 此处的调用嵌套（Union 在上、Intersection 在下）。
func (p *Parser) parseIntersection() ast.TypeNode {
	first := p.parseTypePostfix()
	if first == nil {
		return nil
	}
	if !p.at(token.Amp) {
		return first
	}
	types := []ast.TypeNode{first}
	for p.at(token.Amp) {
		p.next()
		types = append(types, p.parseTypePostfix())
	}
	return &ast.IntersectionType{Types: types, S: token.Span{Start: first.Span().Start, End: p.prevEnd(), File: p.file}}
}

// parseTypePostfix 解析类型后缀：`?` 可选（条件问号让路）、`[]` 数组、`[K]` 索引访问。
//
// 设计者视角：parseTypePostfix 是类型的“串尾机”——parsePrimaryType 造头，for 循环
// 逐个串后缀：`?` 先经 isConditionalQ 验（条件岔口即 break 让外层收），否则包 Optional；
// `[]` 空括号包 Array；`[K]` 内 parseType 包 IndexedAccess。障碍是三后缀可叠加
// （`T?[]`、`T[K]?`）：循环即支持，不设上限。取舍：`[K]` 内 idx 缺席即 break（半截索引
// 不硬包），让外层按缺类型恢复。不变量：返回永非 nil（头 nil 即回 nil）。
// 排查现场：若 `T? : U` 的问号被吞成 Optional，先看 isConditionalQ 的快速路径白名单。
func (p *Parser) parseTypePostfix() ast.TypeNode {
	t := p.parsePrimaryType()
	if t == nil {
		return nil
	}
	for {
		if p.at(token.Question) {
			// 条件岔口 `? A:` 让外层收，此处不包 Optional。
			if p.isConditionalQ() {
				break
			}
			q := p.next()
			t = &ast.OptionalType{Elem: t, S: token.Span{Start: t.Span().Start, End: q.Span.End, File: p.file}}
			continue
		}
		if p.at(token.LBrack) && p.peek(1).Kind == token.RBrack {
			p.next()
			p.next()
			t = &ast.ArrayType{Elem: t, S: token.Span{Start: t.Span().Start, End: p.prevEnd(), File: p.file}}
			continue
		}
		if p.at(token.LBrack) {
			// 索引访问 `T[K]`，空 `[]` 已在上支判数组。
			p.next() // [
			idx := p.parseType()
			p.expect(token.RBrack, "']'")
			if idx == nil {
				break
			}
			t = &ast.IndexedAccessType{Obj: t, Index: idx, S: token.Span{Start: t.Span().Start, End: p.prevEnd(), File: p.file}}
			continue
		}
		break
	}
	return t
}

// parsePrimaryType 解析原子类型：括号/函数、元组 `[...]`、表 `{...}`、字面量、keyof/typeof/infer、命名类型。
//
// 设计者视角：parsePrimaryType 是类型的“地基”——全类型塔站在这块板上，入口即
// enterDepth（元组/函数嵌套深，熔断在此设卡）。分诊：`(` 走 parseParenOrFuncType
// （函数与括号试探分流），`[` 走元组，`{` 走 parseTableType（含 Mapped 分流），
// 字面量/nil/`...T`/keyof/typeof/infer 各归其位，Ident 走 parseNamedType。
// 障碍是未知 Kind（如表达式误入类型位）：取舍是报缺类型+吃一 Token+合成 any，
// 让后文继续（空位永不回 nil，除 infer 名缺席回 any）。不变量：`...T` 内吃全 Postfix
// 让 `...unknown[]` 即 rest-of-unknown。排查现场：若深类型误报 TL2015，先看
// enterDepth/defer 配对，再看调用栈是否经 parseParenOrFuncType 重复计数。
func (p *Parser) parsePrimaryType() ast.TypeNode {
	c := p.cur()
	p.enterDepth(c.Span)
	defer p.leaveDepth()
	switch c.Kind {
	case token.LParen:
		return p.parseParenOrFuncType()
	case token.LBrack:
		start := p.next()
		var elems []ast.TypeNode
		if !p.at(token.RBrack) {
			for {
				elems = append(elems, p.parseType())
				if !p.at(token.Comma) {
					break
				}
				p.next()
			}
		}
		p.expect(token.RBrack, "']'")
		return &ast.TupleType{Elems: elems, S: token.Span{Start: start.Span.Start, End: p.prevEnd(), File: p.file}}
	case token.LBrace:
		return p.parseTableType()
	case token.String, token.Number, token.True, token.False:
		p.next()
		return &ast.LiteralType{Value: c.Literal, S: c.Span}
	case token.Nil:
		p.next()
		return &ast.NamedType{Name: "nil", S: c.Span}
	case token.DotDotDot:
		// `...T` 余参标记，吃全后缀故 `...unknown[]` 即余下 unknown 数组。
		p.next()
		elem := p.parseTypePostfix()
		return &ast.NamedType{Name: "...", Args: []ast.TypeNode{elem}, S: token.Span{Start: c.Span.Start, End: p.prevEnd(), File: p.file}}
	case token.KeyofKw:
		start := p.next()
		elem := p.parsePrimaryType()
		return &ast.KeyofType{Elem: elem, S: token.Span{Start: start.Span.Start, End: p.prevEnd(), File: p.file}}
	case token.TypeofKw:
		start := p.next()
		p.expect(token.LParen, "'('")
		e := p.parseExpr()
		p.expect(token.RParen, "')'")
		return &ast.TypeofType{Expr: e, S: token.Span{Start: start.Span.Start, End: p.prevEnd(), File: p.file}}
	case token.InferKw:
		start := p.next()
		name := p.parseIdent()
		if name == nil {
			return &ast.NamedType{Name: "any", S: start.Span}
		}
		return &ast.InferType{Name: name, S: token.Span{Start: start.Span.Start, End: p.prevEnd(), File: p.file}}
	case token.Ident:
		return p.parseNamedType()
	default:
		p.errorf(diag.ParseExpectedType, c.Span, "expected type, got %s", c.Kind)
		// 合成 any 占位续解，如 `local x: ???` 后文不崩。
		p.next()
		return &ast.NamedType{Name: "any", S: c.Span}
	}
}

// parseNamedType 解析命名类型 `a.b.C<T,...>`：点链拼名，`<...>` 泛型实参加 consumeGt 收尾。
//
// 设计者视角：parseNamedType 是类型名的“门牌+户型”——点链（`a.b.C`）拼全限定名，
// `<` 后逗号列收实参（空 `<>` 即零参），consumeGt 逐个消右括号（含 `>>` 拆分）。
// 障碍是 `F<A>=v` 的 Ge 粘连：consumeGt 把 `>=` 拆成 '>'+'='，泛型收尾后 '=' 留给赋值。
// 取舍：实参内 parseType 全量（含条件），贵但正确；空位（atGt/EOF）即零参跳过。
// 不变量：S 从首名到 prevEnd。排查现场：若 `F<A, B>` 只收一半，先看逗号循环的 break，
// 再看 atGt/consumeGt 的 pending 余量。
func (p *Parser) parseNamedType() ast.TypeNode {
	first := p.next()
	name := first.Literal
	// 限定名 `a.b.C` 点链拼接。
	for p.at(token.Dot) && p.peek(1).Kind == token.Ident {
		p.next()
		n := p.next()
		name += "." + n.Literal
	}
	var args []ast.TypeNode
	if p.at(token.Lt) {
		p.next() // <
		if !p.atGt() && p.cur().Kind != token.EOF {
			for {
				args = append(args, p.parseType())
				if p.at(token.Comma) {
					p.next()
					continue
				}
				break
			}
		}
		p.consumeGt()
	}
	return &ast.NamedType{Name: name, Args: args, S: token.Span{Start: first.Span.Start, End: p.prevEnd(), File: p.file}}
}

// parseParenOrFuncType 解析 `(` 开头的括号/元组/函数类型三义：`()` 空元、`(...)` 试探函数、无箭头回落普通。
//
// 设计者视角：它是括号的“三岔试探”——`()` 空即 void（有 `->` 即无参函数）；
// 非空先 tryParseFuncTypeParams 试探具名参数（`name: T`/`name?: T`/`...`），
// 有 `)`+`->` 即函数类型；无箭头且全匿名即复用（单 Paren/多 Tuple，零重解析）；
// 否则回退重走普通类型列（逗号列+可选 `->` 无名函数）。障碍是 `(A, B)` 既像参数列
// 又像元组：取舍是“有箭头即函数、无箭头全匿名即元组、余者回退”，试探期诊断截断零副作用。
//
// 分流示意：
//
//	( ── () ──[-> R]──► FuncType(void)
//	  ├── tryParams ── ) ── -> R ──► FuncType(具名)
//	  ├── 全匿名无箭头 ──► Paren/Tuple（复用）
//	  └── 回退 ── type (, type)* ──[-> R]──► Paren/Tuple/FuncType(无名)
func (p *Parser) parseParenOrFuncType() ast.TypeNode {
	start := p.next() // (
	// 空 `()` 即 void 单元，有 `-> R` 即无参函数。
	if p.at(token.RParen) {
		p.next()
		if p.at(token.Arrow) {
			p.next()
			ret := p.parseReturnType()
			return &ast.FuncType{Returns: returnTypeToList(ret), S: token.Span{Start: start.Span.Start, End: p.prevEnd(), File: p.file}}
		}
		return &ast.ParenType{Elem: &ast.NamedType{Name: "void", S: start.Span}, S: token.Span{Start: start.Span.Start, End: p.prevEnd(), File: p.file}}
	}
	// 先按具名参数试探，如 `(x: T)`。
	save := p.saveCursor()
	ndiags := len(p.diags)
	params, hasVA, vaTy, ok := p.tryParseFuncTypeParams()
	if ok && p.at(token.RParen) {
		p.next()
		if p.at(token.Arrow) {
			p.next()
			ret := p.parseReturnType()
			return &ast.FuncType{Params: params, HasVarArg: hasVA, VarArgType: vaTy, Returns: returnTypeToList(ret), S: token.Span{Start: start.Span.Start, End: p.prevEnd(), File: p.file}}
		}
		// 非函数即普通类型，全匿名可复用已收珠。
		allUnnamed := !hasVA
		var elems []ast.TypeNode
		for _, pr := range params {
			if pr.Name != nil || pr.Optional {
				allUnnamed = false
				break
			}
			elems = append(elems, pr.Type)
		}
		if allUnnamed {
			if len(elems) == 1 {
				return &ast.ParenType{Elem: elems[0], S: token.Span{Start: start.Span.Start, End: p.prevEnd(), File: p.file}}
			}
			return &ast.TupleType{Elems: elems, S: token.Span{Start: start.Span.Start, End: p.prevEnd(), File: p.file}}
		}
	}
	// 回退按普通类型列重走，如 `(A, B)`。
	p.restoreCursor(save)
	p.diags = p.diags[:ndiags]
	var elems []ast.TypeNode
	for {
		elems = append(elems, p.parseType())
		if !p.at(token.Comma) {
			break
		}
		p.next()
	}
	p.expect(token.RParen, "')'")
	if p.at(token.Arrow) {
		// 无名 `(A, B) -> R` 即多参函数。
		p.next()
		ret := p.parseReturnType()
		var params []*ast.FuncTypeParam
		for _, e := range elems {
			params = append(params, &ast.FuncTypeParam{Type: e, S: e.Span()})
		}
		return &ast.FuncType{Params: params, Returns: returnTypeToList(ret), S: token.Span{Start: start.Span.Start, End: p.prevEnd(), File: p.file}}
	}
	if len(elems) == 1 {
		return &ast.ParenType{Elem: elems[0], S: token.Span{Start: start.Span.Start, End: p.prevEnd(), File: p.file}}
	}
	return &ast.TupleType{Elems: elems, S: token.Span{Start: start.Span.Start, End: p.prevEnd(), File: p.file}}
}

// tryParseFuncTypeParams 试探函数类型参数列（`name[?]: T` / 裸 T / `...` 变参），供括号分流预判。
//
// 设计者视角：它是 FuncType 的“侦察兵”——在 `(` 后裸奔一遍参数（具名看 Colon/Question
// 双前瞻，`...T` 与 `...rest: T` 看次 Token 分形），成败只回 ok，不报诊断（调用方回退
// 即清）。障碍是 `...T`（匿名变参）与 `...rest`（具名）的同前缀：peek(1) 为 Colon/Comma/
// RParen 即具名，否则按类型吃。取舍：遇错（如缺 ':') 即回 false，让外层回落普通类型路，
// 而非在此 synchronize。不变量：返回的 S 由 tySpan（名起类型止）拼接。
// 排查现场：若 `(x: T) -> R` 试探失败，先看 Question/Colon 前瞻，再看 expect(':') 的 ok。
func (p *Parser) tryParseFuncTypeParams() ([]*ast.FuncTypeParam, bool, ast.TypeNode, bool) {
	var params []*ast.FuncTypeParam
	hasVA := false
	var vaTy ast.TypeNode
	for !p.at(token.RParen) && !p.at(token.EOF) {
		if p.at(token.DotDotDot) {
			p.next()
			hasVA = true
			// `...名: T` 具名或 `...T` 匿名。
			if p.at(token.Ident) || isSoftKw(p.cur().Kind) {
				// 次 Token 是 `:`/`,`/`)` 即具名 `...rest`，否则按类型吃 `...T`。
				n := p.cur()
				if p.peek(1).Kind == token.Colon || p.peek(1).Kind == token.Comma || p.peek(1).Kind == token.RParen {
					p.next()
					lit := n.Literal
					if lit == "" {
						lit = n.Kind.String()
					}
					name := &ast.Ident{Name: lit, S: n.Span}
					var ty ast.TypeNode
					if p.at(token.Colon) {
						p.next()
						ty = p.parseType()
					}
					params = append(params, &ast.FuncTypeParam{Name: name, Type: ty, IsVarArg: true, S: tySpan(name, ty)})
					_ = vaTy
				} else {
					ty := p.parseType()
					vaTy = ty
					params = append(params, &ast.FuncTypeParam{Type: ty, IsVarArg: true, S: ty.Span()})
				}
			} else {
				ty := p.parseType()
				vaTy = ty
				params = append(params, &ast.FuncTypeParam{Type: ty, IsVarArg: true, S: ty.Span()})
			}
			break
		}
		// `名: T`/`名?: T` 具名，裸 `T` 无名。
		if p.at(token.Ident) || isSoftKw(p.cur().Kind) {
			// 后跟 `:`/`?` 即具名，如 `(x: T)`。
			isNamed := false
			if p.peek(1).Kind == token.Colon || p.peek(1).Kind == token.Question {
				isNamed = true
			}
			if isNamed {
				n := p.next()
				name := &ast.Ident{Name: n.Literal, S: n.Span}
				optional := false
				if p.at(token.Question) {
					p.next()
					optional = true
				}
				if _, ok := p.expect(token.Colon, "':'"); !ok {
					return nil, false, nil, false
				}
				ty := p.parseType()
				params = append(params, &ast.FuncTypeParam{Name: name, Type: ty, Optional: optional, S: tySpan(name, ty)})
			} else {
				// 无名位：裸 Ident 按类型吃，如 `(string, number)`。
				ty := p.parseType()
				params = append(params, &ast.FuncTypeParam{Type: ty, S: ty.Span()})
			}
		} else {
			ty := p.parseType()
			if ty == nil {
				return nil, false, nil, false
			}
			params = append(params, &ast.FuncTypeParam{Type: ty, S: ty.Span()})
		}
		if !p.at(token.Comma) {
			break
		}
		p.next()
	}
	return params, hasVA, vaTy, true
}

// tySpan 拼接函数类型参数的 Span：名起类型止，无类型即名本身。
//
// 设计者视角：tySpan 是参数的“量尺”——`name: T` 圈全段，裸 `...rest`（ty 为 nil）
// 圈名即够，让 LSP 悬停/重命名有址可点。取舍是纯函数零解析，File 取名之 File。
// 不变量：ty 非空即以 ty.End 收边。排查现场：若参数高亮缺类型段，先看 ty 是否为 nil。
func tySpan(name *ast.Ident, ty ast.TypeNode) token.Span {
	if ty == nil {
		return name.Span()
	}
	return token.Span{Start: name.Span().Start, End: ty.Span().End, File: name.Span().File}
}

// parseFuncTypeParams 解析函数类型参数列（tryParseFuncTypeParams 的提交版，错不停工）。
//
// 设计者视角：它是侦察兵的“转正版”——接口方法糖/表方法糖这些“必为参数列”的位置
// 直接提交，不做回退（错即留痕，调用方 expect 收尾）。取舍是薄转发（ok 丢弃），
// 让三处调用（接口字段/表字段/括号回落）共用一套具名/变参语义，永不分叉。
// 不变量：与 tryParse 成功路等价。排查现场：若方法参数错后游标飞，先查调用方的
// expect(RParen) 而非此处。
func (p *Parser) parseFuncTypeParams() ([]*ast.FuncTypeParam, bool, ast.TypeNode) {
	params, hasVA, vaTy, _ := p.tryParseFuncTypeParams()
	return params, hasVA, vaTy
}

// parseTableType 解析表类型 `{ ... }`：Mapped 前瞻命中即转映射类型，余者字段循环。
//
// 设计者视角：parseTableType 是表类型的“分拣大厅”——isMappedStart（`[K in` 嗅探，
// 含 +/`-` readonly 前缀）命中即移交 parseMappedType 并提交（后错即真错，不回退）；
// 否则 parseTableTypeField 循环收字段，坏字段 synchronizeTableType 跳一格。
// 障碍是 `{ [string]: T }` 索引器与 `{ [K in Keys]: V }` 映射前缀相同：取舍是
// “见 `in` 即映射”的提交式判定，误判成本由 Mapped 内的真报错承担。
// 不变量：返回 TableType/MappedType 必居其一。排查现场：若映射被当索引器，
// 先看 isMappedStart 的 peek(i+2)==In 是否生效。
func (p *Parser) parseTableType() ast.TypeNode {
	start := p.next() // {
	// 映射 `{[K in Keys]?: V}` 见 `[Ident in` 即提交，后错为真错。
	if p.isMappedStart() {
		return p.parseMappedType(start)
	}
	var fields []*ast.TableTypeField
	for !p.at(token.RBrace) && !p.at(token.EOF) {
		if p.at(token.Comma) || p.at(token.Semi) {
			p.next()
			continue
		}
		f := p.parseTableTypeField()
		if f == nil {
			p.synchronizeTableType()
			continue
		}
		fields = append(fields, f)
		if p.at(token.Comma) || p.at(token.Semi) {
			p.next()
		}
	}
	p.expect(token.RBrace, "'}'")
	return &ast.TableType{Fields: fields, S: token.Span{Start: start.Span.Start, End: p.prevEnd(), File: p.file}}
}

// isMappedStart 是映射试纸：`{` 后 `[K in` 三连（可带 +/-readonly）即映射，`[string]:` 索引器靠 `in` 一词区分。
// （前可带 +/`-` readonly）即映射，否则索引器/普通字段。纯前瞻零消费，
// 命中后调用方提交（不回退）。障碍是索引器 `[string]:` 亦以 `[` 开：`in` 一词
// 即分水岭。排查现场：若 `{ [K in V]: T }` 未进映射，先打印 peek(0..2) 的 Kind。
func (p *Parser) isMappedStart() bool {
	i := 0
	if p.peek(i).Kind == token.Plus || p.peek(i).Kind == token.Minus {
		i++
	}
	if p.peek(i).Kind == token.ReadonlyKw {
		i++
	}
	return p.peek(i).Kind == token.LBrack &&
		p.peek(i+1).Kind == token.Ident &&
		p.peek(i+2).Kind == token.In
}

// parseMappedType 解析映射类型 `{ [K in Keys][?/-?][as ...]: V }`（含 +/- readonly/? 修饰）。
//
// 设计者视角：parseMappedType 是映射的“精密装配”——`+readonly`/`-readonly`/裸 readonly
// 三档先收，`[K in Constraint]` 收参束，`+?`/`-?`/裸 `?` 收可选档（此处 `?` 恒为可选标记，
// 因条件 `?` 需 `extends` 在前，天然无歧义），`:` 后收值类型，`}` 封口。
// 障碍是半截映射（如缺 `]`/缺 `:`）：取舍是即时回 any（NamedType(any) 占位），
// 让外层不连锁崩。不变量：param/constraint/value 任一缺席即回 any。
// 排查现场：若 `-readonly` 的 RoMinus 未立，先看 Plus/Minus 分支的 neg 判定。
func (p *Parser) parseMappedType(start token.Token) ast.TypeNode {
	readonly, roMinus := false, false
	if p.at(token.Plus) || p.at(token.Minus) {
		neg := p.at(token.Minus)
		p.next()
		if _, ok := p.expect(token.ReadonlyKw, "'readonly'"); !ok {
			return &ast.NamedType{Name: "any", S: start.Span}
		}
		if neg {
			roMinus = true
		} else {
			readonly = true
		}
	} else if p.at(token.ReadonlyKw) {
		p.next()
		readonly = true
	}
	p.next() // [
	param := p.parseIdent()
	p.expect(token.In, "'in'")
	constraint := p.parseType()
	if _, ok := p.expect(token.RBrack, "']'"); !ok {
		return &ast.NamedType{Name: "any", S: start.Span}
	}
	optional, optMinus := false, false
	if p.at(token.Plus) || p.at(token.Minus) {
		neg := p.at(token.Minus)
		p.next()
		if _, ok := p.expect(token.Question, "'?'"); !ok {
			return &ast.NamedType{Name: "any", S: start.Span}
		}
		if neg {
			optMinus = true
		} else {
			optional = true
		}
	} else if p.at(token.Question) {
		// `?` here is the optional marker, never a conditional branch:
		// a `[K in T] ? ...` conditional would need `extends` first.
		p.next()
		optional = true
	}
	p.expect(token.Colon, "':'")
	value := p.parseType()
	p.expect(token.RBrace, "'}'")
	if param == nil || constraint == nil || value == nil {
		return &ast.NamedType{Name: "any", S: start.Span}
	}
	return &ast.MappedType{Param: param, Constraint: constraint, Optional: optional, OptMinus: optMinus, Readonly: readonly, RoMinus: roMinus, Value: value, S: token.Span{Start: start.Span.Start, End: p.prevEnd(), File: p.file}}
}

// parseTableTypeField 解析单个表类型字段：readonly 属性、索引器 `[K]: V`、方法糖、普通 `name[?]: T` 四形态。
//
// 设计者视角：它是表类型的“四道柜台”——readonly 章先收（后 `?` 可选）；`[` 即索引器
// （K/V 双 parseType）；`Ident (`/`<` 即方法糖（泛型试探+parseFuncTypeParams+`:`/`->`
// 返回，包 FuncType，IsMethod 立旗）；余者普通属性（`?` 可选+expect(':')+类型）。
// 障碍是两处方法糖（`m<T>(...)` 与 `m(...)`）形态相近：取舍是双分支收敛到同一 FuncType，
// 让 checker 只认 MethodSig。不变量：失败回 nil（调用方同步），成功 S 从首 Token 到类型尾。
// 排查现场：若 `m(x: T): R` 的 Returns 为空，先看 Colon/Arrow 分支的 returnTypeToList。
func (p *Parser) parseTableTypeField() *ast.TableTypeField {
	start := p.cur()
	if p.at(token.ReadonlyKw) {
		p.next()
		name := p.parseIdentAllowSoft()
		if name == nil {
			return nil
		}
		optional := false
		if p.at(token.Question) {
			p.next()
			optional = true
		}
		p.expect(token.Colon, "':'")
		ty := p.parseType()
		return &ast.TableTypeField{Name: name.Name, NameSpan: name.Span(), Type: ty, Optional: optional, Readonly: true, S: token.Span{Start: start.Span.Start, End: p.prevEnd(), File: p.file}}
	}
	// 索引器 `[K]: V`，如 `{[string]: number}`。
	if p.at(token.LBrack) {
		p.next()
		k := p.parseType()
		p.expect(token.RBrack, "']'")
		p.expect(token.Colon, "':'")
		v := p.parseType()
		return &ast.TableTypeField{IsIndexer: true, IndexKey: k, IndexVal: v, S: token.Span{Start: start.Span.Start, End: p.prevEnd(), File: p.file}}
	}
	// 方法糖 `m(x): R` 或属性 `name: T`。
	if (p.at(token.Ident) || isSoftKw(p.cur().Kind)) && (p.peek(1).Kind == token.LParen || p.peek(1).Kind == token.Lt) {
		name := p.next()
		lit := name.Literal
		if lit == "" {
			lit = name.Kind.String()
		}
		// 方法泛型 `m<T>(x)` 留给显式实参，无 `(` 即回退。
		var mgen []*ast.GenericParam
		if p.at(token.Lt) {
			save := p.saveCursor()
			nd := len(p.diags)
			mgen = p.parseGenericParams()
			if !p.at(token.LParen) {
				p.restoreCursor(save)
				p.diags = p.diags[:nd]
				mgen = nil
			}
		}
		p.next() // (
		params, hasVA, vaTy := p.parseFuncTypeParams()
		p.expect(token.RParen, "')'")
		var rets []ast.TypeNode
		if p.at(token.Colon) {
			p.next()
			rets = []ast.TypeNode{p.parseType()}
		} else if p.at(token.Arrow) {
			p.next()
			rets = returnTypeToList(p.parseReturnType())
		}
		ft := &ast.FuncType{Params: params, HasVarArg: hasVA, VarArgType: vaTy, Returns: rets, Generics: mgen, S: token.Span{Start: start.Span.Start, End: p.prevEnd(), File: p.file}}
		return &ast.TableTypeField{Name: lit, NameSpan: name.Span, Type: ft, IsMethod: true, MethodSig: ft, S: token.Span{Start: start.Span.Start, End: p.prevEnd(), File: p.file}}
	}
	name := p.parseIdentAllowSoft()
	if name == nil {
		return nil
	}
	optional := false
	if p.at(token.Question) {
		p.next()
		optional = true
	}
	if p.at(token.LParen) {
		p.next()
		params, hasVA, vaTy := p.parseFuncTypeParams()
		p.expect(token.RParen, "')'")
		var rets []ast.TypeNode
		if p.at(token.Colon) {
			p.next()
			rets = []ast.TypeNode{p.parseType()}
		} else if p.at(token.Arrow) {
			p.next()
			rets = returnTypeToList(p.parseReturnType())
		}
		ft := &ast.FuncType{Params: params, HasVarArg: hasVA, VarArgType: vaTy, Returns: rets, S: token.Span{Start: start.Span.Start, End: p.prevEnd(), File: p.file}}
		return &ast.TableTypeField{Name: name.Name, NameSpan: name.Span(), Type: ft, Optional: optional, IsMethod: true, MethodSig: ft, S: token.Span{Start: start.Span.Start, End: p.prevEnd(), File: p.file}}
	}
	p.expect(token.Colon, "':'")
	ty := p.parseType()
	return &ast.TableTypeField{Name: name.Name, NameSpan: name.Span(), Type: ty, Optional: optional, S: token.Span{Start: start.Span.Start, End: p.prevEnd(), File: p.file}}
}

// tryParseTypeArgs 是泛型试吃：`<A,B>` 逗号列+consumeGt 收尾，失败回 false 由调用方 restore，`f<T>(` 有括号才提交。
// consumeGt 收尾（含 `>>` 拆分），成败只回 ok，失败由调用方 restore+截断诊断。
// 障碍是 `<` 与比较同形：调用方（parseSuffix 的 `f<T>(` 与 `:m<T>(`）必须以后接
// '(' 为提交条件，否则按比较回退。取舍：空 `<>` 即零参成功（atGt/EOF 短路），
// 恶意 `<` 不在此报错。不变量：失败路游标/诊断由调用方恢复，此处只管吃。
// 排查现场：若 `f<A>=v` 的 '=' 被吞，先看 consumeGt 的 Ge 拆分余量是否留给赋值。
func (p *Parser) tryParseTypeArgs() ([]ast.TypeNode, bool) {
	if !p.at(token.Lt) {
		return nil, false
	}
	p.next() // <
	var out []ast.TypeNode
	if !p.atGt() && p.cur().Kind != token.EOF {
		for {
			t := p.parseType()
			if t == nil {
				return nil, false
			}
			out = append(out, t)
			if p.at(token.Comma) {
				p.next()
				continue
			}
			break
		}
	}
	if !p.consumeGt() {
		return nil, false
	}
	return out, true
}

// parseGenericParams 解析泛型形参列 `<T [extends C] [= D], ...>`，atGt/consumeGt 收尾。
//
// 设计者视角：parseGenericParams 是泛型的“招生表”——parseIdent 硬收名（软关键字不许
// 作形参），`extends` 后收约束，`=` 后收缺省（双 parseType），逗号续，atGt 停，
// consumeGt 封（含 `>>` 拆分余量进 pending）。障碍是约束/缺省内的 `>`（如 `T extends A<B>`）：
// 内层 parseType 的 consumeGt 逐个消，余量 pending 由外层继续消，天然配对。
// 取舍：名缺席即 break（半截泛型仍返回已收），让调用方以后接 '(' 判定提交与否。
// 不变量：`<` 必消费（调用方已见 Lt），`>` 必经 consumeGt。排查现场：
// 若 `F<A, B>` 只收 A，先看逗号循环的 continue/break。
func (p *Parser) parseGenericParams() []*ast.GenericParam {
	start := p.next() // <
	var out []*ast.GenericParam
	for !p.atGt() && !p.at(token.EOF) {
		n := p.parseIdent()
		if n == nil {
			break
		}
		var constraint ast.TypeNode
		var def ast.TypeNode
		if p.at(token.ExtendsKw) {
			p.next()
			constraint = p.parseType()
		}
		if p.at(token.Assign) {
			p.next()
			def = p.parseType()
		}
		out = append(out, &ast.GenericParam{Name: n, Constraint: constraint, Default: def, S: token.Span{Start: n.Span().Start, End: p.prevEnd(), File: p.file}})
		if p.at(token.Comma) {
			p.next()
			continue
		}
		break
	}
	p.consumeGt()
	_ = start
	return out
}
