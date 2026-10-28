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
			// return must be last; allow trailing ';' then break.
			for p.at(token.Semi) {
				p.next()
			}
			break
		}
		st := p.parseStmt()
		if st == nil {
			// End token encountered; break without consuming.
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

func (p *Parser) parseReturn() *ast.ReturnStmt {
	start := p.next() // return
	var exprs []ast.Expr
	// return is bare if next starts a block end.
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
		// `macro Name` declares; any other use is a plain identifier.
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

// isImportStart peeks `import "path"` / `import Name from` (soft keyword;
// `import` stays a plain identifier everywhere else, including the
// parenthesized call `import("x")`). The token after `import` must sit on
// the same line, so a bare `import` statement followed by a string on the
// next line is never glued into an import.
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
		// `import Name from` or `import Name: Type from`. (`import Name`
		// alone can never start a valid expression statement, so the
		// colon form needs no deeper lookahead.)
		if (n2.Kind == token.Ident && n2.Literal == "from") || n2.Kind == token.Colon {
			return true
		}
	}
	return false
}

// parseImport parses the require sugar:
//
//	import "path"
//	import Name from "path"
//	import Name: Type from "path"
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

// isDeferStart peeks `defer <call>` / `errdefer <call>` (soft keywords:
// the callee must be a plain identifier on the same line, so `defer(x)`
// stays a call to a function named defer and `defer` alone stays a name).
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

// parseDefer parses `defer <call>` / `errdefer <call>`; the call may use
// explicit type arguments (`defer f<T>()`).
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

func (p *Parser) parseDo() ast.Stmt {
	start := p.next()
	b := p.parseBlock()
	e, _ := p.expect(token.End, "'end'")
	_ = e
	return &ast.DoBlock{Block: b, S: token.Span{Start: start.Span.Start, End: p.prevEnd(), File: p.file}}
}

func (p *Parser) prevEnd() token.Pos {
	if p.pos > 0 {
		return p.toks[p.pos-1].Span.End
	}
	return p.cur().Span.Start
}

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

func (p *Parser) parseWhile() ast.Stmt {
	start := p.next()
	c := p.parseExpr()
	p.expect(token.Do, "'do'")
	b := p.parseBlock()
	p.expect(token.End, "'end'")
	return &ast.WhileStmt{Cond: c, Block: b, S: token.Span{Start: start.Span.Start, End: p.prevEnd(), File: p.file}}
}

func (p *Parser) parseRepeat() ast.Stmt {
	start := p.next()
	b := p.parseBlock()
	p.expect(token.Until, "'until'")
	c := p.parseExpr()
	return &ast.RepeatStmt{Block: b, Cond: c, S: token.Span{Start: start.Span.Start, End: c.Span().End, File: p.file}}
}

func (p *Parser) parseFor() ast.Stmt {
	start := p.next()
	name := p.parseIdentAllowSoft()
	if name == nil {
		p.synchronize()
		return &ast.EmptyStmt{S: start.Span}
	}
	// Optional type after loop var: for i: integer = ... (numeric) or for k: K, v: V in ... (generic)
	var firstTy ast.TypeNode
	if p.at(token.Colon) {
		p.next()
		firstTy = p.parseType()
	}
	if p.at(token.Assign) {
		// Numeric for.
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
	// Generic for: name [, name]* [types] in exprlist do block end
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

func (p *Parser) parseGoto() ast.Stmt {
	start := p.next()
	n := p.parseIdentAllowSoft()
	if n == nil {
		return &ast.EmptyStmt{S: start.Span}
	}
	return &ast.GotoStmt{Label: n, S: token.Span{Start: start.Span.Start, End: n.Span().End, File: p.file}}
}

func (p *Parser) parseLabel() ast.Stmt {
	start := p.next() // ::
	n := p.parseIdentAllowSoft()
	if n == nil {
		return &ast.EmptyStmt{S: start.Span}
	}
	p.expect(token.ColonColon, "'::'")
	return &ast.LabelStmt{Name: n, S: token.Span{Start: start.Span.Start, End: p.prevEnd(), File: p.file}}
}

func (p *Parser) parseGlobalFunc() ast.Stmt {
	start := p.next() // function
	name := p.parseFuncName()
	if name == nil {
		p.synchronize()
		return &ast.EmptyStmt{S: start.Span}
	}
	var generics []*ast.GenericParam
	if p.at(token.Lt) {
		// Only treat as generics if it parses and is followed by '('.
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

// parseAttr parses `<const>` / `<close>` if present.
func (p *Parser) parseAttr() string {
	if !p.at(token.Lt) {
		return ""
	}
	// Need Ident then Gt.
	if p.peek(1).Kind != token.Ident {
		return ""
	}
	// peek(2) must be > (or start of >> / >=? attrs are simple, require exact >).
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
