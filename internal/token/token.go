package token

import "fmt"

// Kind identifies the type of a lexical token.
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
type Pos struct {
	Offset int
	Line   int
	Column int
}

// String 把位置格式化为 "行:列"，供诊断信息展示。
func (p Pos) String() string {
	return fmt.Sprintf("%d:%d", p.Line, p.Column)
}

// Span is a half-open [Start, End) range in the source.
type Span struct {
	Start Pos
	End   Pos
	File  string
}

// Token is a single lexical unit.
type Token struct {
	Kind    Kind
	Literal string
	Span    Span
}

// String 把 token 渲染为可读形式，用于调试打印与诊断上下文。
func (t Token) String() string {
	if t.Literal != "" && t.Kind != Ident && !t.Kind.IsKeyword() {
		return fmt.Sprintf("%s(%q)", t.Kind, t.Literal)
	}
	if t.Literal != "" {
		return fmt.Sprintf("%q", t.Literal)
	}
	return t.Kind.String()
}
