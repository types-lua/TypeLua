package parser

import (
	"github.com/typelua/typelua/internal/diag"
	"github.com/typelua/typelua/internal/token"
)

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
	p.errorf(diag.ParseExpectedToken, p.cur().Span, "expected %s, got %s", what, p.cur().Kind)
	return p.cur(), false
}

func (p *Parser) errorf(code diag.Code, sp token.Span, format string, args ...any) {
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
	p.errorf(diag.ParseExpectedGt, c.Span, "expected '>', got %s", c.Kind)
	return false
}

func (p *Parser) atGt() bool {
	k := p.cur().Kind
	return k == token.Gt || k == token.ShiftR || k == token.Ge
}
