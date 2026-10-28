// Package toml 是 tlua 清单与锁文件使用的严格 TOML 子集。
//
// 支持：表 `[a.b."c.d"]`、表数组 `[[a]]`、裸键或引号键的键值对，
// 以及基本/字面量字符串、整数、浮点、布尔、数组（多行、尾逗号、注释）
// 与单行内联表。
// 不支持（明确报错）：日期时间、进制整数、多行字符串。
// 解码只保留值；编码确定性（调用方决定键顺序）。
package toml

import (
	"fmt"
	"strconv"
	"strings"
)

// Kind 分类值。
type Kind int

const (
	// String 表示字符串值。
	String Kind = iota
	// Integer 表示整数值。
	Integer
	// Float 表示浮点值。
	Float
	// Boolean 表示布尔值。
	Boolean
	// Array 表示数组值。
	Array
	// Inline 表示单行内联表。
	Inline
)

// Value 是一个解码后的 TOML 值（按 Kind 判别）。
//
// 物理布局：
//
//	Value
//	├── Kind  Kind           // String/Integer/Float/Boolean/Array/Inline
//	├── Str   string         // 字符串值
//	├── Int   int64          // 整数值
//	├── Float float64        // 浮点值
//	├── Bool  bool           // 布尔值
//	├── Arr   []*Value       // 数组元素
//	└── Map   map[string]*Value // 内联表
type Value struct {
	Kind  Kind
	Str   string
	Int   int64
	Float float64
	Bool  bool
	Arr   []*Value
	Map   map[string]*Value // Inline tables
}

// String 把 Value 渲染为 TOML 文本。
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
	case Inline:
		var parts []string
		for _, k := range SortedKeys(v.Map) {
			parts = append(parts, KeyString(k)+" = "+v.Map[k].String())
		}
		return "{ " + strings.Join(parts, ", ") + " }"
	}
	return ""
}

// Table 是一个 `[table]` 主体。
type Table struct {
	Values map[string]*Value
}

// Doc 是解码后的文档：表按 \x00 连接的路径段作键；数组表按文件顺序。
//
// 物理布局：
//
//	Doc
//	├── Tables map[string]*Table  // [table] 主体
//	└── Arrays map[string][]*Table // [[array]] 条目序列
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

// Decode 把 src 解析为 Doc。
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

// parseHeader 解析 `[a.b]` 或 `[[a.b]]` 并选择表。
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
		// 普通表不得与表数组冲突。
		if _, dup := p.doc.Arrays[key]; dup {
			return p.fail("table [%s] collides with array of tables", strings.Join(segs, "."))
		}
		p.cur = p.doc.table(key)
		p.curA = nil
		p.arrKy = ""
	}
	return nil
}

// parseDottedKey 把 `a.b."c.d".'e'` 解析为段。
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

// parsePair 解析 `key = value`。
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

// parseValue 在 p.pos 解析一个值（无前导空格）。
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
	case c == '{':
		return p.parseInline()
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

// parseBasicRaw 解析 "..." 字符串体（不支持多行）。
func (p *parser) parseBasicRaw() (string, error) {
	p.pos++ // 左引号
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

// parseLiteralRaw 解析 '...' 字符串体。
func (p *parser) parseLiteralRaw() (string, error) {
	p.pos++ // 左引号
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

// parseNumber 解析整数或浮点数。
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

// parseInline 解析单行 `{ k = v, ... }` 表（TOML 禁止内联表内换行；
// 点键像键值对一样被拒绝）。
func (p *parser) parseInline() (*Value, error) {
	p.pos++ // {
	out := &Value{Kind: Inline, Map: map[string]*Value{}}
	for {
		p.skipInlineSpace()
		if p.pos >= len(p.src) {
			return nil, p.fail("unterminated inline table")
		}
		if p.src[p.pos] == '}' {
			p.pos++
			return out, nil
		}
		segs, err := p.parseDottedKey()
		if err != nil {
			return nil, err
		}
		if len(segs) != 1 {
			return nil, p.fail("dotted keys are not supported in inline tables")
		}
		key := segs[0]
		p.skipInlineSpace()
		if p.pos >= len(p.src) || p.src[p.pos] != '=' {
			return nil, p.fail("expected '=' after key %q", key)
		}
		p.pos++
		p.skipInlineSpace()
		v, err := p.parseInlineValue()
		if err != nil {
			return nil, err
		}
		if _, dup := out.Map[key]; dup {
			return nil, p.fail("duplicate key %q", key)
		}
		out.Map[key] = v
		p.skipInlineSpace()
		if p.pos >= len(p.src) {
			return nil, p.fail("unterminated inline table")
		}
		if p.src[p.pos] == ',' {
			p.pos++
			continue
		}
		if p.src[p.pos] == '}' {
			p.pos++
			return out, nil
		}
		return nil, p.fail("expected ',' or '}' in inline table")
	}
}

// parseInlineValue 解析一个内联表值（允许嵌套内联表与数组）。
func (p *parser) parseInlineValue() (*Value, error) {
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
	case c == '{':
		return p.parseInline()
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

func (p *parser) skipInlineSpace() {
	for p.pos < len(p.src) && (p.src[p.pos] == ' ' || p.src[p.pos] == '\t' || p.src[p.pos] == '\r') {
		p.pos++
	}
}

// parseArray 解析可能多行的 [...] 值。
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
