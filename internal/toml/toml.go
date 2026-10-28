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
//
// 设计者视角：Kind 是 TOML 值的“品类章”——String/Integer/Float/Boolean/Array/Inline 六品，
// 决定 Value 读哪个字段。障碍是 TOML  surface 形态多（".."、'..' 同品）：解码即归一，
// 双引号与单引号同落 String，调用方无需再分。取舍是无 Date/Duration 品，不支持即报错，
// 让清单格式严格可控。不变量：零值为 String（最常用品），但构造恒显式填 Kind。
// 排查现场：若取值错位（如读 Int 得 0），先看 Kind 是否与读写字段对齐。
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
//
// 设计者视角：Value 是“按章取货的货箱”——Kind 定读哪格，其余格置零，让调用方一次 switch
// 即取对。障碍是 Go 无代数类型：用扁平 struct + Kind 判别模拟联合，换零接口断言、零反射。
// 取舍是 Arr/Map 在标量品时为 nil，调用方须先看 Kind 再取格。不变量：Kind 与有效格一一对应；
// Inline 的 Map 永非 nil（空表即空 map）。排查现场：若 Float 取到 0，先确认 Kind 是 Float
// 还是 Integer（1 与 1.0 在 TOML 里是两品），次看 parseNumber 的 isFloat 分支。
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
//
// 设计者视角：它是货箱的“出库单打印机”——String 用 Quote 转义，整数十进制直出，
// 浮点 'g' 最短往返，布尔 true/false，数组 `[a, b]`，内联表按 SortedKeys 定序拼
// `{ k = v }`。障碍是 map 迭代随机：内联表必先 SortedKeys，否则锁文件每次字节不同。
// 取舍是单行渲染（不换行不缩进），多行美化留给 Builder。不变量：输出恒可被 Decode 回读
// （往返一致）；未知 Kind 返空串。排查现场：若内联表 diff 飘，先看是否绕过本函数手拼 map，
// 次看 SortedKeys 是否被改动。
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
//
// 设计者视角：它是表头的“抽屉”——Values 装该表下全部键值，Doc 按 \x00 径找抽屉。
// 取舍是值全为 *Value（指针），让数组/内联表共享零拷贝。不变量：Values 永非 nil
// （经 table() 建）。排查现场：若取键得 nil，先看查的是 Table.Values 还是 Doc.Arrays
// （[[array]] 另存），次看键大小写（TOML 键大小写敏感）。
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
//
// 设计者视角：Doc 是整份清单的“档案柜”——Tables 一径一抽屉，Arrays 一径一序列（保文件序，
// 依赖声明顺序即构建顺序）。障碍是点径含 "."（如 [a."b.c"]）：若用 "." Join 会与分段撞车，
// 故用 \x00（永不出现在键里）作内部分隔，显示时才 Join(".")。取舍是超集表自动建
// （table() 懒建），重复 [table] 则报错，[[array]] 同径可追加。不变量："" 径为顶表；
// Arrays 顺序恒为文件序。排查现场：若 [a.b] 与 [a."b"] 对不上，先看 parseDottedKey 的
// 引号段是否正确解出，次看 Join 用的是否为 \x00。
type Doc struct {
	Tables map[string]*Table
	Arrays map[string][]*Table
}

// table 取或建指定内径的表，是档案柜的懒建抽屉器。
//
// 设计者视角：好比找抽屉——有即拿，无即现钉一个（Values 空 map），让顶表与超集表
// 无需预声明。取舍是调用方（parseHeader）负责判重（重复 [table] 报错），本函数只管
// “有则返、无则建”。不变量：返回永非 nil，Tables map 懒初始化。排查现场：若重复表未报错，
// 先看 parseHeader 的 dup 分支是否被绕过（直接调了本函数），次看径 Join 是否一致。
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

// parser 是单遍扫描的游标机：src 待扫、pos 磁头、line 行号、doc 档案柜、cur 当前抽屉。
//
// 设计者视角：cur 指当前表抽屉，curA/arrKy 记当前 [[array]] 元，line 只数换行。
// 取舍是手写递归下降而非生成器，错误信息可带 toml:行号。不变量：cur 永非 nil；
// 行号只增不减。排查现场：若键值进了错表，先看 parseHeader 切 cur 是否漏了 arrKy 清零。
type parser struct {
	src   string
	pos   int
	line  int
	doc   *Doc
	cur   *Table
	curA  *Table // current [[array]] element
	arrKy string // current [[array]] key ("" when in plain table)
}

// fail 构造带行号的解码错误（toml:<line>: ...），是全解析唯一的报错出口。
//
// 设计者视角：好比磁头卡住时报“第几行卡的”——调用方直接 return p.fail(...) 即停机，
// Decode 透出首错。取舍是只记首错（遇错即返），不聚多错，让清单错误定位极准。
// 不变量：返回的 error 永非 nil，恒带行号前缀。排查现场：若行号总差一，先看 line
// 是否从 1 起（Decode 初始化 1），次看换行计数是否漏了数组里的换行。
func (p *parser) fail(format string, args ...any) error {
	return fmt.Errorf("toml:%d: %s", p.line, fmt.Sprintf(format, args...))
}

// Decode 把 src 解析为 Doc。
//
// 设计者视角：它是清单的“总装线”——顶表先建（"" 径），再逐行分流：换行记行号、
// `#` 整行弃、`[` 进 parseHeader、`key` 进 parsePair，首错即停。障碍是行号：
// 主循环只数 \n，行内 \r 留给 skipSpaceInline 当空白，避免 \r\n 算两行。
// 取舍是遇错即返（不聚多错），让首错行号极准，改完再报下一个。不变量：成功则
// Doc 永非 nil；失败则 err 恒带 toml:行号。排查现场：若行号偏小，先看数组/内联里的
// 换行是否另计数（数组计、内联禁），次看 Decode 的 line 是否从 1 起。
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

// skipSpaceInline 跳过行内空白（空格/制表/回车），是键与值之间的吹尘枪。
//
// 设计者视角：\n 不在此吃（留给主循环记行号），\r 在此吃（\r\n 不重复计数）。
// 取舍是纯推进、无报错，可任意重入。不变量：返回时要么到尾，要么停在非空白。
// 排查现场：若 `key = value` 报缺 '='，先看本函数是否把 \n 误吃（应留）。
func (p *parser) skipSpaceInline() {
	for p.pos < len(p.src) && (p.src[p.pos] == ' ' || p.src[p.pos] == '\t' || p.src[p.pos] == '\r') {
		p.pos++
	}
}

// skipLine 丢弃到行尾（不吃 \n），是注释的碎纸机。
//
// 设计者视角：`#` 后整行不要，但 \n 本体留给主循环计数行，让行号不断。
// 不变量：停时要么到尾，要么停在 \n 上。排查现场：若注释后行号不增，先看调用后
// 是否回了主循环（主循环才 line++），次看是否有人在此多吃了 \n。
func (p *parser) skipLine() {
	for p.pos < len(p.src) && p.src[p.pos] != '\n' {
		p.pos++
	}
}

// parseHeader 解析 `[a.b]` 或 `[[a.b]]` 并选择表。
//
// 设计者视角：它是档案柜的“换抽屉手”——`[` 看单双（`[[` 即数组表），parseDottedKey 取径，
// `]`/`]]` 收边，行尾只许空白或注释。命中后切 cur：[[array]] 追加新 Table 保文件序，
// [table] 懒建但重名即错，且普通表撞数组表亦错（同径两态不许并存）。障碍是尾料：
// 头后若跟非空白非注释非换行（如 `[a] x`），必须报“头后有尾巴”而非吞下。
// 不变量：成功必切 cur 并清/置 arrKy；失败游标停在案发点。排查现场：若键值进了错表，
// 先看本函数切的 key（Join 用 \x00）与 parsePair 写的 cur 是否同径，次看 [[array]] 后
// 是否忘了 arrKy 置位导致后续误判。
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
//
// 设计者视角：它是表径的“切刀”——裸键（isBareKeyChar）直切，引号键经 parseBasicRaw/
// parseLiteralRaw 解出（含转义），点号分段。障碍是 `"c.d"` 里的点不是分隔：引号段整体
// 一段，Join 时才用 \x00 粘，避免与真分隔撞车。取舍是空段、尾点（如 `a.`）在此不特判，
// 下轮循环遇非法字符即报 invalid key，让报错收敛一处。不变量：成功段数 >=1，游标停在
// 段尾；失败返首错。排查现场：若 [a."b.c"] 找不到，先打印 segs 看是 [a b.c] 还是 [a b c]，
// 次看裸键是否误含 '-' 之外的符号（isBareKeyChar 只认 A-Z/a-z/0-9/_/-）。
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

// isBareKeyChar 报告是否为裸键字符（A-Z/a-z/0-9/_/-），是切键的刀口标准。
//
// 设计者视角：TOML 裸键只认此五类，其余一律走引号键，让 `a-b_9` 直切、`a.b` 必分段。
// 不变量：纯函数，与 parseDottedKey 的裸键分支同源。排查现场：若含 '.' 的键被切散，
// 先看是否忘了加引号（裸键永不含点），次看此处是否被放宽。
func isBareKeyChar(c byte) bool {
	return c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '_' || c == '-'
}

// parsePair 解析 `key = value`。
//
// 设计者视角：它是键值行的“装配工”——取键（parseDottedKey）、验 '='、取值（parseValue）、
// 验行尾（只许空白/注释/换行）、落抽屉（cur.Values，重复即错）五步。障碍是点键：
// `a.b = 1` 在完整 TOML 合法，但在此子集故意拒收（让用户改用 [table] 头），
// 把嵌套语义收敛到一处，报错即指路。取舍是行尾尾料零容忍（`k = 1 x` 即错），
// 让手误早暴露。不变量：成功必写 cur 一键；失败不写。排查现场：若点键被拒，先改
// `[table]` 头而非放宽本函数；若重复键未报错，先看 cur 是否切错了表。
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
//
// 设计者视角：它是值的“分诊台”——见引号进字符串、见 `[` 进数组、见 `{` 进内联表、
// 见 true/false（后须非裸键字符防 `truex` 误判）进布尔、见数字符号进 parseNumber，
// 其余报 invalid value。障碍是 true/false 的前缀陷阱：`truex` 不是布尔，必须用
// peekAt 看后一字节是否为裸键字符。取舍是数组/内联表在此（表外值）与 parseInlineValue
// （表内值）各有一套分诊，后者禁换行而前者经 parseValue 进数组可多行，口径分层清晰。
// 不变量：入口无前导空格（调用方已吹）；成功游标停值尾。排查现场：若 `true-ish` 被吃成
// 布尔，先查 peekAt 守卫，次看 HasPrefix 是否越界（本处用切片+守卫，尾部安全）。
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

// peekAt 越界安全的取字节（越界回 0），是布尔字面量的岗哨。
//
// 设计者视角：`true`/`false` 后一字节决定是否为独立词：越界即词尾（回 0 判非裸键即放行），
// 让尾部 `true` 不 panic。取舍是纯函数，不动游标。不变量：i 越界恒 0。
// 排查现场：若尾部布尔解析 panic，先看是否绕过本函数直取 s[i]。
func peekAt(s string, i int) byte {
	if i < len(s) {
		return s[i]
	}
	return 0
}

// parseBasicRaw 解析 "..." 字符串体（不支持多行）。
//
// 设计者视角：它是双引号串的“转义翻译机”——\n\t\r\b\f\\、\" 与 \uXXXX/\UXXXXXXXX
// 逐个译成 rune，其余反斜杠一律报 invalid escape，换行与尾部缺引号各有专错。
// 障碍有二：一是代理区（D800-DFFF）与超限（>10FFFF）必须拒，防非法 rune 潜入清单；
// 二是截断转义（`\u12` 尾部不足）须报 truncated 而非越界。取舍是不支持多行
// （见首即禁换行），让清单串保持单行可读。不变量：成功游标停闭合引号后；内容为解码后值。
// 排查现场：若中文写成 \u 后乱码，先看四/八位长度是否数对（\u 取 4，\U 取 8），
// 次看是否误把字面 backslash 当转义（字面请用单引号串）。
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
//
// 设计者视角：它是单引号串的“复印机”——无转义，所见即所得，`\` 只是普通字符，
// 最适写正则与 Windows 路径。障碍是换行与缺闭合：见 \n 即报单行禁换行，到尾无引号
// 即报未闭合，两错分开让用户一眼知修哪。取舍是整段切片直返（零 Builder），
// 换大段字面零拷贝。不变量：返回为原文切片（不解码）；游标停闭合引号后。
// 排查现场：若 `'a\nb'` 报换行错，改双引号 `"a\nb"` 或拆行数组，而非放宽本函数。
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
//
// 设计者视角：它是数字的“验钞机”——符号、数字（`_` 可作千分位）、小数点、指数四段顺验，
// 见 '.' 或 e/E 即定 Float，否则 Integer。障碍有三：一是 `+`/`-` 后无 digit（如孤 "+"）
// 必须报 invalid number；二是指数 `e` 后须至少一 digit（`1e` 非法）；三是下划线只助读，
// 进 ParseInt/ParseFloat 前整串去 '_'。取舍是不支持进制/日期（子集故意拒），
// 让清单数字只有十进制两品。不变量：Integer 进 int64，Float 进 float64；溢出即报 invalid。
// 排查现场：若 `1_000` 报错，先看去 '_' 是否在切 raw 之后（本处 ReplaceAll 在 Parse 前），
// 次看是否误把十六进制 `0x` 喂了进来（本子集不支持）。
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
//
// 设计者视角：它是花括号里的“小装配线”——`{` 开，循环取键验 '=' 取值（parseInlineValue，
// 可嵌套），逗号续、`}` 收，空表 `{}` 即返。障碍有二：一是 TOML 禁内联换行，
// 此处只吃 skipInlineSpace（无 \n），换行即报 expected ',' or '}'；二是点键在此亦拒，
// 与 parsePair 同策。取舍是重复键即错、尾逗号不许（`{a=1,}` 非法），让手误早爆。
// 不变量：返回的 Inline.Map 永非 nil；游标停 `}` 后。排查现场：若内联里想写多行，
// 改用 [table] 头；若 `{a.b=1}` 被拒，与键值点键同理，拆成嵌套 `{a={b=1}}`。
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
//
// 设计者视角：它是内联语境的“分诊台二号”——与 parseValue 同形，唯三处不同：
// 一是 `{` 可递归（嵌套内联），二是 `[` 进 parseArray（其内元素经 parseValue 可多行，
// 但括号仍在同一逻辑行开），三是禁换行由 skipInlineSpace 保证。布尔的 peekAt 守卫
// 与数字的 parseNumber 复用同一套。取舍是双分诊并存，让“表内禁换行/表外数组可多行”
// 的 TOML 规则各有归处。不变量：入口无前导行内空白之外的东西；失败不耗值。
// 排查现场：若内联数组里换行被拒，先确认走的是 parseInlineValue→parseArray→parseValue
// 链（数组内 skipArraySpace 许换行），而非内联直判。
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

// skipInlineSpace 跳过内联表内的行内空白（无换行），是小装配线的吹尘枪。
//
// 设计者视角：与 skipSpaceInline 同口径（空格/制表/\r），刻意不吃 \n，
// 让换行在此即现形为 expected ',' or '}'，把“TOML 禁内联换行”变成显错。
// 不变量：永不推进过 \n。排查现场：若内联换行报了怪错，先看是否该换 [table] 头。
func (p *parser) skipInlineSpace() {
	for p.pos < len(p.src) && (p.src[p.pos] == ' ' || p.src[p.pos] == '\t' || p.src[p.pos] == '\r') {
		p.pos++
	}
}

// parseArray 解析可能多行的 [...] 值。
//
// 设计者视角：它是数组的“传送带”——`[` 开后循环：skipArraySpace（许换行许注释）→
// 空即 `]` 收、否则 parseValue 取一元→再 skipArraySpace→逗号续、`]` 收，
// 尾逗号允许（`[1,]` 合法），空数组 `[]` 即返。障碍是换行计数：skipArraySpace 见 \n
// 即 line++，让数组跨行后报错行号仍准；注释亦可跨行（`#` 到行尾）。取舍是元素经
// parseValue（表外口径），故数组里可直接嵌内联表与多行数组。不变量：返回的 Array.Arr
// 保文件序；游标停 `]` 后。排查现场：若缺 `]` 报 unterminated，先看数组内串是否吞了
// 换行（单行串禁换行），次看注释后是否正确续行。
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

// skipArraySpace 跳过数组内的空白与注释（含换行并计数），是传送带的清道夫。
//
// 设计者视角：它是三类 skip 中唯一吃 \n 者——数组可多行，故 \n 在此 line++ 后放行；
// `#` 注释亦吃到行尾（不吃 \n 本体，留给下轮计数）。取舍是行号在此增、主循环亦增，
// 各管各的 \n，绝不 double count（同一 \n 只被一处吃）。不变量：停时要么到尾，
// 要么停在值/逗号/`]` 上。排查现场：若数组后行号偏大，先看 \n 是否被本函数与主循环
// 各数了一次（本处吃了就不该再回主循环数）。
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
