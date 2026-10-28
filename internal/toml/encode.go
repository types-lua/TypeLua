package toml

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// Builder emits deterministic TOML for fixed schemas (manifests, locks).
//
// 设计者视角：Builder 是清单的“排字机”——调用方按固定 schema 依次调 Table/Str/Int/Bool，
// 产出字节确定的 TOML（锁文件 diff 可读）。障碍是 map 随机序：本机不收 map，只收定序调用，
// 顺序由调用方（配合 SortedKeys）定，同一输入恒同一字节。取舍是不做 schema 校验，
// 错序/重键由 mod 层保证，此处只管拼字。不变量：写入恒带尾换行；空文档 String() 为空。
// 排查现场：若锁文件 diff 飘，先看调用方是否按 SortedKeys 定序，次看是否混了 Raw 手拼。
type Builder struct {
	sb strings.Builder
}

// String returns the built document (ends with exactly one newline,
// or empty if nothing was written).
//
// 设计者视角：它是出厂前的“切纸刀”——TrimRight 去多余空行再补一 \n，让文件恒以单换行收尾，
// POSIX 友好、diff 不飘；空文档即空串，不硬补换行。取舍是每次现算，不缓存，调用方可续写后
// 再取。不变量：非空输出恒以单 \n 结尾、恒不以 \n\n 结尾。排查现场：若末尾多空行，
// 先看是否绕过本函数直读 sb，次看 Raw 是否塞了多余换行。
func (b *Builder) String() string {
	s := b.sb.String()
	if s == "" {
		return ""
	}
	return strings.TrimRight(s, "\n") + "\n"
}

// Table starts `[a.b."c"]` (segments quoted as needed).
//
// 设计者视角：它是“挂新抽屉牌”——segs 经 joinKey（逐段 KeyString，须引则引）拼径，
// 输出 `[径]` 另起一行。障碍是段含点/空格：裸拼会切错径，故须引。取舍是不记当前表，
// 重复挂牌的责任在调用方。不变量：输出恒为 `[`+径+`]`+\n。排查现场：若 Decode 回读
// 找不到表，先看段引号是否与 parseDottedKey 对得上（含点段必须引）。
func (b *Builder) Table(segs ...string) {
	b.sb.WriteString("[" + joinKey(segs) + "]\n")
}

// ArrayTable starts `[[a.b]]`.
//
// 设计者视角：它是“加一页数组表”——与 Table 同径规则，双括号表追加语义，
// 顺序即文件序（依赖声明序）。取舍是与 Table 共 joinKey，保证两态径拼写一致，
// 让 Decode 的 Tables/Arrays 按同 \x00 径归位。不变量：输出恒为 `[[`+径+`]]`+\n。
// 排查现场：若数组表被读成普通表，先看括号是单还是双，次看径是否与 Table 混用同名。
func (b *Builder) ArrayTable(segs ...string) {
	b.sb.WriteString("[[" + joinKey(segs) + "]]\n")
}

// Blank writes an empty line.
//
// 设计者视角：它是排版的“换气口”——表间一空行，人读不挤。取舍是 String() 会把尾部多空行
// 收成单换行，故表尾多 Blank 无害。不变量：恒写一 \n。排查现场：若空行丢了，先看是否被
// String() 的 TrimRight 收了尾（尾空行本就该收），段间空行不受影响。
func (b *Builder) Blank() {
	b.sb.WriteString("\n")
}

// Comment writes a `#` line.
//
// 设计者视角：它是文件头的“题字”——`# `+s 独占一行，常放版本与生成标记。
// 取舍是不转义 s（含换行会破行，调用方须保证单行）。不变量：输出恒为 `# `+s+\n。
// 排查现场：若注释破了回读，先看 s 是否含 \n（Decode 会当注释吃掉但行数变）。
func (b *Builder) Comment(s string) {
	b.sb.WriteString("# " + s + "\n")
}

// joinKey 把多段拼成点径（逐段 KeyString），是挂牌的粘合剂。
//
// 设计者视角：Table/ArrayTable 的共用尺——逐段判裸/引再以 "." 粘，保证含点段被引住。
// 取舍是空 segs 即空径（调用方不应如此调）。不变量：段数与点数恒差一。
// 排查现场：若径回读切散，先看 KeyString 的裸判与 isBareKeyChar 是否同口径。
func joinKey(segs []string) string {
	out := make([]string, len(segs))
	for i, s := range segs {
		out[i] = KeyString(s)
	}
	return strings.Join(out, ".")
}

// KeyString renders one key segment, quoting when necessary.
//
// 设计者视角：它是单段的“裸/引判决”——空串或含非裸字符（非 A-Z/a-z/0-9/_/-）即 Quote，
// 否则裸写，让 `foo-bar` 裸行、`a.b` 与 `a b` 自动穿衣。障碍是与解码侧同口径：
// 裸集必须与 isBareKeyChar 一致，否则编码裸写而解码切散。取舍是用 strconv.Quote
// （Go 双引号转义）而非 TOML 单引号，让输出恒可被 parseBasicRaw 回读。
// 不变量：输出恒可作键段回读。排查现场：若某键回读不到，先对两边裸集，次看 Quote
// 的转义是否被手改。
func KeyString(s string) string {
	bare := true
	if s == "" {
		bare = false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '_' || c == '-') {
			bare = false
			break
		}
	}
	if bare {
		return s
	}
	return strconv.Quote(s)
}

// Raw writes a preformatted line verbatim.
//
// 设计者视角：它是排字机的“后门”——调用方拼好的整行直塞，不经 KeyString/Quote，
// 供固定 schema 的异形行应急。障碍是后门破确定性：手拼顺序、手拼引号错即 diff 飘，
// 故只许定形调用，不许循环拼 map。不变量：原样写，不补换行（调用方自带 \n）。
// 排查现场：若某行回读失败，先看 Raw 是否漏了换行或引号，改走 Str/Int/Bool 定道。
func (b *Builder) Raw(s string) {
	b.sb.WriteString(s)
}

// Str writes `key = "value"`.
//
// 设计者视角：它是字符串键值的“定式”——键经 KeyString、值经 Quote，一行 `k = "v"`。
// 取舍是值恒双引号（不用单引号），让 parseBasicRaw/parseLiteralRaw 任一都能回读，
// 换行转义由 Quote 代劳。不变量：输出恒单行。排查现场：若值含换行后破行，先看 Quote
// 是否被绕过（本函数恒 Quote，不会破）。
func (b *Builder) Str(key, val string) {
	fmt.Fprintf(&b.sb, "%s = %s\n", KeyString(key), strconv.Quote(val))
}

// Int writes `key = 123`.
//
// 设计者视角：它是整数键值的“定式”——十进制直写，与 parseNumber 的 Integer 口径同源，
// 让锁文件的版本号、计数往返无损。取舍是不写千分位 '_'，让人读略挤但解析最稳。
// 不变量：输出恒可被 ParseInt(10,64) 回读。排查现场：若大数回读变浮点，先看读侧 Kind
// 是 Integer 还是 Float（`1.0` 与 `1` 两品），次看是否手拼了指数。
func (b *Builder) Int(key string, val int64) {
	fmt.Fprintf(&b.sb, "%s = %d\n", KeyString(key), val)
}

// Bool writes `key = true`.
//
// 设计者视角：它是布尔键值的“定式”——true/false 小写直写，与 TOML 关键字同形，
// 解码侧 peekAt 守卫可原样认回。取舍是 if/else 双写而非 %v，拼写一眼可审。
// 不变量：输出恒为 `k = true` 或 `k = false` 单行。排查现场：若 `True` 回读失败，
// 先看大小写（TOML 只认小写），本函数永出小写。
func (b *Builder) Bool(key string, val bool) {
	if val {
		fmt.Fprintf(&b.sb, "%s = true\n", KeyString(key))
	} else {
		fmt.Fprintf(&b.sb, "%s = false\n", KeyString(key))
	}
}

// StrArray writes `key = ["a", "b"]`.
//
// 设计者视角：它是字符串数组的“定式”——逐元 Quote 再以 ", " 粘，空数组即 `k = []`，
// 单行出街，依赖列表最常用。障碍是元素含引号/换行：Quote 一律转义，单行不破。
// 取舍是只收 []string（不定式数组另走 Raw 或 Value.String），让常用路最短。
// 不变量：输出恒单行、恒可被 parseArray 回读。排查现场：若数组 diff 飘，先看 vals
// 是否已排序（本函数保序不排序，排序是调用方的活）。
func (b *Builder) StrArray(key string, vals []string) {
	parts := make([]string, len(vals))
	for i, v := range vals {
		parts[i] = strconv.Quote(v)
	}
	fmt.Fprintf(&b.sb, "%s = [%s]\n", KeyString(key), strings.Join(parts, ", "))
}

// SortedKeys returns sorted map keys (deterministic emission).
//
// 设计者视角：它是确定性输出的“点名册”——Go map 迭代随机，直接 range 拼即每次字节不同，
// 先 SortedKeys 定序再拼即稳。泛型收 map[string]V 任值类型，内联表渲染（Value.String）
// 与 Builder 调用方共用此尺。取舍是每次分配+排序（n log n），换锁文件/文档 diff 稳定。
// 不变量：返回为升序新切片，原 map 不动；空 map 返空非 nil。排查现场：若输出仍飘，
// 先看是否全链路都经本函数排序（漏一处即飘），次看键是否含大小写（排序按字节序）。
func SortedKeys[V any](m map[string]V) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}
