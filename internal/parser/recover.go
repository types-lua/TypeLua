package parser

import (
	"github.com/typelua/typelua/internal/token"
)

// ---------- recovery ----------

// 错误恢复：synchronize 吃残骸到语句边界再开工，单错不掩盖后文；表/接口/类各有字段级恢复，不越块边界。

// synchronize 推进到下一个语句边界/起始，实现单错不掩盖后文。
//
// 设计者视角：synchronize 是语法错后的“扫雷复位”——错了一句不能全卷作废，
// 要吃掉残骸直到下一句能认出的路标（end/else/until/`}`/`;`/下条语句起始）再开工。
// 障碍是 Lua 的 end 既是块尾又是同步点：若越过它会把外层块一起吞掉；取舍是见
// End/Else/Elseif/Until/RBrace 即停（不消费），见 ';' 则吃掉再停，让 `{...}` 函数体
// 的错收敛在自家 `}` 内。候选曾是 panic 中断整文件，但 LSP 要“错一句补全下句”，
// 只能选同步恢复。不变量：永不越过 EOF，永不消费同步 Token 本身（除 ';'）。
// 排查现场：若一错之后满屏“expected end”，先看同步集是否漏了 RBrace（花括号体必备）。
func (p *Parser) synchronize() {
	for {
		k := p.cur().Kind
		if k == token.EOF {
			return
		}
		// 语句边界：`{…}` 体错收敛自家 `}`，不级联外层。
		switch k {
		case token.End, token.Else, token.Elseif, token.Until, token.RBrace:
			return
		case token.Semi:
			p.next()
			return
		}
		// 下条语句起始如 local/function/if 即停。
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
//
// 设计者视角：表字面量 `{a=1, ???, b=2}` 错一个字段不能丢整表——本函数是表的
// “字段级扫雷器”，沿逗号/分号切到下一字段。障碍是表可嵌套 `{ { ??? } }`：
// depth 计数让内层 `,` 不误当外层分隔，只有 depth==0 的逗号才停。不变量：
// 停在分隔符之后（已消费）或 EOF/外层 `}` 之前（不消费 `}`，留给 parseTable 收尾）。
// 排查现场：若表错后外层 `}` 被吞，先查 LBrace/RBrace 的 depth 配对是否漏增减。
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
//
// 设计者视角：它是接口表的“换行重对齐”——`{ name: string ??? age: number }` 里
// 坏字段后，要停在下一个像字段起点的位置（Ident 即停），而非无脑吃到 `}`。
// 障碍是类型与表达式长得像，无法精确判定边界；取舍是“见 Ident 即停”的宽松策略，
// 宁可把半截残骸留给 parseInterfaceField 再报一次，也不越界吞掉好字段。
// 不变量：EOF/RBrace 前必停且不消费 `}`。排查现场：若接口错后字段连锁丢失，
// 先看是否把关键字字段（如 `readonly`）误判为非 Ident 起点。
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
		// 下字段起点 `ident` 即停，如 `age: number`。
		if k == token.Ident {
			return
		}
		p.next()
	}
}

// synchronizeTableType 复用 synchronizeInterface（类型表与接口表恢复策略一致）。
//
// 设计者视角：类型表 `{ [string]: T }` 与接口表共用一套“字段级恢复”，好比分店共用
// 一套消防预案——分隔符与字段起点相同，单列一函数只为语义锚点（调用处一眼看出在
// 类型语境）。取舍是薄转发而非复制，保持两处策略永不分叉。不变量：与
// synchronizeInterface 的停机点完全一致。排查现场：若类型表恢复行为异常，
// 直接查 synchronizeInterface 本体。
func (p *Parser) synchronizeTableType() {
	p.synchronizeInterface()
}
