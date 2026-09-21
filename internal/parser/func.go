package parser

import (
	"github.com/typelua/typelua/internal/ast"
	"github.com/typelua/typelua/internal/token"
)

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
