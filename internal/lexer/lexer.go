package lexer

import (
	"strings"
	"unicode/utf8"

	"github.com/typelua/typelua/internal/diag"
	"github.com/typelua/typelua/internal/token"
)

// Lexer turns TypeLua source into tokens.
type Lexer struct {
	src      string
	file     string
	off      int
	line     int
	col      int
	diags    []diag.Diagnostic
	finished bool
}

func New(filename, src string) *Lexer {
	return &Lexer{src: src, file: filename, line: 1, col: 1}
}

func (l *Lexer) Diags() []diag.Diagnostic { return l.diags }

func (l *Lexer) pos() token.Pos {
	return token.Pos{Offset: l.off, Line: l.line, Column: l.col}
}

func (l *Lexer) eof() bool { return l.off >= len(l.src) }

func (l *Lexer) peek(n int) byte {
	if l.off+n >= len(l.src) {
		return 0
	}
	return l.src[l.off+n]
}

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

func (l *Lexer) addDiag(code, format string, args ...any) token.Span {
	sp := token.Span{Start: l.pos(), End: l.pos(), File: l.file}
	l.diags = append(l.diags, diag.Errorf(code, sp, format, args...))
	return sp
}

func isAlpha(c byte) bool {
	return c == '_' || (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z')
}
func isDigit(c byte) bool { return c >= '0' && c <= '9' }
func isAlnum(c byte) bool { return isAlpha(c) || isDigit(c) }
func isHex(c byte) bool {
	return isDigit(c) || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
}
func isSpace(c byte) bool { return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\v' || c == '\f' }

// Lex scans the entire source.
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

func (l *Lexer) lexComment() {
	// At '--'. Check for long comment --[=*[.
	start := l.pos()
	l.advance(2)
	if !l.eof() && l.src[l.off] == '[' {
		if lvl, ok := l.longBracketOpen(); ok {
			l.advance(1 + lvl + 1) // '[' + '='*lvl + '['
			close := "]" + strings.Repeat("=", lvl) + "]"
			idx := strings.Index(l.src[l.off:], close)
			if idx < 0 {
				// Unterminated long comment: consume rest.
				sp := token.Span{Start: start, End: l.pos(), File: l.file}
				// Advance to end for line tracking.
				for !l.eof() {
					l.advance(1)
				}
				l.diags = append(l.diags, diag.Errorf("TL1001", sp, "unterminated long comment"))
				return
			}
			// Advance through content + closer, tracking newlines.
			for i := 0; i < idx+len(close); i++ {
				l.advance(1)
			}
			return
		}
	}
	// Short comment to end of line.
	for !l.eof() && l.src[l.off] != '\n' {
		l.advance(1)
	}
}

// longBracketOpen reports [=*[ level if src[off]=='[' followed by '='* '['.
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
		l.diags = append(l.diags, diag.Errorf("TL1002", sp, "invalid character %q", bad))
		// Return next token instead of a bogus one to keep parser stable.
		return l.next()
	}
	l.advance(1)
	return token.Token{Kind: kind, Literal: string(c), Span: token.Span{Start: start, End: l.pos(), File: l.file}}
}

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
			l.diags = append(l.diags, diag.Errorf("TL1003", sp, "malformed hexadecimal number"))
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

func (l *Lexer) lexShortString(start token.Pos) token.Token {
	quote := l.src[l.off]
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
		l.diags = append(l.diags, diag.Errorf("TL1004", sp, "unterminated string literal"))
	}
	return token.Token{Kind: token.String, Literal: sb.String(), Span: sp}
}

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
	close := "]" + strings.Repeat("=", lvl) + "]"
	idx := strings.Index(l.src[contentStart:], close)
	var lit string
	if idx < 0 {
		// Unterminated: consume rest.
		for !l.eof() {
			l.advance(1)
		}
		lit = l.src[start.Offset:l.off]
		sp := token.Span{Start: start, End: l.pos(), File: l.file}
		l.diags = append(l.diags, diag.Errorf("TL1005", sp, "unterminated long string"))
		return token.Token{Kind: token.String, Literal: lit, Span: sp}
	}
	endOff := contentStart + idx + len(close)
	for l.off < endOff {
		l.advance(1)
	}
	lit = l.src[start.Offset:l.off]
	return token.Token{Kind: token.String, Literal: lit, Span: token.Span{Start: start, End: l.pos(), File: l.file}}
}
