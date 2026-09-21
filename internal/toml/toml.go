// Package toml is a small strict TOML subset for tlua manifests and locks.
//
// Supported: tables `[a.b."c.d"]`, arrays of tables `[[a]]`, key/value
// pairs with bare or quoted keys, and basic/literal strings, integers,
// floats, booleans, and arrays (multiline, trailing commas, comments).
// Unsupported (clear errors): inline tables, datetimes, hex/oct/bin
// integers, multi-line strings. Decoding preserves nothing but values;
// encoding is deterministic (callers order keys).
package toml

import (
	"fmt"
	"strconv"
	"strings"
)

// Kind classifies a value.
type Kind int

const (
	String Kind = iota
	Integer
	Float
	Boolean
	Array
)

// Value is one decoded value.
type Value struct {
	Kind  Kind
	Str   string
	Int   int64
	Float float64
	Bool  bool
	Arr   []*Value
}

func (v *Value) String() string {
	switch v.Kind {
	case String:
		return strconv.Quote(v.Str)
	case Integer:
		return strconv.FormatInt(v.Int, 10)
	case Float:
		return strconv.FormatFloat(v.Float, 'g', -1, 64)
	case Boolean:
		if v.Bool {
			return "true"
		}
		return "false"
	case Array:
		var parts []string
		for _, e := range v.Arr {
			parts = append(parts, e.String())
		}
		return "[" + strings.Join(parts, ", ") + "]"
	}
	return ""
}

// Table is one `[table]` body.
type Table struct {
	Values map[string]*Value
}

// Doc is a decoded document. Tables are keyed by \x00-joined path
// segments; Arrays holds `[[name]]` entries in file order.
type Doc struct {
	Tables map[string]*Table
	Arrays map[string][]*Table
}

func (d *Doc) table(key string) *Table {
	if d.Tables == nil {
		d.Tables = map[string]*Table{}
	}
	t, ok := d.Tables[key]
	if !ok {
		t = &Table{Values: map[string]*Value{}}
		d.Tables[key] = t
	}
	return t
}

type parser struct {
	src   string
	pos   int
	line  int
	doc   *Doc
	cur   *Table
	curA  *Table // current [[array]] element
	arrKy string // current [[array]] key ("" when in plain table)
}

func (p *parser) fail(format string, args ...any) error {
	return fmt.Errorf("toml:%d: %s", p.line, fmt.Sprintf(format, args...))
}

// Decode parses src into a Doc.
func Decode(src string) (*Doc, error) {
	p := &parser{src: src, line: 1, doc: &Doc{}}
	p.cur = p.doc.table("")
	for p.pos < len(p.src) {
		p.skipSpaceInline()
		if p.pos >= len(p.src) {
			break
		}
		c := p.src[p.pos]
		if c == '\n' {
			p.pos++
			p.line++
			continue
		}
		if c == '#' {
			p.skipLine()
			continue
		}
		if c == '[' {
			if err := p.parseHeader(); err != nil {
				return nil, err
			}
			continue
		}
		if err := p.parsePair(); err != nil {
			return nil, err
		}
	}
	return p.doc, nil
}

func (p *parser) skipSpaceInline() {
	for p.pos < len(p.src) && (p.src[p.pos] == ' ' || p.src[p.pos] == '\t' || p.src[p.pos] == '\r') {
		p.pos++
	}
}

func (p *parser) skipLine() {
	for p.pos < len(p.src) && p.src[p.pos] != '\n' {
		p.pos++
	}
}

// parseHeader parses `[a.b]` or `[[a.b]]` and selects the table.
func (p *parser) parseHeader() error {
	startLine := p.line
	p.pos++ // [
	isArr := false
	if p.pos < len(p.src) && p.src[p.pos] == '[' {
		isArr = true
		p.pos++
	}
	p.skipSpaceInline()
	segs, err := p.parseDottedKey()
	if err != nil {
		return err
	}
	p.skipSpaceInline()
	if isArr {
		if p.pos+1 >= len(p.src) || p.src[p.pos] != ']' || p.src[p.pos+1] != ']' {
			return p.fail("expected ']]'")
		}
		p.pos += 2
	} else {
		if p.pos >= len(p.src) || p.src[p.pos] != ']' {
			return p.fail("expected ']'")
		}
		p.pos++
	}
	p.skipSpaceInline()
	if p.pos < len(p.src) && p.src[p.pos] == '#' {
		p.skipLine()
	} else if p.pos < len(p.src) && p.src[p.pos] != '\n' {
		return p.fail("unexpected content after table header (line %d opened)", startLine)
	}
	key := strings.Join(segs, "\x00")
	if isArr {
		t := &Table{Values: map[string]*Value{}}
		if p.doc.Arrays == nil {
			p.doc.Arrays = map[string][]*Table{}
		}
		p.doc.Arrays[key] = append(p.doc.Arrays[key], t)
		p.cur = t
		p.curA = t
		p.arrKy = key
	} else {
		if _, dup := p.doc.Tables[key]; dup {
			return p.fail("duplicate table [%s]", strings.Join(segs, "."))
		}
		// A plain table must not collide with an array of tables.
		if _, dup := p.doc.Arrays[key]; dup {
			return p.fail("table [%s] collides with array of tables", strings.Join(segs, "."))
		}
		p.cur = p.doc.table(key)
		p.curA = nil
		p.arrKy = ""
	}
	return nil
}

// parseDottedKey parses `a.b."c.d".'e'` into segments.
func (p *parser) parseDottedKey() ([]string, error) {
	var segs []string
	for {
		p.skipSpaceInline()
		if p.pos >= len(p.src) {
			return nil, p.fail("unexpected end in key")
		}
		var seg string
		c := p.src[p.pos]
		switch {
		case c == '"':
			s, err := p.parseBasicRaw()
			if err != nil {
				return nil, err
			}
			seg = s
		case c == '\'':
			s, err := p.parseLiteralRaw()
			if err != nil {
				return nil, err
			}
			seg = s
		case isBareKeyChar(c):
			start := p.pos
			for p.pos < len(p.src) && isBareKeyChar(p.src[p.pos]) {
				p.pos++
			}
			seg = p.src[start:p.pos]
		default:
			return nil, p.fail("invalid key character %q", c)
		}
		segs = append(segs, seg)
		p.skipSpaceInline()
		if p.pos < len(p.src) && p.src[p.pos] == '.' {
			p.pos++
			continue
		}
		return segs, nil
	}
}

func isBareKeyChar(c byte) bool {
	return c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '_' || c == '-'
}

// parsePair parses `key = value`.
func (p *parser) parsePair() error {
	segs, err := p.parseDottedKey()
	if err != nil {
		return err
	}
	if len(segs) != 1 {
		return p.fail("dotted keys in pairs are not supported (use [table] headers)")
	}
	key := segs[0]
	p.skipSpaceInline()
	if p.pos >= len(p.src) || p.src[p.pos] != '=' {
		return p.fail("expected '=' after key %q", key)
	}
	p.pos++
	p.skipSpaceInline()
	v, err := p.parseValue()
	if err != nil {
		return err
	}
	p.skipSpaceInline()
	if p.pos < len(p.src) && p.src[p.pos] == '#' {
		p.skipLine()
	} else if p.pos < len(p.src) && p.src[p.pos] != '\n' {
		return p.fail("unexpected content after value for key %q", key)
	}
	if _, dup := p.cur.Values[key]; dup {
		return p.fail("duplicate key %q", key)
	}
	p.cur.Values[key] = v
	return nil
}

// parseValue parses one value at p.pos (no leading space).
func (p *parser) parseValue() (*Value, error) {
	if p.pos >= len(p.src) {
		return nil, p.fail("unexpected end in value")
	}
	c := p.src[p.pos]
	switch {
	case c == '"':
		s, err := p.parseBasicRaw()
		if err != nil {
			return nil, err
		}
		return &Value{Kind: String, Str: s}, nil
	case c == '\'':
		s, err := p.parseLiteralRaw()
		if err != nil {
			return nil, err
		}
		return &Value{Kind: String, Str: s}, nil
	case c == '[':
		return p.parseArray()
	case c == 't' && strings.HasPrefix(p.src[p.pos:], "true") && !isBareKeyChar(peekAt(p.src, p.pos+4)):
		p.pos += 4
		return &Value{Kind: Boolean, Bool: true}, nil
	case c == 'f' && strings.HasPrefix(p.src[p.pos:], "false") && !isBareKeyChar(peekAt(p.src, p.pos+5)):
		p.pos += 5
		return &Value{Kind: Boolean, Bool: false}, nil
	case c == '+' || c == '-' || c >= '0' && c <= '9':
		return p.parseNumber()
	default:
		return nil, p.fail("invalid value starting with %q", c)
	}
}

func peekAt(s string, i int) byte {
	if i < len(s) {
		return s[i]
	}
	return 0
}

// parseBasicRaw parses the body of a "..." string (no multi-line support).
func (p *parser) parseBasicRaw() (string, error) {
	p.pos++ // opening quote
	var sb strings.Builder
	for {
		if p.pos >= len(p.src) {
			return "", p.fail("unterminated string")
		}
		c := p.src[p.pos]
		if c == '"' {
			p.pos++
			return sb.String(), nil
		}
		if c == '\n' {
			return "", p.fail("newline in single-line string")
		}
		if c != '\\' {
			sb.WriteByte(c)
			p.pos++
			continue
		}
		p.pos++
		if p.pos >= len(p.src) {
			return "", p.fail("unterminated escape")
		}
		e := p.src[p.pos]
		p.pos++
		switch e {
		case 'n':
			sb.WriteByte('\n')
		case 't':
			sb.WriteByte('\t')
		case 'r':
			sb.WriteByte('\r')
		case 'b':
			sb.WriteByte('\b')
		case 'f':
			sb.WriteByte('\f')
		case '\\':
			sb.WriteByte('\\')
		case '"':
			sb.WriteByte('"')
		case 'u', 'U':
			n := 4
			if e == 'U' {
				n = 8
			}
			if p.pos+n > len(p.src) {
				return "", p.fail("truncated unicode escape")
			}
			code, err := strconv.ParseUint(p.src[p.pos:p.pos+n], 16, 32)
			if err != nil {
				return "", p.fail("invalid unicode escape %q", p.src[p.pos:p.pos+n])
			}
			p.pos += n
			if code > 0x10FFFF || code >= 0xD800 && code <= 0xDFFF {
				return "", p.fail("invalid unicode escape U+%X", code)
			}
			sb.WriteRune(rune(code))
		default:
			return "", p.fail("invalid escape %q", e)
		}
	}
}

// parseLiteralRaw parses the body of a '...' string.
func (p *parser) parseLiteralRaw() (string, error) {
	p.pos++ // opening quote
	start := p.pos
	for {
		if p.pos >= len(p.src) {
			return "", p.fail("unterminated literal string")
		}
		if p.src[p.pos] == '\'' {
			s := p.src[start:p.pos]
			p.pos++
			return s, nil
		}
		if p.src[p.pos] == '\n' {
			return "", p.fail("newline in single-line string")
		}
		p.pos++
	}
}

// parseNumber parses an integer or float.
func (p *parser) parseNumber() (*Value, error) {
	start := p.pos
	if p.src[p.pos] == '+' || p.src[p.pos] == '-' {
		p.pos++
	}
	digits := 0
	for p.pos < len(p.src) && (p.src[p.pos] >= '0' && p.src[p.pos] <= '9' || p.src[p.pos] == '_') {
		p.pos++
		digits++
	}
	if digits == 0 {
		return nil, p.fail("invalid number")
	}
	isFloat := false
	if p.pos < len(p.src) && p.src[p.pos] == '.' {
		isFloat = true
		p.pos++
		for p.pos < len(p.src) && (p.src[p.pos] >= '0' && p.src[p.pos] <= '9' || p.src[p.pos] == '_') {
			p.pos++
		}
	}
	if p.pos < len(p.src) && (p.src[p.pos] == 'e' || p.src[p.pos] == 'E') {
		isFloat = true
		p.pos++
		if p.pos < len(p.src) && (p.src[p.pos] == '+' || p.src[p.pos] == '-') {
			p.pos++
		}
		if p.pos >= len(p.src) || p.src[p.pos] < '0' || p.src[p.pos] > '9' {
			return nil, p.fail("invalid float exponent")
		}
		for p.pos < len(p.src) && (p.src[p.pos] >= '0' && p.src[p.pos] <= '9' || p.src[p.pos] == '_') {
			p.pos++
		}
	}
	raw := strings.ReplaceAll(p.src[start:p.pos], "_", "")
	if isFloat {
		f, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			return nil, p.fail("invalid float %q", raw)
		}
		return &Value{Kind: Float, Float: f}, nil
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return nil, p.fail("invalid integer %q", raw)
	}
	return &Value{Kind: Integer, Int: n}, nil
}

// parseArray parses a possibly multi-line [...] value.
func (p *parser) parseArray() (*Value, error) {
	p.pos++ // [
	out := &Value{Kind: Array}
	for {
		p.skipArraySpace()
		if p.pos >= len(p.src) {
			return nil, p.fail("unterminated array")
		}
		if p.src[p.pos] == ']' {
			p.pos++
			return out, nil
		}
		v, err := p.parseValue()
		if err != nil {
			return nil, err
		}
		out.Arr = append(out.Arr, v)
		p.skipArraySpace()
		if p.pos >= len(p.src) {
			return nil, p.fail("unterminated array")
		}
		if p.src[p.pos] == ',' {
			p.pos++
			continue
		}
		if p.src[p.pos] == ']' {
			p.pos++
			return out, nil
		}
		return nil, p.fail("expected ',' or ']' in array")
	}
}

func (p *parser) skipArraySpace() {
	for p.pos < len(p.src) {
		c := p.src[p.pos]
		if c == ' ' || c == '\t' || c == '\r' || c == '\n' {
			if c == '\n' {
				p.line++
			}
			p.pos++
			continue
		}
		if c == '#' {
			p.skipLine()
			continue
		}
		return
	}
}
