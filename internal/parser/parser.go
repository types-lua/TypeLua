package parser

import (
	"github.com/typelua/typelua/internal/ast"
	"github.com/typelua/typelua/internal/diag"
	"github.com/typelua/typelua/internal/lexer"
	"github.com/typelua/typelua/internal/token"
)

// Parser is a recursive-descent parser with Pratt expressions and recovery.
type Parser struct {
	toks         []token.Token
	pos          int
	file         string
	diags        []diag.Diagnostic
	pendingGt    int // split >> into two >
	extendsDepth int // >0 while parsing an extends clause (for `infer`)
	inMacroBody  int // >0 while parsing a macro definition body
}

func ParseFile(filename, src string) (*ast.File, []diag.Diagnostic) {
	toks, ldiags := lexer.Lex(filename, src)
	p := &Parser{toks: toks, file: filename, diags: append([]diag.Diagnostic{}, ldiags...)}
	block := p.parseBlock()
	eofSpan := token.Span{File: filename}
	if len(toks) > 0 {
		eofSpan = toks[len(toks)-1].Span
	}
	// Drain to EOF.
	return &ast.File{Name: filename, Block: block, EOF: eofSpan}, p.diags
}

// ---------- cursor helpers ----------

func (p *Parser) cur() token.Token {
	if p.pendingGt > 0 {
		t := p.toks[p.pos]
		// Synthesize a > token at current position.
		return token.Token{Kind: token.Gt, Literal: ">", Span: t.Span}
	}
	if p.pos < len(p.toks) {
		return p.toks[p.pos]
	}
	return token.Token{Kind: token.EOF}
}

func (p *Parser) peek(n int) token.Token {
	idx := p.pos + n
	if p.pendingGt > 0 && n == 0 {
		return p.cur()
	}
	// pendingGt only affects current; peeks beyond shift by 0 (approx, good enough).
	if idx < len(p.toks) {
		return p.toks[idx]
	}
	return token.Token{Kind: token.EOF}
}

func (p *Parser) at(k token.Kind) bool { return p.cur().Kind == k }

func (p *Parser) next() token.Token {
	if p.pendingGt > 0 {
		t := p.toks[p.pos]
		p.pendingGt--
		if p.pendingGt == 0 {
			// Consumed the first '>'; now consume the original >> token,
			// but leave one pending '>' if it was >> (we already returned one).
			// Actually >> represents two '>'s: first returned now, second pending?
			// Our model: when we see >>, we set pendingGt=1 and return one '>' without advancing.
			// Hmm simplify below in consumeGt.
		}
		_ = t
		// Return synthesized token; only advance underlying pos when pending exhausted?
		// We handle advancement in consumeGt; for generic next() just fall through.
		p.pendingGt = 0
		p.pos++
		return token.Token{Kind: token.Gt, Literal: ">", Span: t.Span}
	}
	t := p.cur()
	if p.pos < len(p.toks) {
		p.pos++
	}
	return t
}

func (p *Parser) expect(k token.Kind, what string) (token.Token, bool) {
	if p.at(k) {
		return p.next(), true
	}
	p.errorf("TL2001", p.cur().Span, "expected %s, got %s", what, p.cur().Kind)
	return p.cur(), false
}

func (p *Parser) errorf(code string, sp token.Span, format string, args ...any) {
	p.diags = append(p.diags, diag.Errorf(code, sp, format, args...))
}

func (p *Parser) spanFrom(start token.Token) token.Span {
	end := p.cur().Span
	if p.pos > 0 {
		end = p.toks[p.pos-1].Span
		// end.End is end of last consumed.
	}
	return token.Span{Start: start.Span.Start, End: end.End, File: p.file}
}

func (p *Parser) spanBetween(start token.Token, end token.Token) token.Span {
	return token.Span{Start: start.Span.Start, End: end.Span.End, File: p.file}
}

// consumeGt consumes a single '>', splitting >> / >= if needed.
func (p *Parser) consumeGt() bool {
	c := p.cur()
	switch c.Kind {
	case token.Gt:
		p.next()
		return true
	case token.ShiftR:
		// >> => consume one '>', leave one pending.
		t := p.toks[p.pos]
		p.pos++
		// Push back a synthesized '>' for next call.
		p.toks = append(p.toks, token.Token{})
		copy(p.toks[p.pos+1:], p.toks[p.pos:])
		p.toks[p.pos] = token.Token{Kind: token.Gt, Literal: ">", Span: t.Span}
		return true
	case token.Ge:
		// >= inside generics: treat as '>' then '='? Only valid if default type follows '='.
		// Split into '>' and '='.
		t := p.toks[p.pos]
		p.pos++
		p.toks = append(p.toks, token.Token{}, token.Token{})
		copy(p.toks[p.pos+2:], p.toks[p.pos:])
		p.toks[p.pos] = token.Token{Kind: token.Assign, Literal: "=", Span: t.Span}
		p.toks[p.pos+1] = token.Token{Kind: token.Gt, Literal: ">", Span: t.Span}
		// We need '>' first; swap.
		p.toks[p.pos], p.toks[p.pos+1] = p.toks[p.pos+1], p.toks[p.pos]
		return true
	}
	p.errorf("TL2002", c.Span, "expected '>', got %s", c.Kind)
	return false
}

func (p *Parser) atGt() bool {
	k := p.cur().Kind
	return k == token.Gt || k == token.ShiftR || k == token.Ge
}

// ---------- blocks & statements ----------

func (p *Parser) parseBlock() *ast.Block {
	start := p.cur()
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
		save := p.pos
		nd := len(p.diags)
		g := p.parseGenericParams()
		if g != nil && p.at(token.LParen) {
			generics = g
		} else {
			p.pos = save
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
	if p.at(token.Function) {
		p.next()
		name := p.parseIdentAllowSoft()
		if name == nil {
			p.synchronize()
			return &ast.EmptyStmt{S: start.Span}
		}
		var generics []*ast.GenericParam
		if p.at(token.Lt) {
			save := p.pos
			nd := len(p.diags)
			g := p.parseGenericParams()
			if g != nil && p.at(token.LParen) {
				generics = g
			} else {
				p.pos = save
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
		p.errorf("TL2003", p.cur().Span, "expected local variable name")
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
		p.errorf("TL2004", name.Span, "unknown attribute <%s>, expected <const> or <close>", name.Literal)
	}
	return name.Literal
}

func (p *Parser) parseExport() ast.Stmt {
	start := p.next()
	if p.at(token.ClassKw) || (p.at(token.AbstractKw) && p.peek(1).Kind == token.ClassKw) {
		d := p.parseClassDecl(true, false, false)
		if cd, ok := d.(*ast.ClassDecl); ok {
			sp := cd.Span()
			sp.Start = start.Span.Start
			cd.S = sp
		}
		return d
	}
	if p.at(token.TypeKw) || p.at(token.InterfaceKw) || p.at(token.EnumKw) {
		d := p.parseTypeDecl(true, false)
		// Fix span to include export.
		sp := d.Span()
		sp.Start = start.Span.Start
		switch n := d.(type) {
		case *ast.TypeAliasDecl:
			n.S = sp
		case *ast.InterfaceDecl:
			n.S = sp
		case *ast.EnumDecl:
			n.S = sp
		}
		return d
	}
	if p.at(token.Function) {
		// export function foo() ... end  (sugar for global)
		p.next()
		name := p.parseFuncName()
		fn := p.parseFuncBody(name != nil && name.Method != nil)
		sp := token.Span{Start: start.Span.Start, End: fn.Span().End, File: p.file}
		return &ast.FuncDefStmt{Name: name, Func: fn, S: sp}
	}
	if p.at(token.Local) {
		// export local ... : mark exported where applicable.
		st := p.parseLocal()
		if lf, ok := st.(*ast.LocalFuncDef); ok {
			lf.Exported = true
			lf.S.Start = start.Span.Start
		}
		return st
	}
	if p.at(token.MacroKw) {
		p.errorf("TL2005", p.cur().Span, "macros cannot be exported (they expand within their file)")
		p.synchronize()
		return &ast.EmptyStmt{S: start.Span}
	}
	p.errorf("TL2005", p.cur().Span, "expected declaration after 'export'")
	p.synchronize()
	return &ast.EmptyStmt{S: start.Span}
}

func (p *Parser) parseDeclare() ast.Stmt {
	start := p.next()
	// declare only applies to type-space decls (no runtime emit).
	if p.at(token.ClassKw) || (p.at(token.AbstractKw) && p.peek(1).Kind == token.ClassKw) {
		d := p.parseClassDecl(false, true, false)
		if cd, ok := d.(*ast.ClassDecl); ok {
			sp := cd.Span()
			sp.Start = start.Span.Start
			cd.S = sp
		}
		return d
	}
	if p.at(token.TypeKw) || p.at(token.InterfaceKw) || p.at(token.EnumKw) {
		d := p.parseTypeDecl(false, true)
		sp := d.Span()
		sp.Start = start.Span.Start
		switch n := d.(type) {
		case *ast.TypeAliasDecl:
			n.S = sp
		case *ast.InterfaceDecl:
			n.S = sp
		case *ast.EnumDecl:
			n.S = sp
		}
		return d
	}
	if p.at(token.Function) {
		// declare function foo(a: T): R  (no body)
		p.next()
		name := p.parseFuncName()
		// Optional generics.
		var generics []*ast.GenericParam
		if p.at(token.Lt) {
			save := p.pos
			nd := len(p.diags)
			g := p.parseGenericParams()
			if g != nil && p.at(token.LParen) {
				generics = g
			} else {
				p.pos = save
				p.diags = p.diags[:nd]
			}
		}
		// Parse signature only: (params) [-> ret], no body.
		if _, ok := p.expect(token.LParen, "'('"); !ok {
			return &ast.EmptyStmt{S: start.Span}
		}
		params, hasVA, vaName, vaTy := p.parseParams()
		p.expect(token.RParen, "')'")
		var retTy ast.TypeNode
		if p.at(token.Arrow) {
			p.next()
			retTy = p.parseReturnType()
		} else if p.at(token.Colon) {
			p.next()
			retTy = p.parseType()
		}
		fn := &ast.FuncLit{Params: params, HasVarArg: hasVA, VarArgName: vaName, VarArgType: vaTy, ReturnType: retTy, Body: &ast.Block{}, S: token.Span{Start: start.Span.Start, End: p.prevEnd(), File: p.file}}
		return &ast.FuncDefStmt{Name: name, Generics: generics, Func: fn, S: fn.S}
	}
	if p.at(token.MacroKw) {
		p.errorf("TL2006", p.cur().Span, "macros cannot be declared (they have no runtime value)")
		p.synchronize()
		return &ast.EmptyStmt{S: start.Span}
	}
	p.errorf("TL2006", p.cur().Span, "expected declaration after 'declare'")
	p.synchronize()
	return &ast.EmptyStmt{S: start.Span}
}

// parseMacroDef parses:
//   macro Name(p1, p2) = expr            (expression macro)
//   macro Name(p1, p2) do stmts end      (statement macro)
// A trailing `...` collects extra call arguments (variadic macro).
// Parameters are strict identifiers; `self`/`super` are reserved.
func (p *Parser) parseMacroDef() ast.Stmt {
	start := p.next() // macro
	name := p.parseIdent()
	if name == nil {
		p.synchronize()
		return &ast.EmptyStmt{S: start.Span}
	}
	if _, ok := p.expect(token.LParen, "'('"); !ok {
		p.synchronize()
		return &ast.EmptyStmt{S: start.Span}
	}
	var params []*ast.Ident
	hasVA := false
	if !p.at(token.RParen) {
		for {
			if p.at(token.DotDotDot) {
				dots := p.next()
				hasVA = true
				if !p.at(token.RParen) {
					p.errorf("TL2041", dots.Span, "'...' must be the last macro parameter")
				}
				break
			}
			n := p.parseIdent()
			if n == nil {
				break
			}
			if n.Name == "self" || n.Name == "super" {
				p.errorf("TL2038", n.Span(), "macro parameter cannot be named '%s'", n.Name)
			}
			params = append(params, n)
			if !p.at(token.Comma) {
				break
			}
			p.next()
		}
	}
	p.expect(token.RParen, "')'")
	// Duplicate parameter names shadow confusingly; reject outright.
	seen := map[string]bool{}
	for _, n := range params {
		if seen[n.Name] {
			p.errorf("TL2039", n.Span(), "duplicate macro parameter '%s'", n.Name)
		}
		seen[n.Name] = true
	}
	if p.at(token.Assign) {
		p.next()
		body := p.parseExpr()
		if body == nil {
			p.synchronize()
			return &ast.EmptyStmt{S: start.Span}
		}
		return &ast.MacroDef{Name: name, Params: params, HasVarArg: hasVA, Body: body, IsExpr: true, S: token.Span{Start: start.Span.Start, End: p.prevEnd(), File: p.file}}
	}
	if p.at(token.Do) {
		p.next()
		p.inMacroBody++
		body := p.parseBlock()
		p.inMacroBody--
		p.expect(token.End, "'end'")
		if body.Ret != nil {
			p.errorf("TL2040", body.Ret.Span(), "statement macro body cannot return a value")
		}
		return &ast.MacroDef{Name: name, Params: params, HasVarArg: hasVA, Block: body, IsExpr: false, S: token.Span{Start: start.Span.Start, End: p.prevEnd(), File: p.file}}
	}
	p.errorf("TL2001", p.cur().Span, "expected '=' or 'do' after macro parameters")
	p.synchronize()
	return &ast.EmptyStmt{S: start.Span}
}

func (p *Parser) parseTypeDecl(exported, declared bool) ast.Stmt {
	switch p.cur().Kind {
	case token.TypeKw:
		start := p.next()
		name := p.parseIdent()
		if name == nil {
			p.synchronize()
			return &ast.EmptyStmt{S: start.Span}
		}
		var generics []*ast.GenericParam
		if p.at(token.Lt) {
			generics = p.parseGenericParams()
		}
		p.expect(token.Assign, "'='")
		ty := p.parseType()
		sp := token.Span{Start: start.Span.Start, End: p.prevEnd(), File: p.file}
		return &ast.TypeAliasDecl{Name: name, Generics: generics, Type: ty, Exported: exported, Declared: declared, S: sp}
	case token.InterfaceKw:
		start := p.next()
		name := p.parseIdent()
		if name == nil {
			p.synchronize()
			return &ast.EmptyStmt{S: start.Span}
		}
		var generics []*ast.GenericParam
		if p.at(token.Lt) {
			generics = p.parseGenericParams()
		}
		var extends []ast.TypeNode
		if p.at(token.ExtendsKw) {
			p.next()
			for {
				extends = append(extends, p.parseType())
				if !p.at(token.Comma) {
					break
				}
				p.next()
			}
		}
		p.expect(token.LBrace, "'{'")
		var fields []*ast.InterfaceField
		for !p.at(token.RBrace) && !p.at(token.EOF) {
			if p.at(token.Semi) || p.at(token.Comma) {
				p.next()
				continue
			}
			f := p.parseInterfaceField()
			if f == nil {
				p.synchronizeInterface()
				continue
			}
			fields = append(fields, f)
			if p.at(token.Comma) || p.at(token.Semi) {
				p.next()
			}
		}
		p.expect(token.RBrace, "'}'")
		return &ast.InterfaceDecl{Name: name, Generics: generics, Extends: extends, Fields: fields, Exported: exported, Declared: declared, S: token.Span{Start: start.Span.Start, End: p.prevEnd(), File: p.file}}
	case token.EnumKw:
		start := p.next()
		name := p.parseIdent()
		if name == nil {
			p.synchronize()
			return &ast.EmptyStmt{S: start.Span}
		}
		p.expect(token.LBrace, "'{'")
		var members []*ast.EnumMember
		for !p.at(token.RBrace) && !p.at(token.EOF) {
			if p.at(token.Semi) || p.at(token.Comma) {
				p.next()
				continue
			}
			mstart := p.cur()
			mname := p.parseIdentAllowSoft()
			if mname == nil {
				p.synchronizeInterface()
				continue
			}
			var val ast.Expr
			if p.at(token.Assign) {
				p.next()
				val = p.parseExpr()
			}
			members = append(members, &ast.EnumMember{Name: mname, Value: val, S: token.Span{Start: mstart.Span.Start, End: p.prevEnd(), File: p.file}})
			if p.at(token.Comma) || p.at(token.Semi) {
				p.next()
			}
		}
		p.expect(token.RBrace, "'}'")
		return &ast.EnumDecl{Name: name, Members: members, Exported: exported, Declared: declared, S: token.Span{Start: start.Span.Start, End: p.prevEnd(), File: p.file}}
	}
	return nil
}

// parseClassDecl parses:
//   [abstract] class Name<T> [extends Base] [implements I, ...] { members }
// Members are fields (`[mods] name[: T] [= init]`), methods
// (`[mods] name<T>(params)[: R | -> R] body end`) and at most one
// `constructor(params) body end`. In `declare` classes and on `abstract`
// members there is no body.
func (p *Parser) parseClassDecl(exported, declared, isLocal bool) ast.Stmt {
	start := p.cur()
	abstract := false
	if p.at(token.AbstractKw) {
		p.next()
		abstract = true
	}
	if _, ok := p.expect(token.ClassKw, "'class'"); !ok {
		p.synchronize()
		return &ast.EmptyStmt{S: start.Span}
	}
	name := p.parseIdent()
	if name == nil {
		p.synchronize()
		return &ast.EmptyStmt{S: start.Span}
	}
	var generics []*ast.GenericParam
	if p.at(token.Lt) {
		generics = p.parseGenericParams()
	}
	var extends ast.TypeNode
	if p.at(token.ExtendsKw) {
		p.next()
		extends = p.parseType()
	}
	var implements []ast.TypeNode
	if p.at(token.ImplementsKw) {
		p.next()
		for {
			implements = append(implements, p.parseType())
			if !p.at(token.Comma) {
				break
			}
			p.next()
		}
	}
	if _, ok := p.expect(token.LBrace, "'{'"); !ok {
		p.synchronize()
		return &ast.EmptyStmt{S: start.Span}
	}
	var members []*ast.ClassMember
	for !p.at(token.RBrace) && !p.at(token.EOF) {
		if p.at(token.Comma) || p.at(token.Semi) {
			p.next()
			continue
		}
		m := p.parseClassMember(declared)
		if m == nil {
			p.synchronizeClassMember()
			continue
		}
		members = append(members, m)
		if p.at(token.Comma) || p.at(token.Semi) {
			p.next()
		}
	}
	p.expect(token.RBrace, "'}'")
	sp := token.Span{Start: start.Span.Start, End: p.prevEnd(), File: p.file}
	return &ast.ClassDecl{Name: name, Generics: generics, Extends: extends, Implements: implements, Members: members, Exported: exported, Declared: declared, Abstract: abstract, IsLocal: isLocal, S: sp}
}

func (p *Parser) parseClassMember(declared bool) *ast.ClassMember {
	start := p.cur()
	vis := ast.Public
	visSeen := false
	isStatic, isReadonly, isAbstract := false, false, false
loop:
	for {
		switch {
		case p.at(token.PublicKw):
			if visSeen {
				p.errorf("TL2030", p.cur().Span, "duplicate visibility modifier")
			}
			p.next()
			vis, visSeen = ast.Public, true
		case p.at(token.PrivateKw):
			if visSeen {
				p.errorf("TL2030", p.cur().Span, "duplicate visibility modifier")
			}
			p.next()
			vis, visSeen = ast.Private, true
		case p.at(token.ProtectedKw):
			if visSeen {
				p.errorf("TL2030", p.cur().Span, "duplicate visibility modifier")
			}
			p.next()
			vis, visSeen = ast.Protected, true
		case p.at(token.StaticKw):
			if isStatic {
				p.errorf("TL2031", p.cur().Span, "duplicate 'static' modifier")
			}
			p.next()
			isStatic = true
		case p.at(token.ReadonlyKw):
			if isReadonly {
				p.errorf("TL2031", p.cur().Span, "duplicate 'readonly' modifier")
			}
			p.next()
			isReadonly = true
		case p.at(token.AbstractKw):
			if isAbstract {
				p.errorf("TL2031", p.cur().Span, "duplicate 'abstract' modifier")
			}
			p.next()
			isAbstract = true
		default:
			break loop
		}
	}
	name := p.parseIdentAllowSoft()
	if name == nil {
		return nil
	}
	isCtor := name.Name == "constructor"
	// Method or constructor: `name<T>(params)`.
	if p.at(token.LParen) || p.at(token.Lt) {
		var generics []*ast.GenericParam
		if p.at(token.Lt) {
			save := p.pos
			nd := len(p.diags)
			g := p.parseGenericParams()
			if g != nil && p.at(token.LParen) {
				generics = g
			} else {
				p.pos = save
				p.diags = p.diags[:nd]
			}
		}
		if _, ok := p.expect(token.LParen, "'('"); !ok {
			return nil
		}
		params, hasVA, vaName, vaTy := p.parseParams()
		p.expect(token.RParen, "')'")
		var retTy ast.TypeNode
		if !isCtor {
			if p.at(token.Arrow) {
				p.next()
				retTy = p.parseReturnType()
			} else if p.at(token.Colon) {
				p.next()
				retTy = p.parseType()
			}
		} else if p.at(token.Arrow) || p.at(token.Colon) {
			sp := p.cur().Span
			p.errorf("TL2032", sp, "constructor must not declare a return type")
			p.next()
			_ = p.parseType()
		}
		kind := ast.ClassMethod
		if isCtor {
			kind = ast.ClassConstructor
		}
		m := &ast.ClassMember{Kind: kind, Name: name, Visibility: vis, IsStatic: isStatic, IsReadonly: isReadonly, IsAbstract: isAbstract, Generics: generics, Params: params, HasVarArg: hasVA, VarArgName: vaName, VarArgType: vaTy, ReturnType: retTy, S: token.Span{Start: start.Span.Start, End: p.prevEnd(), File: p.file}}
		if declared || isAbstract {
			m.S = token.Span{Start: start.Span.Start, End: p.prevEnd(), File: p.file}
			return m
		}
		if p.at(token.Comma) || p.at(token.Semi) || p.at(token.RBrace) {
			p.errorf("TL2033", p.cur().Span, "method '%s' must have a body ending with 'end'", name.Name)
			m.Body = &ast.Block{S: token.Span{Start: p.prevEnd(), End: p.prevEnd(), File: p.file}}
			m.S = token.Span{Start: start.Span.Start, End: p.prevEnd(), File: p.file}
			return m
		}
		m.Body = p.parseBlock()
		p.expect(token.End, "'end'")
		m.S = token.Span{Start: start.Span.Start, End: p.prevEnd(), File: p.file}
		return m
	}
	// Field: `name[: T] [= init]`.
	if isCtor {
		p.errorf("TL2034", name.Span(), "'constructor' must be declared as a method")
		return nil
	}
	var fty ast.TypeNode
	if p.at(token.Colon) {
		p.next()
		fty = p.parseType()
	}
	var init ast.Expr
	if p.at(token.Assign) {
		if declared {
			p.errorf("TL2035", p.cur().Span, "field '%s' must not have an initializer in a 'declare' class", name.Name)
		}
		p.next()
		init = p.parseExpr()
	}
	if fty == nil && init == nil {
		p.errorf("TL2036", name.Span(), "field '%s' needs a type annotation or an initializer", name.Name)
		fty = &ast.NamedType{Name: "any", S: name.Span()}
	}
	if isAbstract {
		p.errorf("TL2037", start.Span, "only methods can be 'abstract'")
	}
	return &ast.ClassMember{Kind: ast.ClassField, Name: name, Visibility: vis, IsStatic: isStatic, IsReadonly: isReadonly, FieldType: fty, Init: init, S: token.Span{Start: start.Span.Start, End: p.prevEnd(), File: p.file}}
}

func (p *Parser) synchronizeClassMember() {
	for {
		k := p.cur().Kind
		if k == token.EOF || k == token.RBrace {
			return
		}
		if k == token.Comma || k == token.Semi {
			p.next()
			return
		}
		if k == token.Ident || isSoftKw(k) {
			return
		}
		p.next()
	}
}

func (p *Parser) parseInterfaceField() *ast.InterfaceField {
	start := p.cur()
	readonly := false
	if p.at(token.ReadonlyKw) {
		p.next()
		readonly = true
	}
	name := p.parseIdentAllowSoft()
	if name == nil {
		// Support method shorthand with string keys? No.
		return nil
	}
	optional := false
	if p.at(token.Question) {
		p.next()
		optional = true
	}
	// Method shorthand: name<T>(params) [: ret | -> ret]
	if p.at(token.LParen) || p.at(token.Lt) {
		// Generic methods: keep generics for explicit type arguments.
		var mgen []*ast.GenericParam
		if p.at(token.Lt) {
			save := p.pos
			nd := len(p.diags)
			mgen = p.parseGenericParams()
			if !p.at(token.LParen) {
				p.pos = save
				p.diags = p.diags[:nd]
				mgen = nil
			}
		}
		if _, ok := p.expect(token.LParen, "'('"); !ok {
			return nil
		}
		params, hasVA, vaTy := p.parseFuncTypeParams()
		p.expect(token.RParen, "')'")
		var rets []ast.TypeNode
		if p.at(token.Colon) {
			p.next()
			rets = []ast.TypeNode{p.parseType()}
		} else if p.at(token.Arrow) {
			p.next()
			rt := p.parseReturnType()
			rets = returnTypeToList(rt)
		}
		ft := &ast.FuncType{Params: params, HasVarArg: hasVA, VarArgType: vaTy, Returns: rets, Generics: mgen, S: token.Span{Start: start.Span.Start, End: p.prevEnd(), File: p.file}}
		return &ast.InterfaceField{Name: name, Type: ft, Optional: optional, Readonly: readonly, S: token.Span{Start: start.Span.Start, End: p.prevEnd(), File: p.file}}
	}
	p.expect(token.Colon, "':'")
	ty := p.parseType()
	return &ast.InterfaceField{Name: name, Type: ty, Optional: optional, Readonly: readonly, S: token.Span{Start: start.Span.Start, End: p.prevEnd(), File: p.file}}
}

func returnTypeToList(rt ast.TypeNode) []ast.TypeNode {
	if rt == nil {
		return nil
	}
	if tup, ok := rt.(*ast.TupleType); ok {
		return tup.Elems
	}
	if named, ok := rt.(*ast.NamedType); ok && named.Name == "void" && len(named.Args) == 0 {
		return nil
	}
	return []ast.TypeNode{rt}
}

// ---------- assignment / expr statements ----------

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
				p.errorf("TL2007", t.Span(), "invalid assignment target")
			}
		}
		return &ast.AssignStmt{Targets: targets, Values: values, S: token.Span{Start: start.Span.Start, End: p.prevEnd(), File: p.file}}
	}
	if len(targets) > 1 {
		p.errorf("TL2008", p.cur().Span, "expected '=' in assignment")
		p.synchronize()
		return &ast.EmptyStmt{S: start.Span}
	}
	// Must be a call.
	switch first.(type) {
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
		p.errorf("TL2009", first.Span(), "expected assignment or function call")
		return &ast.ExprStmt{E: first, S: first.Span()}
	}
}

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

func (p *Parser) parseIdent() *ast.Ident {
	if p.cur().Kind != token.Ident {
		p.errorf("TL2010", p.cur().Span, "expected identifier, got %s", p.cur().Kind)
		return nil
	}
	t := p.next()
	return &ast.Ident{Name: t.Literal, S: t.Span}
}

// Soft keywords can be used as identifiers in value positions (e.g. `type(x)` calls builtin).
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
	p.errorf("TL2010", p.cur().Span, "expected identifier, got %s", p.cur().Kind)
	return nil
}

// isTypeDeclStart reports whether `type/interface/enum/class` at current pos starts a declaration
// (as opposed to a value use like `type(x)`).
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

func (p *Parser) parseExpr() ast.Expr { return p.parseExprPrec(precNone + 1) }

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
			p.errorf("TL2011", op.Span, "expected expression after '%s'", op.Kind)
			return lhs
		}
		lhs = &ast.BinOpExpr{Op: op.Kind, L: lhs, R: rhs, S: token.Span{Start: lhs.Span().Start, End: rhs.Span().End, File: p.file}}
	}
	return lhs
}

func (p *Parser) parseUnary() ast.Expr {
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
				save := p.pos
				nd := len(p.diags)
				if tas, ok := p.tryParseTypeArgs(); ok && p.at(token.LParen) {
					targs = tas
				} else {
					p.pos = save
					p.diags = p.diags[:nd]
				}
			}
			args := p.parseCallArgs()
			if args == nil {
				p.errorf("TL2012", p.cur().Span, "expected method call arguments after ':method'")
				return e
			}
			e = &ast.MethodCallExpr{Obj: e, Method: m, TypeArgs: targs, Args: args, S: token.Span{Start: e.Span().Start, End: p.prevEnd(), File: p.file}}
		case p.at(token.Lt):
			// `f<T>(args)`: explicit generic arguments. Tentative: `<` types `>`
			// must be followed by `(`; otherwise this is a `<` comparison.
			save := p.pos
			nd := len(p.diags)
			tas, ok := p.tryParseTypeArgs()
			if !ok || !p.at(token.LParen) {
				p.pos = save
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

func (p *Parser) parseCallArgs() []ast.Expr {
	if p.at(token.LParen) {
		p.next()
		args := []ast.Expr{}
		if !p.at(token.RParen) {
			for {
				args = append(args, p.parseExpr())
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

func (p *Parser) parsePrimary() ast.Expr {
	c := p.cur()
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
			save := p.pos
			nd := len(p.diags)
			g := p.parseGenericParams()
			if g != nil && p.at(token.LParen) {
				fgen = g
			} else {
				p.pos = save
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
	p.errorf("TL2013", c.Span, "expected expression, got %s", c.Kind)
	return nil
}

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

// ---------- function bodies ----------

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
	body := p.parseBlock()
	p.expect(token.End, "'end'")
	return &ast.FuncLit{Params: params, HasVarArg: hasVA, VarArgName: vaName, VarArgType: vaTy, ReturnType: retTy, Body: body, IsMethod: isMethod, S: token.Span{Start: start.Span.Start, End: p.prevEnd(), File: p.file}}
}

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
		// Trailing `, ...` allowed.
		if p.at(token.DotDotDot) {
			continue
		}
	}
	return params, hasVA, vaName, vaTy
}

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

// ---------- types ----------

func (p *Parser) parseType() ast.TypeNode { return p.parseConditional() }

// parseConditional parses `Check extends Extends ? True : False`
// (right-associative on both branches, like TS). The `?` collides with
// the Optional postfix `T?`; isConditionalQ disambiguates.
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

// isConditionalQ reports whether the current `?` opens conditional branches
// (`? True :`) rather than forming Optional `T?`. Speculative and side
// effect free: restores position and diagnostics either way.
func (p *Parser) isConditionalQ() bool {
	if !p.at(token.Question) {
		return false
	}
	savePos, saveDiags := p.pos, len(p.diags)
	saveGt := p.pendingGt
	p.next() // ?
	branch := p.parseType()
	ok := branch != nil && p.at(token.Colon)
	p.pos, p.pendingGt = savePos, saveGt
	p.diags = p.diags[:saveDiags]
	return ok
}

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

func (p *Parser) parseTypePostfix() ast.TypeNode {
	t := p.parsePrimaryType()
	if t == nil {
		return nil
	}
	for {
		if p.at(token.Question) {
			// `? True :` belongs to an enclosing conditional, not Optional.
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
			// Indexed access `T[K]` (empty brackets stay Array above).
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

func (p *Parser) parsePrimaryType() ast.TypeNode {
	c := p.cur()
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
		// `...T` rest marker (tuple patterns, vararg positions).
		// Takes a full postfix type so `...unknown[]` means rest-of-unknown.
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
		p.errorf("TL2014", c.Span, "expected type, got %s", c.Kind)
		// Synthesize any to continue.
		p.next()
		return &ast.NamedType{Name: "any", S: c.Span}
	}
}

func (p *Parser) parseNamedType() ast.TypeNode {
	first := p.next()
	name := first.Literal
	// Qualified a.b.C
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

func (p *Parser) parseParenOrFuncType() ast.TypeNode {
	start := p.next() // (
	// Empty () => void-ish unit.
	if p.at(token.RParen) {
		p.next()
		if p.at(token.Arrow) {
			p.next()
			ret := p.parseReturnType()
			return &ast.FuncType{Returns: returnTypeToList(ret), S: token.Span{Start: start.Span.Start, End: p.prevEnd(), File: p.file}}
		}
		return &ast.ParenType{Elem: &ast.NamedType{Name: "void", S: start.Span}, S: token.Span{Start: start.Span.Start, End: p.prevEnd(), File: p.file}}
	}
	// Tentatively parse as func params.
	save := p.pos
	ndiags := len(p.diags)
	params, hasVA, vaTy, ok := p.tryParseFuncTypeParams()
	if ok && p.at(token.RParen) {
		p.next()
		if p.at(token.Arrow) {
			p.next()
			ret := p.parseReturnType()
			return &ast.FuncType{Params: params, HasVarArg: hasVA, VarArgType: vaTy, Returns: returnTypeToList(ret), S: token.Span{Start: start.Span.Start, End: p.prevEnd(), File: p.file}}
		}
		// Not a func type; fall through to plain types. If params were all unnamed, we can reuse.
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
	// Backtrack and parse as plain type list.
	p.pos = save
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
		// `(A, B) -> R` with unnamed params.
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

func (p *Parser) tryParseFuncTypeParams() ([]*ast.FuncTypeParam, bool, ast.TypeNode, bool) {
	var params []*ast.FuncTypeParam
	hasVA := false
	var vaTy ast.TypeNode
	for !p.at(token.RParen) && !p.at(token.EOF) {
		if p.at(token.DotDotDot) {
			p.next()
			hasVA = true
			// ...name? : T  or ...T
			if p.at(token.Ident) || isSoftKw(p.cur().Kind) {
				// Could be `...T` (unnamed vararg of type T) or `...rest: T`.
				// Lookahead: if next is Colon/Comma/RParen, it's a name.
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
		// name: T  |  name?: T  |  T (unnamed)
		if p.at(token.Ident) || isSoftKw(p.cur().Kind) {
			// Lookahead for Colon or Question+Colon => named.
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
				// Unnamed: parse a full type. But an Ident alone could be either a name without colon (invalid) or a type.
				// Treat as type.
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

func tySpan(name *ast.Ident, ty ast.TypeNode) token.Span {
	if ty == nil {
		return name.Span()
	}
	return token.Span{Start: name.Span().Start, End: ty.Span().End, File: name.Span().File}
}

func (p *Parser) parseFuncTypeParams() ([]*ast.FuncTypeParam, bool, ast.TypeNode) {
	params, hasVA, vaTy, _ := p.tryParseFuncTypeParams()
	return params, hasVA, vaTy
}

func (p *Parser) parseTableType() ast.TypeNode {
	start := p.next() // {
	// Mapped type: `{ [K in Keys]?: V }` (optional readonly prefix).
	// Committed once `[Ident in` is seen; errors after that are real.
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

// isMappedStart peeks `[K in` (optional `+`/`-` + readonly prefix)
// without consuming.
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
	// Indexer [K]: V
	if p.at(token.LBrack) {
		p.next()
		k := p.parseType()
		p.expect(token.RBrack, "']'")
		p.expect(token.Colon, "':'")
		v := p.parseType()
		return &ast.TableTypeField{IsIndexer: true, IndexKey: k, IndexVal: v, S: token.Span{Start: start.Span.Start, End: p.prevEnd(), File: p.file}}
	}
	// Method shorthand or property.
	if (p.at(token.Ident) || isSoftKw(p.cur().Kind)) && (p.peek(1).Kind == token.LParen || p.peek(1).Kind == token.Lt) {
		name := p.next()
		lit := name.Literal
		if lit == "" {
			lit = name.Kind.String()
		}
		// Optional generics on methods: keep for explicit type arguments.
		var mgen []*ast.GenericParam
		if p.at(token.Lt) {
			save := p.pos
			nd := len(p.diags)
			mgen = p.parseGenericParams()
			if !p.at(token.LParen) {
				p.pos = save
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

// tryParseTypeArgs parses `<T, ...>` explicit generic arguments.
// Returns ok=false without consuming on failure (caller restores).
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

// ---------- recovery ----------

func (p *Parser) synchronize() {
	for {
		k := p.cur().Kind
		if k == token.EOF {
			return
		}
		// Statement boundaries.
		switch k {
		case token.End, token.Else, token.Elseif, token.Until:
			return
		case token.Semi:
			p.next()
			return
		}
		// Next statement starters.
		switch k {
		case token.Local, token.Function, token.If, token.While, token.Repeat,
			token.For, token.Do, token.Return, token.Break, token.Goto,
			token.TypeKw, token.InterfaceKw, token.EnumKw, token.ClassKw,
			token.AbstractKw, token.ExportKw, token.DeclareKw, token.MacroKw:
			return
		}
		p.next()
	}
}

func (p *Parser) synchronizeTable() {
	depth := 0
	for {
		k := p.cur().Kind
		if k == token.EOF {
			return
		}
		if k == token.LBrace {
			depth++
		}
		if k == token.RBrace {
			if depth == 0 {
				return
			}
			depth--
		}
		if depth == 0 && (k == token.Comma || k == token.Semi) {
			p.next()
			return
		}
		p.next()
	}
}

func (p *Parser) synchronizeInterface() {
	for {
		k := p.cur().Kind
		if k == token.EOF || k == token.RBrace {
			return
		}
		if k == token.Comma || k == token.Semi {
			p.next()
			return
		}
		// If next looks like `ident [:? ( ...` start of next field, stop.
		if k == token.Ident {
			return
		}
		p.next()
	}
}

func (p *Parser) synchronizeTableType() {
	p.synchronizeInterface()
}
