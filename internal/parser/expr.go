package parser

import (
	"github.com/typelua/typelua/internal/ast"
	"github.com/typelua/typelua/internal/diag"
	"github.com/typelua/typelua/internal/token"
)

// ---------- assignment / expr statements ----------

// 表达式子系统物理布局（Pratt + 后缀链）：
//
//	parseExprStmtOrAssign
//	  ├── parseExpr ──► parseExprPrec(precNone+1)
//	  │                    ├── parseUnary  (前缀 ! # ~ - await)
//	  │                    │     └── parseSuffix
//	  │                    │           ├── parsePrimary  (字面量/标识符/括号/表/函数)
//	  │                    │           └── 后缀链：. [] : < () {} string  as  !  >>  (泛型试探)
//	  │                    └── 二元循环：binPrec 查表取优先级/结合性
//	  └── 赋值分支：多 LHS 逗号收集 + '=' + parseExprList
//
// 试探解析：`f<T>(args)` / `obj:m<T>(args)` 用 saveCursor + 诊断长度回退，
// 泛型参数必须后接 '(' 才提交，否则按比较处理。

// parseExprStmtOrAssign 解析表达式语句或赋值：先收首表达式，再按 ',' 与 '=' 分流。
//
// 设计者视角：它是语句层的“岔路口”——Lua 里 `f(x)`、`a = 1`、`a, b = b, a` 长得都像
// 表达式开头，只能先把首个 parseExpr 吃下来，再看逗号/等号决定是赋值还是调用。
// 障碍有二：一是 `a, b` 无 '=' 时只能报错（非赋值非调用）；二是裸标识符在宏体内是
// 语句占位符（splice），要网开一面放行 ExprStmt。取舍：LHS 合法性后验（ast.IsLValue），
// 而非解析期硬卡，让 `f() = 1` 也能产出 AST 再报“非法赋值目标”，定位更准。
// 不变量：返回永非 nil（坏路回 EmptyStmt）。排查现场：若 `x = y` 被报“非调用”，
// 先看 Assign 分支是否因逗号收集吞了换行。
func (p *Parser) parseExprStmtOrAssign() ast.Stmt {
	start := p.cur()
	first := p.parseExpr()
	if first == nil {
		p.synchronize()
		return &ast.EmptyStmt{S: start.Span}
	}
	// Collect comma-separated LHS candidates.
	targets := []ast.Expr{first}
	for p.at(token.Comma) {
		p.next()
		e := p.parseExpr()
		if e == nil {
			break
		}
		targets = append(targets, e)
	}
	if p.at(token.Assign) {
		p.next()
		values := p.parseExprList()
		// Validate lvalues.
		for _, t := range targets {
			if !ast.IsLValue(t) {
				p.errorf(diag.ParseInvalidAssignTarget, t.Span(), "invalid assignment target")
			}
		}
		return &ast.AssignStmt{Targets: targets, Values: values, S: token.Span{Start: start.Span.Start, End: p.prevEnd(), File: p.file}}
	}
	if len(targets) > 1 {
		p.errorf(diag.ParseExpectedAssign, p.cur().Span, "expected '=' in assignment")
		p.synchronize()
		return &ast.EmptyStmt{S: start.Span}
	}
	// Must be a call (awaiting a call counts as a call statement).
	callTarget := first
	for {
		aw, ok := callTarget.(*ast.AwaitExpr)
		if !ok {
			break
		}
		callTarget = aw.X
	}
	switch callTarget.(type) {
	case *ast.CallExpr, *ast.MethodCallExpr:
		return &ast.ExprStmt{E: first, S: first.Span()}
	default:
		// Inside macro bodies a bare identifier is a splice placeholder
		// for a statement argument (validated after substitution).
		if p.inMacroBody > 0 {
			if _, ok := first.(*ast.Ident); ok {
				return &ast.ExprStmt{E: first, S: first.Span()}
			}
		}
		p.errorf(diag.ParseExpectedCall, first.Span(), "expected assignment or function call")
		return &ast.ExprStmt{E: first, S: first.Span()}
	}
}

// parseExprList 解析逗号分隔的表达式列（赋值右值/返回列/call 实参共用）。
//
// 设计者视角：它是“糖葫芦串”——一根逗号签串起若干 parseExpr，空位即收尾。
// 取舍：遇 nil 即停而不报错，把报错权让给调用方（如赋值 Branch 报缺右值），
// 避免一处空位报三次。不变量：返回可为空切片但永不含 nil 元。
// 排查现场：若 `f(a,,b)` 吞了第二个逗号，先看本循环的 break 是否把错误恢复交给了上层。
func (p *Parser) parseExprList() []ast.Expr {
	var out []ast.Expr
	for {
		e := p.parseExpr()
		if e == nil {
			break
		}
		out = append(out, e)
		if !p.at(token.Comma) {
			break
		}
		p.next()
	}
	return out
}

// parseIdent 解析严格标识符，非 Ident 即报 ParseExpectedIdentifier 并返回 nil。
//
// 设计者视角：它是“查身份证”——只认 token.Ident，软关键字（type/class 等）一律不认，
// 专供声明名、泛型形参这类“必须硬名”的位置。障碍是软关键字满地走：误用此处会把
// `local type = 1` 判死刑；取舍是另设 parseIdentAllowSoft 分流，硬软双通道。
// 不变量：成功必消费恰一 Token，失败不动游标之外的状态。排查现场：若关键字作名
// 被误杀，先确认调的是 parseIdent 还是 AllowSoft 版。
func (p *Parser) parseIdent() *ast.Ident {
	if p.cur().Kind != token.Ident {
		p.errorf(diag.ParseExpectedIdentifier, p.cur().Span, "expected identifier, got %s", p.cur().Kind)
		return nil
	}
	t := p.next()
	return &ast.Ident{Name: t.Literal, S: t.Span}
}

// Soft keywords can be used as identifiers in value positions (e.g. `type(x)` calls builtin).
//
// 设计者视角：isSoftKw 是软关键字的“花名册”——词法层把 type/interface/class 等统一标成
// 关键字 Kind，parser 按位置决定它是声明还是名字。障碍是 Lua 超集必须兼容 `type(x)`
// 这类内建调用：若词法直接标 Ident 就丢了声明 corp；标关键字又杀了值用法；取舍是
// “词法标死、语法按需松绑”，本函数即松绑名单。不变量：名单与 token.Kind 的软关键字
// 定义同步增减。排查现场：若新关键字作变量名被误杀，先看是否漏进了本名单。
func isSoftKw(k token.Kind) bool {
	switch k {
	case token.TypeKw, token.InterfaceKw, token.EnumKw, token.ExportKw, token.DeclareKw,
		token.KeyofKw, token.TypeofKw, token.IsKw, token.ReadonlyKw, token.AsKw, token.ExtendsKw,
		token.ClassKw, token.SuperKw, token.StaticKw, token.PrivateKw, token.PublicKw,
		token.ProtectedKw, token.AbstractKw, token.ImplementsKw, token.InferKw, token.MacroKw:
		return true
	}
	return false
}

// parseIdentAllowSoft 解析标识符并接纳软关键字（如 `type`、`class` 作变量名）。
//
// 设计者视角：它是 parseIdent 的“宽容版”——值位置上软关键字仍是好名字
// （`local type = 1`、`x.type`），Literal 为空时回落 Kind.String() 兜底。
// 障碍是字面量缺失的合成 Token：lit=="" 时若直接存空串，下游符号表会撞车；
// 取舍是回落 Kind 名，保证 Name 永非空。不变量：失败（真关键字如 if）必报诊断并返 nil。
// 排查现场：若字段名 `type` 取不到字面量，先看 Token 的 Literal 是否在词法层被置空。
func (p *Parser) parseIdentAllowSoft() *ast.Ident {
	k := p.cur().Kind
	if k == token.Ident || isSoftKw(k) {
		t := p.next()
		lit := t.Literal
		if lit == "" {
			lit = k.String()
		}
		return &ast.Ident{Name: lit, S: t.Span}
	}
	p.errorf(diag.ParseExpectedIdentifier, p.cur().Span, "expected identifier, got %s", p.cur().Kind)
	return nil
}

// isTypeDeclStart reports whether `type/interface/enum/class` at current pos starts a declaration
// (as opposed to a value use like `type(x)`).
//
// 设计者视角：它是声明与值的“分水岭”——同样 `type` 开头，`type Foo = ...` 是声明，
// `type(x)` 是调用；判定只看后一个 Token 是否为 Ident（声明必有名字），`type(`/`type =`
// 即值用。障碍是 `abstract class Foo` 双词开头，需三步前瞻；取舍是纯前瞻零消费，
// 误判宁可偏向值（parseExprStmtOrAssign 兜底），也不提前吞声明关键字。
// 不变量：只读游标，无副作用。排查现场：若 `type = 1` 被当声明吞，先看 peek(1)
// 是否为 Assign（应判 false 走值路）。
func (p *Parser) isTypeDeclStart() bool {
	k := p.cur().Kind
	// `abstract class Foo` also starts a class decl.
	if k == token.AbstractKw && p.peek(1).Kind == token.ClassKw {
		return p.peek(2).Kind == token.Ident
	}
	if k != token.TypeKw && k != token.InterfaceKw && k != token.EnumKw && k != token.ClassKw {
		return false
	}
	// Must be followed by an identifier (decl name). `type(` or `type =` is a value use.
	return p.peek(1).Kind == token.Ident
}

// ---------- expressions (Pratt) ----------

// precNone 等是 Pratt 表达式的优先级阶梯（Or < And < Cmp < 位运算 < Shift < Concat < Add < Mul < Unary < Pow）。
//
// 设计者视角：这组常量是二元运算的“楼层表”——parseExprPrec 爬楼，同层左结合逐层吃，
// 右结合（`..` 与 `^`）不涨 minPrec 以便右递归。障碍是 Lua 的 `^` 右结合、`..` 右结合
// 且低于加减：若照抄 C 优先级会算错 `2^3^2`；取舍是手写 binPrec 查表而非表驱动生成，
// 一眼可对 Lua 手册。不变量：precNone+1 即表达式入口，最低一层亦可入。
// 排查现场：若 `a..b..c` 结合反了，先看 binPrec 回的 right 标志，再看 nextMin 是否取 prec。
const (
	precNone = iota
	precOr
	precAnd
	precCmp
	precBor
	precBxor
	precBand
	precShift
	precConcat
	precAdd
	precMul
	precUnary
	precPow
)

// binPrec 查二元运算符的优先级与右结合标志，非二元符回 (0,false)。
//
// 设计者视角：binPrec 是 Pratt 循环的“价目表”——调用方只问“这符号几楼、电梯朝哪”，
// 不关心符号拼写。障碍是比较符有六兄弟（< > <= >= == ~=），位运算有四档；取舍是
// switch 直写而非 map，零分配且编译期穷举。不变量：返回 prec==0 即“不是二元位”，
// 循环必停。排查现场：若 `and` 被当乘法，先看此处 case 是否把 And 错挂到 precMul。
func binPrec(k token.Kind) (int, bool) {
	right := false
	var prec int
	switch k {
	case token.Or:
		prec = precOr
	case token.And:
		prec = precAnd
	case token.Lt, token.Gt, token.Le, token.Ge, token.Eq, token.Ne:
		prec = precCmp
	case token.Pipe:
		prec = precBor
	case token.Tilde:
		prec = precBxor
	case token.Amp:
		prec = precBand
	case token.ShiftL, token.ShiftR:
		prec = precShift
	case token.DotDot:
		prec = precConcat
		right = true
	case token.Plus, token.Minus:
		prec = precAdd
	case token.Star, token.Slash, token.DoubleSlash, token.Percent:
		prec = precMul
	case token.Caret:
		prec = precPow
		right = true
	default:
		return 0, false
	}
	return prec, right
}

// parseExpr 解析完整表达式（Pratt 入口，minPrec 为最低层）。
//
// 设计者视角：parseExpr 是表达式世界的“正门”——全文件凡要一个值（右值、条件、实参、
// 表值）都走此门，门内即 parseExprPrec(precNone+1)。取舍是薄转发而非 inline，让
// 调用栈一眼分出“要值”与“要带优先级的值”。不变量：与 parseExprPrec 最低档等价。
// 排查现场：若某处表达式优先级异常，先确认调的是 parseExpr 还是带 minPrec 的内部版。
func (p *Parser) parseExpr() ast.Expr { return p.parseExprPrec(precNone + 1) }

// parseExprPrec 以 minPrec 为门槛爬 Pratt 楼梯：左侧 parseUnary 开路，右侧按优先级循环收割。
//
// 设计者视角：它是 Pratt 的“传送带”——lhs 先由一元/后缀链造出，再看当前符够不够格
// （prec==0 或 prec<minPrec 即停），够格就吃掉并以 nextMin（左结合 +1、右结合不加）
// 递归收 rhs，拼成 BinOpExpr 再循环。障碍有二：一是 `!=` 在词法层是 `!`+`=` 两颗 Token，
// 要在循环首特判拼成 Ne；二是 rhs 缺席（如 `a + end`）要报缺操作数并收尾，不可空转。
// 不变量：返回的 lhs Span 从首 Token 起到末 Token 止，单调覆盖。排查现场：
// 若 `a + b * c` 算成 `(a+b)*c`，先打印各 binPrec 再看 nextMin 是否漏 +1。
func (p *Parser) parseExprPrec(minPrec int) ast.Expr {
	// Handle `!=` (lexed as ! then =) at any binary position? Check first.
	lhs := p.parseUnary()
	if lhs == nil {
		return nil
	}
	for {
		// Support != as alias for ~=.
		if p.at(token.Bang) && p.peek(1).Kind == token.Assign {
			opSpan := token.Span{Start: p.cur().Span.Start, End: p.peek(1).Span.End, File: p.file}
			prec := precCmp
			if prec < minPrec {
				break
			}
			p.next()
			p.next()
			rhs := p.parseExprPrec(prec + 1)
			lhs = &ast.BinOpExpr{Op: token.Ne, L: lhs, R: rhs, S: token.Span{Start: lhs.Span().Start, End: rhs.Span().End, File: p.file}}
			_ = opSpan
			continue
		}
		prec, right := binPrec(p.cur().Kind)
		if prec == 0 || prec < minPrec {
			break
		}
		op := p.next()
		nextMin := prec + 1
		if right {
			nextMin = prec
		}
		rhs := p.parseExprPrec(nextMin)
		if rhs == nil {
			p.errorf(diag.ParseExpectedOperand, op.Span, "expected expression after '%s'", op.Kind)
			return lhs
		}
		lhs = &ast.BinOpExpr{Op: op.Kind, L: lhs, R: rhs, S: token.Span{Start: lhs.Span().Start, End: rhs.Span().End, File: p.file}}
	}
	return lhs
}

// parseUnary 解析前缀一元（not/#/~/-/await），无前缀则下沉 parseSuffix。
//
// 设计者视角：parseUnary 是前缀的“安检口”——`not`/`#`/`~`/`-` 与 await 前缀在此统一
// 验放，一律以 precUnary 为门槛递归吃操作数，保证 `-2^2` 判成 `-(2^2)`（Pow 高于 Unary）。
// 障碍是 await 的软关键字外形：必须先经 isAwaitPrefix 判定，`await(x)` 留给调用 suites，
// `await -x` 则收为 Await(-x)。取舍：一元错（如 `- end`）直接回 nil 让上层收尾，
// 而非在此 synchronize，避免吞掉块边界。不变量：成功节点的 S 从 op 起到操作数尾。
// 排查现场：若 `-x` 的 Span 漏了负号，先看 op.Span.Start 是否作为 S.Start。
func (p *Parser) parseUnary() ast.Expr {
	if p.isAwaitPrefix() {
		return p.parseAwait()
	}
	switch p.cur().Kind {
	case token.Not, token.Hash, token.Tilde:
		op := p.next()
		x := p.parseExprPrec(precUnary)
		if x == nil {
			return nil
		}
		return &ast.UnOpExpr{Op: op.Kind, X: x, S: token.Span{Start: op.Span.Start, End: x.Span().End, File: p.file}}
	case token.Minus:
		op := p.next()
		x := p.parseExprPrec(precUnary)
		if x == nil {
			return nil
		}
		return &ast.UnOpExpr{Op: op.Kind, X: x, S: token.Span{Start: op.Span.Start, End: x.Span().End, File: p.file}}
	}
	return p.parseSuffix()
}

// parseSuffix 解析后缀链：`.field`/`[k]`/`:m<T>()`/`f<T>()`/`()`/`as T`/`!`，是表达式最长的尾巴。
//
// 设计者视角：parseSuffix 是 Primary 后的“串珠机”——parsePrimary 造出珠头，
// for 循环逐颗串后缀，直到当前 Token 不再像后缀即停。障碍有三：
//  1. `:` 在表达式语境恒为方法调用（与类型冒号无涉）；
//  2. `<` 可能是泛型实参也可能是比较，必须 saveCursor+诊断回退试探，且后接 '(' 才提交；
//  3. `!` 可能是非空断言也可能是 `!=` 的前半，peek('=') 即让路给 parseExprPrec 的 Ne 分支。
//
// 取舍：试探失败一律 restore+截断诊断，零副作用；as/cast 吃全类型（`as` 后 parseType）。
//
// 链式示意：
//
//	Primary ──.field──[k]──:m<T>(args)──<T>(args)──(args)──as T──!──► Expr
//	   ▲           └── 循环直到无后缀 ──┘
func (p *Parser) parseSuffix() ast.Expr {
	e := p.parsePrimary()
	if e == nil {
		return nil
	}
	for {
		switch {
		case p.at(token.Dot):
			p.next()
			f := p.parseIdentAllowSoft()
			if f == nil {
				return e
			}
			e = &ast.FieldExpr{Obj: e, Field: f, S: token.Span{Start: e.Span().Start, End: f.Span().End, File: p.file}}
		case p.at(token.LBrack):
			p.next()
			k := p.parseExpr()
			p.expect(token.RBrack, "']'")
			if k == nil {
				return e
			}
			e = &ast.IndexExpr{Obj: e, Key: k, S: token.Span{Start: e.Span().Start, End: p.prevEnd(), File: p.file}}
		case p.at(token.Colon):
			// Distinguish method call `obj:method(args)` from type-ish? In expr context, colon always method call.
			// But careful: after a parenthesized expr in statement `f() : T`? That's not valid expr; ignore.
			p.next()
			m := p.parseIdentAllowSoft()
			if m == nil {
				return e
			}
			var targs []ast.TypeNode
			if p.at(token.Lt) {
				// `obj:m<T>(args)`: tentative type args, must be followed by `(`.
				save := p.saveCursor()
				nd := len(p.diags)
				if tas, ok := p.tryParseTypeArgs(); ok && p.at(token.LParen) {
					targs = tas
				} else {
					p.restoreCursor(save)
					p.diags = p.diags[:nd]
				}
			}
			args := p.parseCallArgs()
			if args == nil {
				p.errorf(diag.ParseExpectedMethodArgs, p.cur().Span, "expected method call arguments after ':method'")
				return e
			}
			e = &ast.MethodCallExpr{Obj: e, Method: m, TypeArgs: targs, Args: args, S: token.Span{Start: e.Span().Start, End: p.prevEnd(), File: p.file}}
		case p.at(token.Lt):
			// `f<T>(args)`: explicit generic arguments. Tentative: `<` types `>`
			// must be followed by `(`; otherwise this is a `<` comparison.
			save := p.saveCursor()
			nd := len(p.diags)
			tas, ok := p.tryParseTypeArgs()
			if !ok || !p.at(token.LParen) {
				p.restoreCursor(save)
				p.diags = p.diags[:nd]
				return e
			}
			args := p.parseCallArgs()
			e = &ast.CallExpr{Func: e, TypeArgs: tas, Args: args, S: token.Span{Start: e.Span().Start, End: p.prevEnd(), File: p.file}}
		case p.at(token.LParen), p.at(token.LBrace), p.at(token.String):
			args := p.parseCallArgs()
			e = &ast.CallExpr{Func: e, Args: args, S: token.Span{Start: e.Span().Start, End: p.prevEnd(), File: p.file}}
		case p.at(token.AsKw):
			p.next()
			ty := p.parseType()
			e = &ast.CastExpr{E: e, T: ty, S: token.Span{Start: e.Span().Start, End: p.prevEnd(), File: p.file}}
		case p.at(token.Bang):
			// Postfix non-null; but not if it's != (handled in binop loop).
			if p.peek(1).Kind == token.Assign {
				return e
			}
			b := p.next()
			e = &ast.NonNullExpr{E: e, S: token.Span{Start: e.Span().Start, End: b.Span.End, File: p.file}}
		default:
			return e
		}
	}
}

// parseCallArgs 解析调用实参三种形态：`(a,b)` / `{table}` / `"str"`，皆归一为 []ast.Expr。
//
// 设计者视角：parseCallArgs 是 Lua 糖调用的“收纳盒”——`f{...}` 与 `f"..."` 在 Lua 里
// 都是单实参调用，此处分别包成 TableLit/StringLit 再装片，checker 只见统一切片。
// 障碍是无括号调用后无 RParen 可对齐：取舍是直接返回单元素片，Span 即字面量 Span。
// 不变量：非调用位回 nil（调用方以 nil 判“无实参”，如方法冒号后缺参即报错）。
// 排查现场：若 `f"hi"` 实参为空，先看 String 分支是否把 Literal 误当 Kind。
func (p *Parser) parseCallArgs() []ast.Expr {
	if p.at(token.LParen) {
		p.next()
		args := []ast.Expr{}
		if !p.at(token.RParen) {
			for {
				a := p.parseExpr()
				if a == nil {
					break
				}
				args = append(args, a)
				if !p.at(token.Comma) {
					break
				}
				p.next()
			}
		}
		p.expect(token.RParen, "')'")
		return args
	}
	if p.at(token.LBrace) {
		t := p.parseTable()
		return []ast.Expr{t}
	}
	if p.at(token.String) {
		t := p.next()
		return []ast.Expr{&ast.StringLit{Literal: t.Literal, S: t.Span}}
	}
	return nil
}

// parsePrimary 解析原子表达式：字面量/标识符/括号/表/function（含 async 前缀与泛型试探）。
//
// 设计者视角：parsePrimary 是表达式的“地基”——所有后缀链都站在这块板上，板歪则全楼歪。
// 障碍有三：一是 async/function 双前缀要在后缀之前判定（isAsyncFuncStart 优先）；
// 二是软关键字在值位仍是 Ident（如 `type`），需 isSoftKw 放行；三是匿名 function 的
// `<T>` 泛型要试探（后接 '(' 才提交，否则按比较回退）。取舍：入口即 enterDepth，
// 深括号 `((((...))))` 由深度熔断兜底；未知 Token 报缺表达式并返 nil，让后缀循环停转。
// 不变量：成功必消费至少一 Token；`(...)` 包 ParenExpr 保留括号 Span。
// 排查现场：若 `async function` 被拆成两个变量，先看 isAsyncFuncStart 的 sameLine 是否跨行。
func (p *Parser) parsePrimary() ast.Expr {
	c := p.cur()
	p.enterDepth(c.Span)
	defer p.leaveDepth()
	if p.isAsyncFuncStart() {
		return p.parseAsyncFuncLit(false)
	}
	if c.Kind == token.Ident || isSoftKw(c.Kind) {
		p.next()
		lit := c.Literal
		if lit == "" {
			lit = c.Kind.String()
		}
		return &ast.Ident{Name: lit, S: c.Span}
	}
	switch c.Kind {
	case token.Number:
		p.next()
		return &ast.NumberLit{Literal: c.Literal, S: c.Span}
	case token.String:
		p.next()
		return &ast.StringLit{Literal: c.Literal, S: c.Span}
	case token.True:
		p.next()
		return &ast.BoolLit{Value: true, S: c.Span}
	case token.False:
		p.next()
		return &ast.BoolLit{Value: false, S: c.Span}
	case token.Nil:
		p.next()
		return &ast.NilLit{S: c.Span}
	case token.DotDotDot:
		p.next()
		return &ast.VarArg{S: c.Span}
	case token.Function:
		p.next()
		// Optional generics on anonymous functions.
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
		fn := p.parseFuncBody(false)
		fn.Generics = fgen
		return fn
	case token.LBrace:
		return p.parseTable()
	case token.LParen:
		p.next()
		e := p.parseExpr()
		p.expect(token.RParen, "')'")
		if e == nil {
			return nil
		}
		return &ast.ParenExpr{E: e, S: token.Span{Start: c.Span.Start, End: p.prevEnd(), File: p.file}}
	}
	p.errorf(diag.ParseExpectedExpression, c.Span, "expected expression, got %s", c.Kind)
	return nil
}

// parseTable 解析表字面量 `{ fields }`，字段错由 synchronizeTable 局部恢复。
//
// 设计者视角：parseTable 是 Lua 表的“装箱机”——`{}` 空箱亦合法，`;`/`,` 皆可为分隔，
// 坏字段只跳一格（synchronizeTable 到下一分隔），好字段照单全收。取舍：缺 `}`
// 时 expect 只记账不停工，让外层块继续；空表 Span 即括号对，LSP 仍有坐标可跳。
// 不变量：返回的 TableLit 永非 nil，Fields 保序。排查现场：若表错后 `}` 被吞，
// 先看 synchronizeTable 的 depth 配对，再看本循环的 RBrace 终止条件。
func (p *Parser) parseTable() ast.Expr {
	start := p.next() // {
	var fields []*ast.TableField
	for !p.at(token.RBrace) && !p.at(token.EOF) {
		if p.at(token.Semi) || p.at(token.Comma) {
			p.next()
			continue
		}
		f := p.parseTableField()
		if f == nil {
			p.synchronizeTable()
			continue
		}
		fields = append(fields, f)
		if p.at(token.Comma) || p.at(token.Semi) {
			p.next()
		}
	}
	p.expect(token.RBrace, "'}'")
	return &ast.TableLit{Fields: fields, S: token.Span{Start: start.Span.Start, End: p.prevEnd(), File: p.file}}
}

// parseTableField 解析单个表字段：`[k]=v` 索引式、`Name=v` 具名式、余者表达式式。
//
// 设计者视角：它是表装箱机的“分拣口”——先看 `[`（索引式）、再看 `Ident =`（具名式，
// 软关键字亦可作 Key），都不是即当表达式式。障碍是 `Name =` 与表达式 `Name` 前缀相同：
// 用 peek(1)==Assign 一眼区分，零消费判定。取舍：具名 Key 存 Ident 而非 Expr，
// 让 emitter 可原样回写 `x=1` 而非 `[\"x\"]=1`。不变量：失败回 nil，由 parseTable
// 同步恢复；成功 Span 覆盖键值全段。排查现场：若 `{type = 1}` 的 Key 为空，
// 先看 isSoftKw 是否放行了 `type`。
func (p *Parser) parseTableField() *ast.TableField {
	// `[expr] = expr`
	if p.at(token.LBrack) {
		start := p.next()
		k := p.parseExpr()
		p.expect(token.RBrack, "']'")
		p.expect(token.Assign, "'='")
		v := p.parseExpr()
		return &ast.TableField{Kind: ast.IndexedField, KeyX: k, Val: v, S: token.Span{Start: start.Span.Start, End: p.prevEnd(), File: p.file}}
	}
	// `Name = expr` vs expr.
	if (p.cur().Kind == token.Ident || isSoftKw(p.cur().Kind)) && p.peek(1).Kind == token.Assign {
		start := p.cur()
		n := p.next()
		lit := n.Literal
		if lit == "" {
			lit = n.Kind.String()
		}
		p.next() // =
		v := p.parseExpr()
		return &ast.TableField{Kind: ast.NamedField, Key: &ast.Ident{Name: lit, S: n.Span}, Val: v, S: token.Span{Start: start.Span.Start, End: p.prevEnd(), File: p.file}}
	}
	e := p.parseExpr()
	if e == nil {
		return nil
	}
	return &ast.TableField{Kind: ast.ExprField, Val: e, S: e.Span()}
}
