package parser

import (
	"github.com/typelua/typelua/internal/ast"
	"github.com/typelua/typelua/internal/diag"
	"github.com/typelua/typelua/internal/token"
)

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
		p.errorf(diag.ParseExpectedIdentifier, p.cur().Span, "expected identifier, got %s", p.cur().Kind)
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
	p.errorf(diag.ParseExpectedIdentifier, p.cur().Span, "expected identifier, got %s", p.cur().Kind)
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
			p.errorf(diag.ParseExpectedOperand, op.Span, "expected expression after '%s'", op.Kind)
			return lhs
		}
		lhs = &ast.BinOpExpr{Op: op.Kind, L: lhs, R: rhs, S: token.Span{Start: lhs.Span().Start, End: rhs.Span().End, File: p.file}}
	}
	return lhs
}

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
