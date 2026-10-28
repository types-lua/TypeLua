package parser

import (
	"github.com/typelua/typelua/internal/diag"
	"github.com/typelua/typelua/internal/token"
)

// ---------- cursor helpers ----------

// savedCursor 捕获试探解析的完整 Token 流位置：索引 pos 加上 consumeGt
// 拆分产生的 pending 余量。pending 队列被复制，避免后续拆分别名快照。
//
// 游标物理布局（单槽 + 待消费队列）：
//
//	toks []token.Token  (只读 Token 流)
//	┌────┬────┬────┬────┬────┐
//	│ 0  │ 1  │ 2  │... │ n-1│
//	└────┴────┴────┴────┴────┘
//	           ▲
//	           │ pos
//	pending []token.Token  (consumeGt 拆分余量，优先消费)
//	┌────┬────┐
//	│ >  │ =  │  (例如 >= 拆成 '>' '=')
//	└────┴────┘
//	   ▲
//	   │ next() 先吃这里
//
// cur()/peek()/next() 都“pending 优先、toks 兜底”；saveCursor 复制 pending，
// restoreCursor 整体回退，实现试探解析零副作用。
type savedCursor struct {
	pos     int
	pending []token.Token
}

// saveCursor 快照当前 pos 与 pending（pending 复制，避免别名）。
func (p *Parser) saveCursor() savedCursor {
	return savedCursor{pos: p.pos, pending: append([]token.Token(nil), p.pending...)}
}

// restoreCursor 回退到快照，试探解析失败时零副作用恢复。
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

// consumeGt 消费单个 '>'，必要时拆分 >> / >=。
// Token 流从不原地修改：拆分余量追加到 p.pending（最多两个：'>' 与 '='），
// 试探解析可通过 saveCursor/restoreCursor 精确恢复。合成 Token 复用原 Token 的 Span。
//
// token 切片从不被修改：拆分的余下部分排队进入 p.pending（`>=` 最多两个
// token：`>` 然后 `=`），因此推测式解析可通过 saveCursor/restoreCursor 精确恢复。
// 合成 token
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
