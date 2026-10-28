package token

import "fmt"

// Kind identifies the type of a lexical token.
//
// 设计者视角：Kind 是 Token 的“身份证号码”——好比仓库里每个货箱的条码，
// parser 全靠 switch 它来分拣，而不用昂贵的字符串比较。障碍是 Lua 关键字、
// TypeLua 扩展关键字与标点必须挤进同一枚举：扩展部分全是软关键字，词法只贴
// “疑似关键字”标签，判决权留给 parser，词法因此保持无状态。取舍是用 int 而非
// string：跳转快、内存紧凑，人读名字只在报错/调试时经 String() 现算。
// 不变量：iota 顺序一旦落盘/缓存即冻结，新增种类只能追加，绝不重排。
// 排查现场：若关键字突然被当成 Ident，先看 LookupIdent 的长度快路径，再看
// keywords 表是否漏注册，最后查 Kind.String() 是否缺 case 而掉进 token(n)。
type Kind int

const (
	EOF Kind = iota
	Ident
	Number
	String

	// Keywords (Lua)
	And
	Break
	Do
	Else
	Elseif
	End
	False
	For
	Function
	Goto
	If
	In
	Local
	Nil
	Not
	Or
	Repeat
	Return
	Then
	True
	Until
	While

	// Keywords (TypeLua extensions)
	TypeKw
	InterfaceKw
	EnumKw
	AsKw
	ExtendsKw
	ExportKw
	DeclareKw
	KeyofKw
	TypeofKw
	IsKw
	ReadonlyKw
	ClassKw
	SuperKw
	StaticKw
	PrivateKw
	PublicKw
	ProtectedKw
	AbstractKw
	ImplementsKw
	InferKw
	MacroKw

	// Literals / punctuation
	Plus        // +
	Minus       // -
	Star        // *
	Slash       // /
	DoubleSlash // //
	Percent     // %
	Caret       // ^
	Hash        // #
	Amp         // &
	Tilde       // ~
	Pipe        // |
	ShiftR      // >>
	ShiftL      // <<
	DotDot      // ..
	DotDotDot   // ...
	Lt          // <
	Le          // <=
	Gt          // >
	Ge          // >=
	Eq          // ==
	Ne          // ~=
	ColonColon  // ::
	Colon       // :
	Semi        // ;
	Comma       // ,
	Dot         // .
	Assign      // =
	LParen      // (
	RParen      // )
	LBrace      // {
	RBrace      // }
	LBrack      // [
	RBrack      // ]
	Question    // ?
	Arrow       // ->
	FatArrow    // =>
	Bang        // !
)

var keywords = map[string]Kind{
	"and":        And,
	"break":      Break,
	"do":         Do,
	"else":       Else,
	"elseif":     Elseif,
	"end":        End,
	"false":      False,
	"for":        For,
	"function":   Function,
	"goto":       Goto,
	"if":         If,
	"in":         In,
	"local":      Local,
	"nil":        Nil,
	"not":        Not,
	"or":         Or,
	"repeat":     Repeat,
	"return":     Return,
	"then":       Then,
	"true":       True,
	"until":      Until,
	"while":      While,
	"type":       TypeKw,
	"interface":  InterfaceKw,
	"enum":       EnumKw,
	"as":         AsKw,
	"extends":    ExtendsKw,
	"export":     ExportKw,
	"declare":    DeclareKw,
	"keyof":      KeyofKw,
	"typeof":     TypeofKw,
	"is":         IsKw,
	"readonly":   ReadonlyKw,
	"class":      ClassKw,
	"super":      SuperKw,
	"static":     StaticKw,
	"private":    PrivateKw,
	"public":     PublicKw,
	"protected":  ProtectedKw,
	"abstract":   AbstractKw,
	"implements": ImplementsKw,
	"infer":      InferKw,
	"macro":      MacroKw,
}

// LookupIdent maps an identifier to a keyword kind, or Ident.
//
// 设计者视角：它是词法前台的“查表员”——好比快递分拣员先看面单是否为专线件，
// 是专线（关键字）走专线槽，否则走普通 Ident 槽。障碍是每个标识符都要过一次
// map 哈希，热点路径代价高：观测发现关键字最短 2（as/do/if/in/is/or）、最长 10
// （implements），故先用长度快路径把单字符与长标识符直接放行，结果与查表完全一致。
// 候选曾是 switch/完美哈希，但 map + 长度守卫已足够快且增关键字时只改一处表。
// 不变量：纯函数、无副作用，大小写敏感（Lua 关键字全小写）。排查现场：若新软关键字
// 不生效，先看 keywords 表是否注册，再看长度是否超出 2-10 区间被快路径提前拦截。
func LookupIdent(s string) Kind {
	// Fast path: no keyword is shorter than 2 ("as","do","if","in","is","or")
	// or longer than 10 ("implements"). This skips the map hash for the
	// common long-identifier and single-char cases with identical results.
	if len(s) < 2 || len(s) > 10 {
		return Ident
	}
	if k, ok := keywords[s]; ok {
		return k
	}
	return Ident
}

// IsKeyword reports whether k is any keyword.
//
// 设计者视角：它是“查岗器”——好比问“这人是不是在编人员”，Token.String() 与 parser 的
// 软关键字回退都要先问它。障碍是关键字集合横跨 Lua 全集与 TypeLua 扩展两拨人，
// 若用 map 查会有哈希与初始化顺序负担；此处用 switch 让编译器编成跳转表，零分配、
// 可内联。取舍是增关键字时要同步改 keywords 表、IsKeyword、String() 三处，靠 code review
// 而非单点真相换取热点零开销。不变量：返回 true 当且仅当 LookupIdent 可能产出该 Kind。
// 排查现场：若某关键字打印成带引号的 "type" 而非裸词，先查本函数是否漏了该 Kind。
func (k Kind) IsKeyword() bool {
	switch k {
	case And, Break, Do, Else, Elseif, End, False, For, Function,
		Goto, If, In, Local, Nil, Not, Or, Repeat, Return, Then,
		True, Until, While, TypeKw, InterfaceKw, EnumKw, AsKw,
		ExtendsKw, ExportKw, DeclareKw, KeyofKw, TypeofKw, IsKw, ReadonlyKw,
		ClassKw, SuperKw, StaticKw, PrivateKw, PublicKw, ProtectedKw, AbstractKw, ImplementsKw, InferKw, MacroKw:
		return true
	}
	return false
}

// String 返回 Kind 的文本名称，用于诊断与调试打印；未知 Kind 回退为 "token(n)"。
//
// 设计者视角：它是条码到“人话”的翻译器——好比仓库盘点时把货号念成货名，
// 诊断里写 `expected end` 而非 `expected 12` 全靠它。障碍是 Kind 是 int，
// 越界/野值仍可能流入（反序列化、测试构造）：若 panic 会把定位问题变成崩溃，
// 故未知回退为 token(n)，宁可含糊也不摔。取舍是长 switch 手写而非自动生成，
// 换可读的诊断措辞（identifier/number/string 是类别名，其余是字面拼写）。
// 不变量：已知 Kind 永不返回空串；同一 Kind 多次调用结果一致。
// 排查现场：若诊断里冒出 token(97)，说明有新 Kind 只加了常量却忘了在此加 case。
func (k Kind) String() string {
	switch k {
	case EOF:
		return "EOF"
	case Ident:
		return "identifier"
	case Number:
		return "number"
	case String:
		return "string"
	case And:
		return "and"
	case Break:
		return "break"
	case Do:
		return "do"
	case Else:
		return "else"
	case Elseif:
		return "elseif"
	case End:
		return "end"
	case False:
		return "false"
	case For:
		return "for"
	case Function:
		return "function"
	case Goto:
		return "goto"
	case If:
		return "if"
	case In:
		return "in"
	case Local:
		return "local"
	case Nil:
		return "nil"
	case Not:
		return "not"
	case Or:
		return "or"
	case Repeat:
		return "repeat"
	case Return:
		return "return"
	case Then:
		return "then"
	case True:
		return "true"
	case Until:
		return "until"
	case While:
		return "while"
	case TypeKw:
		return "type"
	case InterfaceKw:
		return "interface"
	case EnumKw:
		return "enum"
	case AsKw:
		return "as"
	case ExtendsKw:
		return "extends"
	case ExportKw:
		return "export"
	case DeclareKw:
		return "declare"
	case KeyofKw:
		return "keyof"
	case TypeofKw:
		return "typeof"
	case IsKw:
		return "is"
	case ReadonlyKw:
		return "readonly"
	case ClassKw:
		return "class"
	case SuperKw:
		return "super"
	case StaticKw:
		return "static"
	case PrivateKw:
		return "private"
	case PublicKw:
		return "public"
	case ProtectedKw:
		return "protected"
	case AbstractKw:
		return "abstract"
	case ImplementsKw:
		return "implements"
	case InferKw:
		return "infer"
	case MacroKw:
		return "macro"
	case Plus:
		return "+"
	case Minus:
		return "-"
	case Star:
		return "*"
	case Slash:
		return "/"
	case DoubleSlash:
		return "//"
	case Percent:
		return "%"
	case Caret:
		return "^"
	case Hash:
		return "#"
	case Amp:
		return "&"
	case Tilde:
		return "~"
	case Pipe:
		return "|"
	case ShiftR:
		return ">>"
	case ShiftL:
		return "<<"
	case DotDot:
		return ".."
	case DotDotDot:
		return "..."
	case Lt:
		return "<"
	case Le:
		return "<="
	case Gt:
		return ">"
	case Ge:
		return ">="
	case Eq:
		return "=="
	case Ne:
		return "~="
	case ColonColon:
		return "::"
	case Colon:
		return ":"
	case Semi:
		return ";"
	case Comma:
		return ","
	case Dot:
		return "."
	case Assign:
		return "="
	case LParen:
		return "("
	case RParen:
		return ")"
	case LBrace:
		return "{"
	case RBrace:
		return "}"
	case LBrack:
		return "["
	case RBrack:
		return "]"
	case Question:
		return "?"
	case Arrow:
		return "->"
	case FatArrow:
		return "=>"
	case Bang:
		return "!"
	}
	return fmt.Sprintf("token(%d)", int(k))
}

// Pos is a byte offset + line/column (1-based line, 1-based column in runes).
//
// 设计者视角：Pos 是全编译器的“坐标货币”——好比面单上同时印机器码（Offset）与
// 人读地址（Line/Column）：机器拿 Offset 做 O(1) 切片，编辑器拿行列画线，双方
// 都不需二次换算。障碍是源码含多字节 rune（中文注释/字符串）：若 Column 按字节计，
// 中文一出现列号就跑偏；故 Column 按 rune 计数，Offset 按字节计数，各走各的尺。
// 不变量：Offset 为 0 起字节偏移，Line/Column 为 1 起；同一文件的 Pos 可比 Offset 定序。
// 排查现场：若列号在中文行漂移，先查 lexer 的 advance 是否只给前导字节加列，次查
// 是否有人把 Offset 当 rune 下标去切片 src。
type Pos struct {
	Offset int
	Line   int
	Column int
}

// String 把位置格式化为 "行:列"，供诊断信息展示。
//
// 设计者视角：它是坐标的“门牌写法”——好比 3:5 即 3 楼 5 号，diag.Diagnostic.String()
// 拼 file:line:col 全靠它。障碍是 Pos 还有 Offset 与 File，直接打印三者会淹没用户；
// 此处只取人读的两维，机器维留给切分逻辑。取舍是不带文件名（文件名由 Span/Diagnostic
// 层拼接），让 Pos 保持文件无关、可跨文件比较行列。不变量：永不返回空串，格式恒为
// "%d:%d"。排查现场：若诊断行列是 0:0，说明该 Pos 是零值合成的，往上查是谁忘了填 Span。
func (p Pos) String() string {
	return fmt.Sprintf("%d:%d", p.Line, p.Column)
}

// Span is a half-open [Start, End) range in the source.
//
// 设计者视角：Span 是贴在每个 Token/节点上的“面单”——好比快递单标明取件区间，
// 半开 [Start, End) 让长度 = End.Offset-Start.Offset，切片 src[Start.Offset:End.Offset]
// 一刀即中。障碍是空 Token（EOF/合成节点）无长度可言：允许 Start==End 合法，
// 让空文件与插入的恢复节点也有可报的锚点。取舍是 Start/End 各带完整 Pos（含行列），
// 空间换时间，避免展示时回扫全文重算行列。不变量：Start.Offset <= End.Offset，
// 同一 Span 的 File 唯一。排查现场：若诊断画线偏一格，先确认是否把闭区间当半开用，
// 次查 End 是否取成了下个 Token 的 Start。
type Span struct {
	Start Pos
	End   Pos
	File  string
}

// Token is a single lexical unit.
//
// 设计者视角：Token 是流水线上的“一件货”——好比 Kind 定品类、Literal 留原样、
// Span 贴面单三件套，缺一不可。障碍是数字/字符串若在词法层求值，会把类型语义
// （进制、转义、精度）提前耦合进来；故 Literal 只存原文切片，赋义推迟到 parser/checker。
// 取舍是不存 trivia（注释/空白）与父指针，Token 流保持扁平，parser 无需跳噪音。
// 不变量：Literal 与源码切片字节一致（不规范化）；EOF 的 Literal 为空、Span 为零长锚点。
// 排查现场：若字面量值对不上，先看 Literal 是否含引号/前后缀原文，再往上查求值层。
type Token struct {
	Kind    Kind
	Literal string
	Span    Span
}

// String 把 token 渲染为可读形式，用于调试打印与诊断上下文。
//
// 设计者视角：它是货箱的“展示牌”——调试时一眼看出是 `"end"` 还是 `..(%q)`。
// 规则分三档：非 Ident 非关键字的带 Literal 者印成 Kind("lit")（如 ..("..")）；
// Ident 与关键字带 Literal 者直接印 "lit"（关键字读着像源码）；无 Literal 者退守
// Kind.String()。障碍是诊断里既要精确又要紧凑：全印 Kind+Literal 太啰嗦，全印
// Literal 又分不清 `=` 与 `==`；此分档是可读性与精确性的折中。不变量：永不返回空串，
// 含 Literal 时必带引号转义。排查现场：若断言里 token 显示不一致，先确认是走的本函数
// 还是 Kind.String()，两者措辞不同。
func (t Token) String() string {
	if t.Literal != "" && t.Kind != Ident && !t.Kind.IsKeyword() {
		return fmt.Sprintf("%s(%q)", t.Kind, t.Literal)
	}
	if t.Literal != "" {
		return fmt.Sprintf("%q", t.Literal)
	}
	return t.Kind.String()
}
