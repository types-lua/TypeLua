package parser

import (
	"github.com/typelua/typelua/internal/token"
)

// ---------- recovery ----------

func (p *Parser) synchronize() {
	for {
		k := p.cur().Kind
		if k == token.EOF {
			return
		}
		// Statement boundaries. RBrace joins End here so an error inside
		// a `{ ... }` function body resyncs at the body's own closer
		// instead of skipping it and cascading outward; every loop that
		// can reach synchronize (block/table/class/select members)
		// already terminates on RBrace.
		switch k {
		case token.End, token.Else, token.Elseif, token.Until, token.RBrace:
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
