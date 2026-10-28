package parser

import (
	"github.com/typelua/typelua/internal/diag"
	"github.com/typelua/typelua/internal/token"
)

// ---------- cursor helpers ----------

// savedCursor 存试探快照：pos 索引+pending 余量（深拷防别名），供 save/restore 零副作用悔棋。
// 布局：toks 只读流（pos 指针）+pending 优先队列（如 `>=` 拆 `>`+`=`），cur/peek/next 皆 pending 优先。
type savedCursor struct {
	pos     int
	pending []token.Token
}

// saveCursor 快照当前 pos 与 pending（pending 复制，避免别名）。
//
// 设计者视角：它是试探解析的“存档点”，好比下棋前先拍照——泛型 `<T>` 与比较
// `a < b` 在看到 '(' 前无法区分，只能先走一步再悔棋。障碍是 pending 为切片，
// 若只拷 header 不拷底层数组，后续 consumeGt 的 append 会污染快照；此处
// append 到 nil 即深拷，杜绝别名。取舍：每次试探多一次小分配，换零副作用的正确性。
// 不变量：快照与恢复必须成对，中间的 diags 长度也要同步截断（调用方负责）。
// 排查现场：若试探后 Token 错位，先查是否只恢复了 pos 而漏了 pending。
func (p *Parser) saveCursor() savedCursor {
	return savedCursor{pos: p.pos, pending: append([]token.Token(nil), p.pending...)}
}

// restoreCursor 回退到快照，试探解析失败时零副作用恢复。
//
// 设计者视角：它是存档点的“读档”——pos 与 pending 整体回写，Token 流仿佛从未前进一步。
// 候选曾是记录操作日志再逆向回放，但分支太多易漏；取舍是整体覆盖，简单粗暴却无死角。
// 不变量：恢复后 cur()/peek() 与快照时刻逐 Token 一致。排查现场：若恢复后诊断残留，
// 检查调用方是否忘了 `p.diags = p.diags[:nd]`（本函数只管游标不管诊断）。
func (p *Parser) restoreCursor(s savedCursor) {
	p.pos = s.pos
	p.pending = s.pending
}

// cur 返回当前 Token（pending 优先），永不越界：末尾之后统一返回 EOF。
//
// 设计者视角：cur 是全 parser 的“眼睛”——所有 at()/expect() 都经它看料，
// 它把“pending 队列 + toks 流 + EOF 哨兵”三段拼成一条无限流，调用方无需判界。
// 不变量：返回值只读，绝不持有可写引用。排查现场：若死循环，先看是否有人绕过
// cur 直接读 toks[pos] 而漏了 pending。
func (p *Parser) cur() token.Token {
	if len(p.pending) > 0 {
		return p.pending[0]
	}
	if p.pos < len(p.toks) {
		return p.toks[p.pos]
	}
	return token.Token{Kind: token.EOF}
}

// peek 向前看第 n 个 Token（n=0 等价 cur），用于软关键字与泛型的两三步前瞻。
//
// 设计者视角：peek 是“望远镜”——`async function`、`spawn f(`、`import Name from`
// 都靠它不消费即判定，避免误把普通标识符吃掉。障碍是 pending 与 toks 分段：
// n 落在 pending 内直接取，落在后段要减去 pending 长度再映射到 toks。
// 公式 idx = pos + n - len(pending) 即两段拼接的下标换算。不变量：peek 永不推进游标。
// 排查现场：若软关键字误判，先打印 cur/peek(1)/peek(2) 的 Kind 与 sameLine 再断。
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

// at 报告当前 Token 是否为指定 Kind，是最常用的单步判定。
//
// 设计者视角：at 是“门卫查牌”——`at(token.End)` 即问“到块尾了吗”，比手写
// `p.cur().Kind ==` 更易读，也收敛了 pending 优先的细节。不变量：无副作用，可任意重入。
func (p *Parser) at(k token.Kind) bool { return p.cur().Kind == k }

// next 消费并返回当前 Token（pending 优先），是游标唯一的推进点。
//
// 设计者视角：next 是“ конвей带上的取料手”——先吃手心的 pending（拆分余量），
// 再吃料架的 toks[pos++]。障碍是 EOF 处不得再推进（pos 钳住），否则 prevEnd 会漂移。
// 不变量：toks 内容永不修改，推进只动 pos/pending。排查现场：若 Span 起止错乱，
// 先看是否多/少 next 了一次，再对 spanFrom/prevEnd。
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

// expect 期望当前为指定 Kind，命中则消费，否则报 ParseExpectedToken 并原地不动。
//
// 设计者视角：expect 是“报关检查”——`expect(token.End)` 通不过就地报关（诊断锚在 cur），
// 但不强行吞料，让上层恢复逻辑决定跳到哪。障碍是缺符号时若硬吞会连锁错位；
// 取舍是返回 (Token, bool)，调用方按 ok 分流（缺 ')' 时多走同步恢复）。
// 不变量：失败时游标不动、诊断恰增一条。排查现场：若缺括号后满屏红，先看是否在
// expect 失败后仍继续按成功路径解析。
func (p *Parser) expect(k token.Kind, what string) (token.Token, bool) {
	if p.at(k) {
		return p.next(), true
	}
	p.errorf(diag.ParseExpectedToken, p.cur().Span, "expected %s, got %s", what, p.cur().Kind)
	return p.cur(), false
}

// errorf 向 diags 追加一条带码诊断，是全 parser 唯一的报错出口。
//
// 设计者视角：errorf 是“记账本”——code（如 ParseExpectedToken）供 LSP/测试按码断言，
// Span 供编辑器画线，format 供人阅读。取舍是不立刻中断，而是记账后继续，让单错
// 不掩盖后文。不变量：只追加不截断，截断只由试探回退的调用方执行。
// 排查现场：若某错“时有时无”，先查试探路径是否正确截断了投机产生的诊断。
func (p *Parser) errorf(code diag.Code, sp token.Span, format string, args ...any) {
	p.diags = append(p.diags, diag.Errorf(code, sp, format, args...))
}

// spanFrom 以 start 为起点、以上次消费末尾为终点拼接 Span。
//
// 设计者视角：spanFrom 是“量尺收尾”——解析函数入口记 start，出口以 toks[pos-1].End
// 收边，圈出整条语句/表达式。障碍是中间可能经历 pending 拆分与回退，末 Token 未必
// 在 pos-1；此处取 toks[pos-1] 即“料架上最后吃掉的那件”，pending 余量不计入长度。
// 不变量：Start 永取 start，File 永为 p.file。排查现场：若节点 Span 尾部多出一个 '>'，
// 先看 consumeGt 的合成 Span 是否复用了原 Token 的宽区间。
func (p *Parser) spanFrom(start token.Token) token.Span {
	end := p.cur().Span
	if p.pos > 0 {
		end = p.toks[p.pos-1].Span
		// 取料架上最后吃掉件的尾，如连吃三词即第三词尾。
	}
	return token.Span{Start: start.Span.Start, End: end.End, File: p.file}
}

// spanBetween 以 start/end 两 Token 的起止拼接 Span，用于首尾都已知的节点。
//
// 设计者视角：它是 spanFrom 的“双锚版”——首尾 Token 都攥在手里时（如 FuncName 的
// 首 Ident 与尾 Ident）直接量两头，比“当前位置收边”更精确，不受中间同步跳料影响。
// 不变量：File 取 p.file 而非任一 Token 的 File，保证多文件拼接时归一。
// 排查现场：若 Span 起止跨行过大，先确认 end 是否取成了同步恢复后的 cur。
func (p *Parser) spanBetween(start token.Token, end token.Token) token.Span {
	return token.Span{Start: start.Span.Start, End: end.Span.End, File: p.file}
}

// consumeGt 是泛型拆弹器：`>>` 拆两 `>`，`>=` 拆 `>`+`=`，余量进 pending，合成 Token 复用原 Span。
// 焊成一颗 Token，但 `F<A>>` / `F<A>=v` 在类型语境里必须是两个 '>' 或 '>'+'='。
// 障碍是拆了就回不去：老代码曾直接改 toks 切片，快照恢复直接失效；现把余量排进
// pending（`>>` 余一个 '>'，`>=` 余 '>'+'='），原流只动 pos，悔棋只需倒 pending。
// 取舍：合成 Token 复用原 Span（与旧切片拼接行为一致），定位略宽但恢复语义简单。
// 不变量：成功必消费恰一个逻辑 '>'，失败必报 ParseExpectedGt 且不动游标（除拆分提交）。
// 排查现场：若 `a >= b` 的比较被误吃成泛型尾，先看调用方是否在 tryParseTypeArgs
// 成功且后接 '(' 才提交，否则必须 restore。
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
		// 泛型内 `>=` 拆 `>`+`=`，如 `F<A>=v` 余 `=` 留给赋值。
		p.pos++
		p.pending = append(p.pending,
			token.Token{Kind: token.Gt, Literal: ">", Span: c.Span},
			token.Token{Kind: token.Assign, Literal: "=", Span: c.Span})
		return true
	}
	p.errorf(diag.ParseExpectedGt, c.Span, "expected '>', got %s", c.Kind)
	return false
}

// atGt 报告当前位置是否可视为 '>'（含未拆分的 >> / >=），不消费不报错。
//
// 设计者视角：它是 consumeGt 的“无损探头”——parseGenericParams 的循环条件靠它
// 判断“还有没有右尖括号”，pending 队首与 toks 当前位都要看。障碍是 Ge/ShiftR
// 在值语境是比较/移位，在类型语境是右括号；此处只答“长得像不像”，语义由调用方
// 试探后决定。取舍：EOF 直接 false，避免 cur 的零值 Span 误触发。
// 不变量：纯只读，可任意重入。排查现场：若泛型参数提前收尾，先看 atGt 是否把
// 独立的 `>=` 误判（应由 consumeGt 拆分后逐个消费）。
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
