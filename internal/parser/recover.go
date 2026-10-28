package parser

import (
	"github.com/typelua/typelua/internal/token"
)

// ---------- recovery ----------

// 错误恢复策略（同步集合）：
//
//	Parser 遇到语法错误后不立刻停，而是 synchronize() 吃掉 Token 直到
//	“语句边界 / 下一条语句起始”，然后继续解析，保证单错不掩盖后文。
//
//	同步点（任一即停）：
//	  End / Else / Elseif / Until / RBrace / ;(吃掉)
//	  语句起始：local/function/if/while/repeat/for/do/return/break/goto/
//	            type/interface/enum/class/abstract/export/declare/macro
//
//	布局（块内恢复）：
//	  parseBlock 循环 ──► parseStmt ──► 出错 ──► synchronize() ──► 回到循环
//	                                    │
//	                                    └── RBrace/End/Else/... 作哨兵，不越过块边界
//
//	表/接口/类成员各有专用 synchronizeTable/Interface/ClassMember，
//	避免把内层错误扩散到外层块。
//
// 为什么 RBrace 加入同步集：`{ ... }` 函数体出错时在体自身的 `}` 停下，
// 不跳过 `}` 而级联到外层；所有可达 synchronize 的循环均已以 RBrace 终止。

// synchronize 推进到下一个语句边界/起始，实现单错不掩盖后文。
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

// synchronizeTable 在表字面量内推进到逗号/分号，恢复字段级错误。
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

// synchronizeInterface 在 interface/type table 内推进到下一个字段，恢复字段级错误。
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

// synchronizeTableType 复用 synchronizeInterface（类型表与接口表恢复策略一致）。
func (p *Parser) synchronizeTableType() {
	p.synchronizeInterface()
}
