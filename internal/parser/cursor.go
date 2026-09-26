package parser

import (
	"github.com/typelua/typelua/internal/diag"
	"github.com/typelua/typelua/internal/token"
)

// ---------- cursor helpers ----------

// savedCursor captures the full token-stream position for speculative
// parses: the index plus any pending split remainders from consumeGt.
// The pending queue is copied so later splits cannot alias the snapshot.
type savedCursor struct {
	pos     int
	pending []token.Token
}

func (p *Parser) saveCursor() savedCursor {
	return savedCursor{pos: p.pos, pending: append([]token.Token(nil), p.pending...)}
}

func (p *Parser) restoreCursor(s savedCursor) {
	p.pos = s.pos
	p.pending = s.pending
}

func (p *Parser) cur() token.Token {
	if len(p.pending) > 0 {
		return p.pending[0]
	}
	if p.pos < len(p.toks) {
		return p.toks[p.pos]
	}
	return token.Token{Kind: token.EOF}
}

func (p *Parser) peek(n int) token.Token {
	if n < len(p.pending) {
		return p.pending[n]
	}
	idx := p.pos + n - len(p.pending)
	if idx < len(p.toks) {
		return p.toks[idx]
	}
	return token.Token{Kind: token.EOF}
}

func (p *Parser) at(k token.Kind) bool { return p.cur().Kind == k }

func (p *Parser) next() token.Token {
	if len(p.pending) > 0 {
		t := p.pending[0]
		p.pending = p.pending[1:]
		return t
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
//
// The token slice is never mutated: the remainder of a split is queued in
// p.pending (at most two tokens: `>` then `=` for `>=`), so speculative
// parses restore exactly via saveCursor/restoreCursor. Synthesized tokens
// reuse the original token's span, matching the old slice-splicing behavior.
func (p *Parser) consumeGt() bool {
	if len(p.pending) > 0 {
		if p.pending[0].Kind == token.Gt {
			p.pending = p.pending[1:]
			return true
		}
		c := p.pending[0]
		p.errorf(diag.ParseExpectedGt, c.Span, "expected '>', got %s", c.Kind)
		return false
	}
	if p.pos >= len(p.toks) {
		p.errorf(diag.ParseExpectedGt, p.cur().Span, "expected '>', got %s", p.cur().Kind)
		return false
	}
	c := p.toks[p.pos]
	switch c.Kind {
	case token.Gt:
		p.pos++
		return true
	case token.ShiftR:
		// >> => consume one '>', queue the other.
		p.pos++
		p.pending = append(p.pending, token.Token{Kind: token.Gt, Literal: ">", Span: c.Span})
		return true
	case token.Ge:
		// >= inside generics => '>' then '=' (e.g. `F<A>=v` lexed together).
		// Like the old slice-splicing code, both parts stay pending: the
		// caller sees '>' next (outer levels eat it), then '='.
		p.pos++
		p.pending = append(p.pending,
			token.Token{Kind: token.Gt, Literal: ">", Span: c.Span},
			token.Token{Kind: token.Assign, Literal: "=", Span: c.Span})
		return true
	}
	p.errorf(diag.ParseExpectedGt, c.Span, "expected '>', got %s", c.Kind)
	return false
}

func (p *Parser) atGt() bool {
	if len(p.pending) > 0 {
		return p.pending[0].Kind == token.Gt
	}
	if p.pos >= len(p.toks) {
		return false
	}
	k := p.toks[p.pos].Kind
	return k == token.Gt || k == token.ShiftR || k == token.Ge
}
