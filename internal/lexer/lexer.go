package lexer

import (
	"strings"
	"unicode/utf8"

	"github.com/typelua/typelua/internal/diag"
	"github.com/typelua/typelua/internal/token"
)

// Lexer turns TypeLua source into tokens.
//
// 设计者视角：Lexer 是编译流水线的“质检分拣员”——好比传送带入口按形状切块、
// 按种类贴面单（Token），坏字符记一笔诊断后跳过，永不停机。它是游标式单遍扫描：
// src 是整卷布，off 是剪刀位置，line/col 是尺子，diags 是次品登记本。
// 障碍是任意输入都不能 panic（空文件、孤立 0xFF、未闭合长串）：所有坏输入都
// 转为 diag 并推进游标，保证 Lex 对任意字节串终止。取舍是无状态、无回溯、
// 无上下文——软关键字一律贴“疑似”标签，上下文判决留给 parser，换词法零分支。
// 不变量：Span 半开、Offset 单调不减；行列与偏移同时携带。
// 排查现场：若 token 行列漂移，先查 advance/advanceBlock 的列计数，再查 skipSpace
// 是否漏了 \r；若死循环，先查坏字符分支是否至少推进一 rune。
type Lexer struct {
	src      string
	file     string
	off      int
	line     int
	col      int
	diags    []diag.Diagnostic
	finished bool
}

// New 创建一个针对 src 的词法分析器，文件名为 filename；用于流式取 token 与收集诊断。
//
// 设计者视角：New 是“开新布卷”——好比给分拣员发一卷布并告诉他卷号（filename），
// 卷号此后会抄到每个 Token 的 Span.File 上，多文件诊断自带地址。障碍是空串与 BOM：
// 此处不做预处理，line/col 直接从 1/1 起，BOM 若存在会走无效字符分支记一笔后跳过，
// 保持构造零失败。取舍是 src 不拷贝、直接引用调用方字符串，Token.Literal 切片共享
// 底层，换零分配；调用方在 Lex 期间不得改 src。不变量：返回的 Lexer 永不为 nil，
// 起始 pos 为 1:1、off 为 0。排查现场：若文件名串了，先看 New 的 filename 是否传错，
// 次看 Lex(shebang 跳过) 是否动了 off 却没同步行列。
func New(filename, src string) *Lexer {
	return &Lexer{src: src, file: filename, line: 1, col: 1}
}

// Diags 返回扫描过程中累积的词法诊断切片，供 parser/compiler 聚合报告。
//
// 设计者视角：它是“次品登记本的只读窗口”——parser.ParseFile 先 Lex 再把 diags 与
// 语法诊断拼成一份报告，一次编译报全错。障碍是词法永不停机：坏字符只记账不停线，
// 故调用方必须在取 toks 的同时取 diags，缺一不可。取舍是直接返回底层切片（不拷贝），
// 换零分配；约定调用方只读，若要续写请先拷贝。不变量：返回 nil 表示零诊断，
// 而非空分配；顺序与源码出现顺序一致。排查现场：若坏字符“报了没显示”，先查上层
// 是否只看了 parser 诊断而丢了 lexer 这一本。
func (l *Lexer) Diags() []diag.Diagnostic { return l.diags }

// pos 抓拍当前游标坐标，是所有 Span.Start/End 的唯一尺子。
//
// 设计者视角：它是“快门”——next()/lexNumber()/lexString 入口先咔嚓一张（start），
// 出口再咔嚓一张（end），两张拼成半开 Span。取舍是每次现算 struct 而非缓存，
// 换坐标永远新鲜。不变量：返回的 Offset==l.off、Line/Column 与推进同步。
// 排查现场：若 Span 起止相同却非 EOF，先看是否在推进前连拍了两次 pos。
func (l *Lexer) pos() token.Pos {
	return token.Pos{Offset: l.off, Line: l.line, Column: l.col}
}

// eof 报告剪刀是否已到布尾，是主循环唯一的停机条件。
//
// 设计者视角：它是传送带的“到头传感器”——next() 开头先问它，到头即发 EOF Token，
// Lex 循环见 EOF 收线。取舍是用 off>=len(src) 而非留哨兵字节，换零额外内存。
// 不变量：eof 为真时 peek 一律回 0、cur 不得再读 src[off]。
// 排查现场：若 Lex 停不下来，先查无效字符分支是否忘了 advance 而原地打转。
func (l *Lexer) eof() bool { return l.off >= len(l.src) }

// peek 向前看第 n 个字节，越界回 0，是最长匹配的望远镜。
//
// 设计者视角：好比切 .../..//.、==/=>/=、>>/<< 全靠多看一两字节定夺，
// peek 让 next() 不消费即判定。障碍是尾部不足 n 字节时直接下标会 panic，
// 故越界回 0（NUL 永不与 Lua 语法字节相撞，天然哨兵）。不变量：纯只读，
// 永不推进 off/line/col。排查现场：若行尾操作符误判，先确认 peek 的 n 是否
// 从 1 数起（peek(1) 是下一字节，不是当前）。
func (l *Lexer) peek(n int) byte {
	if l.off+n >= len(l.src) {
		return 0
	}
	return l.src[l.off+n]
}

// advance 推进 n 字节并同步行列，是单 Token 扫描的步进器。
//
// 设计者视角：它是“剪刀走纸”——每走一字节看是换行（line++、col=1）还是普通
// 字符（col++）。障碍是中文等多字节 rune：若按字节加列，列号会虚胖；此处只给
// 前导字节（c<0x80 或 c>=0xC0）加列，延续字节不计数，让 Column 恒为 rune 列。
// 取舍是逐字节分支，单 Token 长度短，开销可忽略；大段跳跃另走 advanceBlock 快道。
// 不变量：推进后 off 单调增，line/col 与 off 同步；超尾调用安全早退。
// 排查现场：若中文行列号偏大，先看是否有人用 len(src) 当列宽，再看本处的前导字节判定。
func (l *Lexer) advance(n int) {
	for i := 0; i < n; i++ {
		if l.off >= len(l.src) {
			return
		}
		c := l.src[l.off]
		l.off++
		if c == '\n' {
			l.line++
			l.col = 1
		} else {
			// For multi-byte runes, advance column by 1 per rune, not per byte.
			// We step byte-by-byte; only count leading bytes.
			if c < 0x80 || c >= 0xC0 {
				l.col++
			}
		}
	}
}

// advanceBlock moves forward n bytes with line/column updates identical to
// n advance(1) calls, but in two linear scans instead of per-byte branches.
// Used for skipping already-located regions (long strings and comments);
// token-level scanning still uses advance for single tokens.
//
// 设计者视角：它是“大段快进键”——好比长字符串/注释已用 strings.Index 定位了终点，
// 逐字节走 advance 是每步一分支的慢动作，此处两遍线性扫（先数 \n 与末行起点，
// 再数末行 rune 列）即与 n 次 advance(1) 等价。障碍是末行列号：无换行时在原 col 上
// 累加 rune 数，有换行时行加 lines、列重置为末段 rune 数+1，两岔必须与 advance 的
// 前导字节规则同口径。取舍是多一次遍历换少 N 次分支，长块稳赚、短 Token 不走此路。
// 不变量：结束时 off 与逐字节推进一致，line/col 亦一致；n 越界自动钳到尾部。
// 排查现场：若长串后的首个 Token 行列错位，先看传入的 n 是否含终结符长度，次看
// 是否误把单 Token 也走了快道导致列口径分叉。
func (l *Lexer) advanceBlock(n int) {
	if n <= 0 {
		return
	}
	if l.off+n > len(l.src) {
		n = len(l.src) - l.off
	}
	s := l.src[l.off : l.off+n]
	lastNL := -1
	lines := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			lines++
			lastNL = i
		}
	}
	if lines == 0 {
		for i := 0; i < len(s); i++ {
			if c := s[i]; c < 0x80 || c >= 0xC0 {
				l.col++
			}
		}
	} else {
		l.line += lines
		c := 0
		for i := lastNL + 1; i < len(s); i++ {
			if cc := s[i]; cc < 0x80 || cc >= 0xC0 {
				c++
			}
		}
		l.col = c + 1
	}
	l.off += n
}

// longCloseStr returns the closing bracket for a long string/comment level.
// Levels 0-2 cover virtually all sources; higher levels fall back to Repeat.
//
// 设计者视角：它是长括号的“配对钥匙模”——lvl 个 '=' 对应 ]=...=]，开 `[=*[` 与闭
// 必须同级，否则 `--[[ ]]` 里的 `]=]` 不该提前收尾。障碍是高 lvl 的闭合串需动态拼：
// 对 0-2 级用常量直返（覆盖几乎全部源码，零分配），3 级以上才走 strings.Repeat 慢道。
// 取舍是调用方先经 longBracketOpen 确认确为长括号，再调本函数取闭合串去 Index 定位。
// 不变量：返回恒形如 "]"+"="*lvl+"]"。排查现场：若长注释提前闭合，先看开闭 lvl 是否
// 一致，次看是否把 `[` 误判成长括号开（后跟非 = 非 [ 时不是）。
func longCloseStr(lvl int) string {
	switch lvl {
	case 0:
		return "]]"
	case 1:
		return "]=]"
	case 2:
		return "]==]"
	default:
		return "]" + strings.Repeat("=", lvl) + "]"
	}
}

// addDiag 在当前游标处记一笔零长诊断并返回其 Span，是词法记账的统一出口。
//
// 设计者视角：好比次品标签贴在剪刀口——Span.Start==End 锚住案发点，parser 按此画线。
// 取舍是零长而非包住坏字节，让“无效字符”与“未闭合”两类锚点口径一致，宽区间由调用方另拼。
// 不变量：只追加不中断，调用后游标不动（推进由调用方负责）。排查现场：若诊断列号总偏一格，
// 先看调用时是在 advance 前还是后抓的 pos。
func (l *Lexer) addDiag(code diag.Code, format string, args ...any) token.Span {
	sp := token.Span{Start: l.pos(), End: l.pos(), File: l.file}
	l.diags = append(l.diags, diag.Errorf(code, sp, format, args...))
	return sp
}

// isAlpha 报告是否为标识符首字符（Lua 规范仅 ASCII：_ 与大小写字母）。
//
// 设计者视角：它是标识符的“验首字母章”——中文首字一律判否，落到无效字符分支记账，
// 让“标识符只能 ASCII”在词法入口即被强制执行。不变量：纯函数，与 isAlnum/isDigit 正交。
// 排查现场：若中文变量名被切成多 Token，先确认此处未放宽，而应由上层报无效字符。
func isAlpha(c byte) bool {
	return c == '_' || (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z')
}

// isDigit 报告是否为十进制数字，是数字快道的守门员。
//
// 设计者视角：next() 见 digit 即进 lexNumber，见 '.' 还要再看 peek(1) 是否为 digit
// 才进（否则是 . / .. / ...）。不变量：仅 0-9 为真，十六进制字母另由 isHex 把关。
func isDigit(c byte) bool { return c >= '0' && c <= '9' }

// isAlnum 报告是否为标识符后续字符（字母或数字），配合 isAlpha 圈出整词。
//
// 设计者视角：next() 的标识符分支用它一口气吞到词尾，再经 LookupIdent 判是否为关键字。
// 取舍是单字节判断，unicode 字母一律截断，换与 Lua 规范一致的 ASCII 词法。
func isAlnum(c byte) bool { return isAlpha(c) || isDigit(c) }

// isHex 报告是否为十六进制数字，供 0x 分支与 \x 转义复用。
//
// 设计者视角：它是 hex 语境的“验钞灯”——lexNumber 的 0x 整数/小数部与 lexShortString
// 的 \xXX 都靠它验两格。不变量：仅 0-9/a-f/A-F 为真，下划线分隔符由调用方另行允许。
func isHex(c byte) bool {
	return isDigit(c) || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
}

// isSpace 报告是否为空白（含 \v \f），是 skipSpaceAndComments 的噪音判据。
//
// 设计者视角：好比分拣前的“吹尘枪”——空格/制表/换行/回车/纵横换页一律吹走，行列推进
// 照常（\n 走 advance 的换行分支）。取舍是 \r 单独也算空白，让 \r\n 与孤 \r 都不留痕。
func isSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\v' || c == '\f'
}

// Lex scans the entire source.
//
// 设计者视角：Lex 是“一卷布一次交货”的全量入口——好比 New 开卷、next 逐件切、
// EOF 收线，toks 与 diags 双轨交付，parser 拿去即能解析聚合。障碍有两个：
// 一是首行 `#!`（可执行脚本的 shebang）必须整行吞掉，否则 '#' 会报无效字符；
// 二是坏输入永不停机，靠 next() 内部的记账+跳过保证每次 next 必有进展。
//
//	src
//	 │ New(filename, src)
//	 ▼
//	[跳 shebang] → loop{ next() → toks } → EOF 收线 → toks + diags
//
// 取舍是不做流式回调，一次返全量切片，换 parser 可任意回看；大文件内存换简单。
// 不变量：返回的 toks 必以 EOF 结尾；diags 顺序与源码一致；任意输入不 panic。
// 排查现场：若首行 `#!/usr/bin/env tlua` 报红，先看 HasPrefix("#!") 是否在 off!=0 时
// 被跳过；若 Lex 丢了尾部诊断，先确认是否只取了 toks 而忘了 Diags()。
func Lex(filename, src string) ([]token.Token, []diag.Diagnostic) {
	l := New(filename, src)
	var toks []token.Token
	// Shebang: #! at very start -> skip line.
	if strings.HasPrefix(src, "#!") {
		for !l.eof() && l.src[l.off] != '\n' {
			l.advance(1)
		}
	}
	for {
		t := l.next()
		toks = append(toks, t)
		if t.Kind == token.EOF {
			break
		}
	}
	return toks, l.diags
}

// skipSpaceAndComments 吹走空白与注释，是每次切 Token 前的清场。
//
// 设计者视角：它是切料前的“扫台布”——空格直吹、`--` 开头转 lexComment 吃整段，
// 循环直到露出真料或到头。障碍是长注释里可含换行：行列推进交给 lexComment 内部的
// advanceBlock，保证扫完后续 Token 行列仍准。取舍是不保留 trivia，注释吃完即弃，
// 换 Token 流零噪音。不变量：返回时要么 eof，要么 src[off] 为有效 Token 首字节。
// 排查现场：若注释后首 Token 行号错位，先查 lexComment 的 advanceBlock 长度，
// 次看短注释找 \n 时是否把 \n 本体留给了本循环的下一轮（应留，由 isSpace 吃）。
func (l *Lexer) skipSpaceAndComments() {
	for !l.eof() {
		c := l.src[l.off]
		if isSpace(c) {
			l.advance(1)
			continue
		}
		if c == '-' && l.peek(1) == '-' {
			l.lexComment()
			continue
		}
		break
	}
}

// lexComment 吃掉一段注释（调用时游标在 `--` 上），是噪音的最大头。
//
// 设计者视角：好比撕掉快递单上的广告贴——短注释撕到行尾，长注释 `--[=*[` 配对撕到
// 同级闭合。障碍是未闭合长注释：若傻等闭合会吞掉全文；此处记 LexUnterminatedLongComment
// 一笔并用 advanceBlock 吃到尾，让后文诊断仍有行列可依。候选曾是逐字节扫长注释体，
// 现用 strings.Index 一次定位闭合再 advanceBlock 快进，大文件稳赚。不变量：返回时游标
// 必有进展（至少吃掉 `--`）；短注释不吃换行符本身（留给 skipSpace 计数行）。
// 排查现场：若 `--[[` 后全文消失，先看 longBracketOpen 是否误判（`--[` 后非 = 非 [ 不是长注），
// 次看 Index 是否用了错级的 close 串。
func (l *Lexer) lexComment() {
	// At '--'. Check for long comment --[=*[.
	start := l.pos()
	l.advance(2)
	if !l.eof() && l.src[l.off] == '[' {
		if lvl, ok := l.longBracketOpen(); ok {
			l.advance(1 + lvl + 1) // '[' + '='*lvl + '['
			close := longCloseStr(lvl)
			idx := strings.Index(l.src[l.off:], close)
			if idx < 0 {
				// Unterminated long comment: consume rest.
				sp := token.Span{Start: start, End: l.pos(), File: l.file}
				// Advance to end for line tracking.
				l.advanceBlock(len(l.src) - l.off)
				l.diags = append(l.diags, diag.Errorf(diag.LexUnterminatedLongComment, sp, "unterminated long comment"))
				return
			}
			// Advance through content + closer, tracking newlines.
			l.advanceBlock(idx + len(close))
			return
		}
	}
	// Short comment to end of line.
	eol := len(l.src) - l.off
	for i := l.off; i < len(l.src); i++ {
		if l.src[i] == '\n' {
			eol = i - l.off
			break
		}
	}
	l.advanceBlock(eol)
}

// longBracketOpen reports [=*[ level if src[off]=='[' followed by '='* '['.
//
// 设计者视角：它是长串/长注的“试钥匙”——只看不拿（纯只读），数清 '[' 后跟几个 '='
// 再看是否还有 '['，级别即 '=' 个数。障碍是 `[`、`[=`、`[==` 后无 `[` 时都是普通中括号，
// 必须回 false 让调用方走 LBrack 单字符分支；此处把“是否”与“几级”一次答全，避免调用方
// 二次扫描。不变量：返回 ok=true 当且仅当 src[off]=='[' 且其后 '='* 后确为 '['。
// 排查现场：若 `a[1]` 被误吃成长串，先看调用前是否确认了 c=='[' 且本函数回 ok，次看
// 越界时是否把 0 当 '=' 比（本函数遇尾直接回 false）。
func (l *Lexer) longBracketOpen() (int, bool) {
	i := l.off + 1
	lvl := 0
	for i < len(l.src) && l.src[i] == '=' {
		lvl++
		i++
	}
	if i < len(l.src) && l.src[i] == '[' {
		return lvl, true
	}
	return 0, false
}

// next 切出下一个 Token，是词法的中央分发器（每次必有进展或报 EOF）。
//
// 设计者视角：它是分拣线的“总闸”——skipSpace 清场后按首字节分流：字母走标识符/
// 关键字、数字/点数走 lexNumber、引号走 lexShortString、长括号走 lexLongString、
// 其余走操作符最长匹配，兜底是无效字节记账后递归取下一个。障碍有三：
// 一是 `...`/`..`/`.` 与 `//`/`/`、`>>`/`<<` 靠最长匹配区分，顺序错则切错；
// 二是 `!=` 故意不在此处理（留给 parser 拼 `~=`，发射再统一），词法只认 `!`；
// 三是坏字节不能停机——用 utf8.DecodeRuneInString 吞一整个 rune（防半个中文死循环），
// 记 LexInvalidCharacter 后递归 next，保证 parser 永远拿到合法 Token。
// 不变量：非 EOF 即推进 off；返回 Token 的 Span.File 恒为 l.file。
// 排查现场：若 parser 报“缺操作数”连锁，先看此处是否把 `..` 吞进了数字；
// 若无效字符后列号漂移，先查 bad 段的 advance(size) 是否按 rune 列口径推进。
func (l *Lexer) next() token.Token {
	l.skipSpaceAndComments()
	start := l.pos()
	if l.eof() {
		return token.Token{Kind: token.EOF, Span: token.Span{Start: start, End: start, File: l.file}}
	}
	c := l.src[l.off]

	// Identifier / keyword (ASCII only, per Lua spec).
	if isAlpha(c) {
		i := l.off
		for i < len(l.src) && isAlnum(l.src[i]) {
			i++
		}
		lit := l.src[l.off:i]
		n := i - l.off
		l.advance(n)
		kind := token.LookupIdent(lit)
		return token.Token{Kind: kind, Literal: lit, Span: token.Span{Start: start, End: l.pos(), File: l.file}}
	}

	// Number: starts with digit, or '.' followed by digit.
	if isDigit(c) || (c == '.' && isDigit(l.peek(1))) {
		return l.lexNumber(start)
	}

	// String.
	if c == '"' || c == '\'' {
		return l.lexShortString(start)
	}
	if c == '[' {
		if _, ok := l.longBracketOpen(); ok {
			return l.lexLongString(start)
		}
		l.advance(1)
		return token.Token{Kind: token.LBrack, Literal: "[", Span: token.Span{Start: start, End: l.pos(), File: l.file}}
	}

	// Operators / punctuation: longest match first.
	switch c {
	case '.':
		if l.peek(1) == '.' && l.peek(2) == '.' {
			l.advance(3)
			return token.Token{Kind: token.DotDotDot, Literal: "...", Span: token.Span{Start: start, End: l.pos(), File: l.file}}
		}
		if l.peek(1) == '.' {
			l.advance(2)
			return token.Token{Kind: token.DotDot, Literal: "..", Span: token.Span{Start: start, End: l.pos(), File: l.file}}
		}
		l.advance(1)
		return token.Token{Kind: token.Dot, Literal: ".", Span: token.Span{Start: start, End: l.pos(), File: l.file}}
	case ':':
		if l.peek(1) == ':' {
			l.advance(2)
			return token.Token{Kind: token.ColonColon, Literal: "::", Span: token.Span{Start: start, End: l.pos(), File: l.file}}
		}
		l.advance(1)
		return token.Token{Kind: token.Colon, Literal: ":", Span: token.Span{Start: start, End: l.pos(), File: l.file}}
	case '=':
		if l.peek(1) == '=' {
			l.advance(2)
			return token.Token{Kind: token.Eq, Literal: "==", Span: token.Span{Start: start, End: l.pos(), File: l.file}}
		}
		if l.peek(1) == '>' {
			l.advance(2)
			return token.Token{Kind: token.FatArrow, Literal: "=>", Span: token.Span{Start: start, End: l.pos(), File: l.file}}
		}
		l.advance(1)
		return token.Token{Kind: token.Assign, Literal: "=", Span: token.Span{Start: start, End: l.pos(), File: l.file}}
	case '~':
		if l.peek(1) == '=' {
			l.advance(2)
			return token.Token{Kind: token.Ne, Literal: "~=", Span: token.Span{Start: start, End: l.pos(), File: l.file}}
		}
		l.advance(1)
		return token.Token{Kind: token.Tilde, Literal: "~", Span: token.Span{Start: start, End: l.pos(), File: l.file}}
	case '<':
		if l.peek(1) == '<' {
			l.advance(2)
			return token.Token{Kind: token.ShiftL, Literal: "<<", Span: token.Span{Start: start, End: l.pos(), File: l.file}}
		}
		if l.peek(1) == '=' {
			l.advance(2)
			return token.Token{Kind: token.Le, Literal: "<=", Span: token.Span{Start: start, End: l.pos(), File: l.file}}
		}
		l.advance(1)
		return token.Token{Kind: token.Lt, Literal: "<", Span: token.Span{Start: start, End: l.pos(), File: l.file}}
	case '>':
		if l.peek(1) == '>' {
			l.advance(2)
			return token.Token{Kind: token.ShiftR, Literal: ">>", Span: token.Span{Start: start, End: l.pos(), File: l.file}}
		}
		if l.peek(1) == '=' {
			l.advance(2)
			return token.Token{Kind: token.Ge, Literal: ">=", Span: token.Span{Start: start, End: l.pos(), File: l.file}}
		}
		l.advance(1)
		return token.Token{Kind: token.Gt, Literal: ">", Span: token.Span{Start: start, End: l.pos(), File: l.file}}
	case '/':
		if l.peek(1) == '/' {
			l.advance(2)
			return token.Token{Kind: token.DoubleSlash, Literal: "//", Span: token.Span{Start: start, End: l.pos(), File: l.file}}
		}
		l.advance(1)
		return token.Token{Kind: token.Slash, Literal: "/", Span: token.Span{Start: start, End: l.pos(), File: l.file}}
	case '-':
		if l.peek(1) == '>' {
			l.advance(2)
			return token.Token{Kind: token.Arrow, Literal: "->", Span: token.Span{Start: start, End: l.pos(), File: l.file}}
		}
		l.advance(1)
		return token.Token{Kind: token.Minus, Literal: "-", Span: token.Span{Start: start, End: l.pos(), File: l.file}}
	}

	// Single-char tokens.
	var kind token.Kind
	switch c {
	case '+':
		kind = token.Plus
	case '*':
		kind = token.Star
	case '%':
		kind = token.Percent
	case '^':
		kind = token.Caret
	case '#':
		kind = token.Hash
	case '&':
		kind = token.Amp
	case '|':
		kind = token.Pipe
	case '(':
		kind = token.LParen
	case ')':
		kind = token.RParen
	case '{':
		kind = token.LBrace
	case '}':
		kind = token.RBrace
	case ']':
		kind = token.RBrack
	case ';':
		kind = token.Semi
	case ',':
		kind = token.Comma
	case '?':
		kind = token.Question
	case '!':
		kind = token.Bang
	default:
		// Invalid byte. Consume one rune to avoid infinite loop.
		_, size := utf8.DecodeRuneInString(l.src[l.off:])
		if size == 0 {
			size = 1
		}
		bad := l.src[l.off : l.off+size]
		l.advance(size)
		sp := token.Span{Start: start, End: l.pos(), File: l.file}
		l.diags = append(l.diags, diag.Errorf(diag.LexInvalidCharacter, sp, "invalid character %q", bad))
		// Return next token instead of a bogus one to keep parser stable.
		return l.next()
	}
	l.advance(1)
	// Sub-slice shares the source backing (no allocation); the old
	// string(c) conversion allocated per token.
	lit := l.src[l.off-1 : l.off]
	return token.Token{Kind: kind, Literal: lit, Span: token.Span{Start: start, End: l.pos(), File: l.file}}
}

// lexNumber 切出数字字面量（入口已确认首字节为 digit 或 ./digit），存原文不求值。
//
// 设计者视角：它是数字的“描模机”——只描出字面轮廓（Literal），不算值，进制/精度语义
// 留给后层。分两条产线：0x/0X 进 hex 线（整数部必填，小数部仅当 '.' 后确为 hex digit 才吃，
// 指数是 p/P；"0x" 后无 digit 即记 LexMalformedHexNumber）；其余走 decimal 线
// （'_' 分隔直通，'.' 后跟 '.' 留给连接符，`1.` 的孤点与 `1.e5` 都按 Lua 收下，
// e/E 指数需至少一 digit 才提交）。障碍是 '.' 的三义性（小数点/连接符/表点）：
// 必须多看一字节，`.`+digit 才吃，`..` 坚决留给操作符。取舍是先算 [off:i) 再一次 advance，
// 让 Span 与 Literal 同源。不变量：返回 Kind 恒为 Number；失败也必推进防死循环。
// 排查现场：若 `1..2` 被切成 `1.`+`.2` 之外的样子，先看小数分支的 `..` 让路逻辑，
// 次看 hex 分支的 '.'+hex 预判。
func (l *Lexer) lexNumber(start token.Pos) token.Token {
	i := l.off
	n := len(l.src)
	isHexNum := false
	if i+1 < n && l.src[i] == '0' && (l.src[i+1] == 'x' || l.src[i+1] == 'X') {
		isHexNum = true
		i += 2
		hexStart := i
		for i < n && (isHex(l.src[i]) || l.src[i] == '_') {
			i++
		}
		if i == hexStart {
			// "0x" with no digits.
			for k := 0; k < i-l.off; k++ {
				l.advance(1)
			}
			sp := token.Span{Start: start, End: l.pos(), File: l.file}
			l.diags = append(l.diags, diag.Errorf(diag.LexMalformedHexNumber, sp, "malformed hexadecimal number"))
			return token.Token{Kind: token.Number, Literal: l.src[l.off:i], Span: sp}
		}
		// Optional fractional part.
		if i < n && l.src[i] == '.' {
			// Only consume '.' if followed by hex digit (else it's concat '..' or field '.').
			if i+1 < n && isHex(l.src[i+1]) {
				i++
				for i < n && (isHex(l.src[i]) || l.src[i] == '_') {
					i++
				}
			}
		}
		// Binary exponent p/P (required for hex float with fraction, optional otherwise).
		if i < n && (l.src[i] == 'p' || l.src[i] == 'P') {
			j := i + 1
			if j < n && (l.src[j] == '+' || l.src[j] == '-') {
				j++
			}
			ds := j
			for j < n && (isDigit(l.src[j]) || l.src[j] == '_') {
				j++
			}
			if j > ds {
				i = j
			}
		}
		lit := l.src[l.off:i]
		l.advance(i - l.off)
		return token.Token{Kind: token.Number, Literal: lit, Span: token.Span{Start: start, End: l.pos(), File: l.file}}
	}
	_ = isHexNum
	// Decimal: digits with optional underscores.
	for i < n && (isDigit(l.src[i]) || l.src[i] == '_') {
		i++
	}
	// Fractional part: '.' followed by digit. Careful not to swallow '..'.
	if i < n && l.src[i] == '.' {
		if i+1 < n && l.src[i+1] == '.' {
			// Leave '..' for concat operator.
		} else if i+1 < n && isDigit(l.src[i+1]) {
			i++
			for i < n && (isDigit(l.src[i]) || l.src[i] == '_') {
				i++
			}
		} else if i+1 < n && (l.src[i+1] == 'e' || l.src[i+1] == 'E') {
			// "1.e5" is valid Lua: consume '.' then exponent below.
			i++
		} else {
			// Lone '.': Lua allows trailing dot like `1.`; consume it.
			i++
		}
	}
	if i < n && (l.src[i] == 'e' || l.src[i] == 'E') {
		j := i + 1
		if j < n && (l.src[j] == '+' || l.src[j] == '-') {
			j++
		}
		ds := j
		for j < n && (isDigit(l.src[j]) || l.src[j] == '_') {
			j++
		}
		if j > ds {
			i = j
		}
	}
	lit := l.src[l.off:i]
	l.advance(i - l.off)
	return token.Token{Kind: token.Number, Literal: lit, Span: token.Span{Start: start, End: l.pos(), File: l.file}}
}

// lexShortString 切出短字符串（调用时游标在引号上），Literal 存原文转义不动。
//
// 设计者视角：它是字符串的“原样复印机”——引号、转义一律照抄，不解码，校验与求值
// 留给后层。快慢双道：先经 scanCleanString 探路，无反斜杠无换行即整段切片直返
// （零拷贝 Builder）；否则进慢道逐字节抄，转义（\xXX 取两格 hex、\u{...} 抄到 }、
// \z 吞后随空白、\ddd 补足三位）原样搬运，遇同种引号收尾，遇 \n/\r 即判未闭合记
// LexUnterminatedString。障碍是行尾孤反斜杠与文件尾：eof 即停并判未闭合，绝不越界。
// 不变量：返回 Kind 恒为 String；Literal 首尾与原文引号一致（含未闭合的残段）。
// 排查现场：若字符串 Span 跨行，先看快道是否把含 \n 的串误判 clean；若转义后列号偏，
// 先查 \z 吞空白时是否走了 advance（应走，行列同步）。
func (l *Lexer) lexShortString(start token.Pos) token.Token {
	quote := l.src[l.off]
	// Fast path: no escapes or newlines before the closing quote. The
	// slow path below copies bytes verbatim, so a clean slice is identical.
	if end, ok := scanCleanString(l.src, l.off+1, quote); ok {
		lit := l.src[start.Offset:end]
		l.advanceBlock(end - l.off)
		return token.Token{Kind: token.String, Literal: lit, Span: token.Span{Start: start, End: l.pos(), File: l.file}}
	}
	l.advance(1)
	var sb strings.Builder
	sb.WriteByte(quote)
	closed := false
	for !l.eof() {
		c := l.src[l.off]
		if c == '\n' || c == '\r' {
			break // unterminated
		}
		if c == '\\' {
			// Copy escape sequence verbatim (validation done by parser/checker if needed).
			sb.WriteByte(c)
			l.advance(1)
			if l.eof() {
				break
			}
			e := l.src[l.off]
			sb.WriteByte(e)
			l.advance(1)
			if e == 'x' {
				// \xXX: copy up to 2 hex digits.
				for k := 0; k < 2 && !l.eof() && isHex(l.src[l.off]); k++ {
					sb.WriteByte(l.src[l.off])
					l.advance(1)
				}
			} else if e == 'u' {
				// \u{...}
				if !l.eof() && l.src[l.off] == '{' {
					sb.WriteByte('{')
					l.advance(1)
					for !l.eof() && l.src[l.off] != '}' {
						sb.WriteByte(l.src[l.off])
						l.advance(1)
					}
					if !l.eof() {
						sb.WriteByte('}')
						l.advance(1)
					}
				}
			} else if e == 'z' {
				// \z skips following whitespace.
				for !l.eof() && isSpace(l.src[l.off]) {
					l.advance(1)
				}
			} else if isDigit(e) {
				// \ddd up to 3 digits total.
				for k := 1; k < 3 && !l.eof() && isDigit(l.src[l.off]); k++ {
					sb.WriteByte(l.src[l.off])
					l.advance(1)
				}
			}
			continue
		}
		if c == quote {
			sb.WriteByte(c)
			l.advance(1)
			closed = true
			break
		}
		sb.WriteByte(c)
		l.advance(1)
	}
	end := l.pos()
	sp := token.Span{Start: start, End: end, File: l.file}
	if !closed {
		l.diags = append(l.diags, diag.Errorf(diag.LexUnterminatedString, sp, "unterminated string literal"))
	}
	return token.Token{Kind: token.String, Literal: sb.String(), Span: sp}
}

// scanCleanString reports the end offset (one past the closing quote) when
// src[i:] up to the closing quote contains no backslash, newline or CR —
// i.e. the literal can be sliced verbatim. Otherwise ok=false.
//
// 设计者视角：它是短串快道的“探雷器”——好比复印前先看纸面有无涂改（反斜杠/换行），
// 干净即整段切片直返，脏则退回慢道逐字节抄。障碍是未闭合串：扫到尾无引号即回 false，
// 让慢道去记未闭合诊断，快道永不报错。取舍是多一次线性预扫，换干净串的零 Builder、
// 零转义分支；脏串多付一遍扫描但正确性不变。不变量：ok=true 时 end 恒指闭合引号后一字节，
// src[i:end] 可原样作 Literal。排查现场：若含转义的串被快道直返，先看本函数是否漏判
// '\\'，次看调用方传入的 quote 与起始 off+1 是否对齐。
func scanCleanString(src string, i int, quote byte) (end int, ok bool) {
	for i < len(src) {
		c := src[i]
		if c == quote {
			return i + 1, true
		}
		if c == '\\' || c == '\n' || c == '\r' {
			return 0, false
		}
		i++
	}
	return 0, false
}

// lexLongString 切出长字符串（调用时游标在长括号开上），首换行按 Lua 规则丢弃。
//
// 设计者视角：它是长串的“整卷搬运工”——级别 lvl 定开闭钥匙（longCloseStr），
// 开后首个 \n（或 \r\n）是 Lua 规定的“开卷换行不算数”，先丢再搬。体部用 strings.Index
// 一次定位闭合，命中即 advanceBlock 快进到终点，Literal 整段切片；未命中即吃到尾、
// 记 LexUnterminatedLongString，parser 靠此继续而非崩。障碍是开闭级别不一致的
// `] mismatch`：Index 用同级 close 串，低级闭合在高级体里只是普通字符。不变量：
// 返回 Kind 恒为 String；Literal 含开闭括号原文。排查现场：若长串吞了后文，先看开 lvl
// 是否数错（longBracketOpen），次看 Index 是否从 contentStart（丢首换行后）算起。
func (l *Lexer) lexLongString(start token.Pos) token.Token {
	lvl, _ := l.longBracketOpen()
	openLen := 1 + lvl + 1
	l.advance(openLen)
	// Skip initial newline (Lua rule).
	if !l.eof() && l.src[l.off] == '\n' {
		l.advance(1)
	} else if !l.eof() && l.src[l.off] == '\r' {
		l.advance(1)
		if !l.eof() && l.src[l.off] == '\n' {
			l.advance(1)
		}
	}
	contentStart := l.off
	close := longCloseStr(lvl)
	idx := strings.Index(l.src[contentStart:], close)
	var lit string
	if idx < 0 {
		// Unterminated: consume rest.
		l.advanceBlock(len(l.src) - l.off)
		lit = l.src[start.Offset:l.off]
		sp := token.Span{Start: start, End: l.pos(), File: l.file}
		l.diags = append(l.diags, diag.Errorf(diag.LexUnterminatedLongString, sp, "unterminated long string"))
		return token.Token{Kind: token.String, Literal: lit, Span: sp}
	}
	endOff := contentStart + idx + len(close)
	l.advanceBlock(endOff - l.off)
	lit = l.src[start.Offset:l.off]
	return token.Token{Kind: token.String, Literal: lit, Span: token.Span{Start: start, End: l.pos(), File: l.file}}
}
