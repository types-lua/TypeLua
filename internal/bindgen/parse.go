package bindgen

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

var (
	spaceRe   = regexp.MustCompile(`\s+`)
	identFind = regexp.MustCompile(`[A-Za-z_][A-Za-z0-9_]*`)
)

// bracePart 返回第一个 {...} 子串（含括号）。
//
// 设计者视角：首 `{` 起计数配对取体，保嵌套 struct 不早断。障碍是失配：返 "" 上游
// 跳。排查现场：体空先查配对。
func bracePart(s string) string {
	start := strings.Index(s, "{")
	if start < 0 {
		return ""
	}
	depth := 0
	for i := start; i < len(s); i++ {
		if s[i] == '{' {
			depth++
		} else if s[i] == '}' {
			depth--
			if depth == 0 {
				return s[start : i+1]
			}
		}
	}
	return ""
}

// typedefTrailingName 返回 typedef 的尾随标识符（} 之后）。
//
// 设计者视角：`}` 后末标识即别名（去 `*` 空格），供 typedef struct/enum 命名。
// 障碍是无别名（`typedef struct Tag{...};`）：返 "" 上游用 Tag。排查现场：别名空
// 先查 `}` 后。
func typedefTrailingName(t string) string {
	end := strings.LastIndex(t, "}")
	if end < 0 {
		return ""
	}
	rest := strings.TrimSpace(t[end+1:])
	rest = strings.Trim(rest, "* ")
	f := strings.Fields(rest)
	if len(f) == 0 {
		return ""
	}
	return cleanIdent(f[len(f)-1])
}

func afterBraceTypedefName(t, _ string) string {
	// 设计者视角：拼 `Name { ... }` 供 enum 复用解析（见 handleTypedef）。障碍是空名：
	// 拼空由下流拒。排查现场：枚举名错先查此拼。
	// 为枚举解析重构 "Name { ... }"。
	name := typedefTrailingName(t)
	return name + " " + bracePart(t)
}

func cleanIdent(s string) string {
	// 设计者视角：去饰（星/括号/逗号/数组/位域）取纯名，供 Tag/别名/声明符。障碍是
	// `buf[64]`/`x:4`：截之。排查现场：名带饰先查此。
	s = strings.TrimSpace(s)
	s = strings.Trim(s, "*() [],;")
	s = strings.TrimSpace(s)
	// 剥离数组后缀：`buf[64]` -> `buf`。
	if i := strings.Index(s, "["); i >= 0 {
		s = strings.TrimSpace(s[:i])
	}
	// 剥离位域：`x:4` -> `x`。
	if i := strings.Index(s, ":"); i >= 0 {
		s = strings.TrimSpace(s[:i])
	}
	return s
}

func forwardDeclName(t string) string {
	// 设计者视角：`struct Foo;` 双词形取名前向名，供 cdata 别名。障碍是多词：非双词
	// 返 ""。排查现场：前向未登记先查词数。
	t = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(t), ";"))
	f := strings.Fields(t)
	if len(f) == 2 && (f[0] == "struct" || f[0] == "enum" || f[0] == "union") && identRe.MatchString(f[1]) {
		return f[1]
	}
	return ""
}

// parseStructDecl 处理 `struct Name {...}` 与 `typedef struct [Tag] {...} Alias`。
//
// 设计者视角：取体→定名（typedef 尾名优先、退 Tag；非 typedef 取 Tag）→逐字段映类型
// 拼 `type N = {...}`。障碍是匿名（名非法）：返 "" 跳。候选：匿 struct 编号 vs 跳；
// 选跳保名义。排查现场：struct 丢失先查名。
func parseStructDecl(t string, isTypedef bool, known map[string]bool) (string, string) {
	brace := bracePart(t)
	if brace == "" {
		return "", ""
	}
	inner := strings.TrimSpace(brace[1 : len(brace)-1])
	var name string
	if isTypedef {
		name = typedefTrailingName(t)
		if name == "" {
			// typedef struct Tag {...};（无别名）-> 使用 Tag。
			name = structTagName(t)
		}
	} else {
		name = structTagName(t)
	}
	if !identRe.MatchString(name) {
		return "", ""
	}
	fields := parseStructFields(inner, known)
	var b strings.Builder
	fmt.Fprintf(&b, "type %s = {\n", name)
	for _, f := range fields {
		fmt.Fprintf(&b, "  %s,\n", f)
	}
	b.WriteString("}")
	return name, b.String()
}

func structTagName(t string) string {
	// 设计者视角：取 `struct` 后 `{` 前末标识为 Tag（防尾别名混）。障碍是匿名（直 `{`）：
	// 返 ""。排查现场：Tag 错先查 `{` 前截。
	// struct Tag {...} — Tag 是 struct 后的标识符（若非 `{`）。
	rest := strings.TrimSpace(t)
	rest = strings.TrimPrefix(rest, "typedef")
	rest = strings.TrimSpace(rest)
	rest = strings.TrimPrefix(rest, "struct")
	rest = strings.TrimSpace(rest)
	if strings.HasPrefix(rest, "{") || rest == "" {
		return ""
	}
	f := strings.Fields(rest)
	if len(f) > 0 && identRe.MatchString(cleanIdent(f[0])) && !strings.HasPrefix(strings.TrimSpace(rest), "{") {
		// 确保 Tag 在 `{` 之前（避免尾随别名混淆）。
		before := rest[:strings.Index(rest, "{")]
		bf := strings.Fields(before)
		if len(bf) > 0 {
			return cleanIdent(bf[len(bf)-1])
		}
	}
	return ""
}

// parseStructFields 在深度 0 按 ';' 拆分结构体体。
//
// 设计者视角：体零深切分→每句 splitDeclarators 展多声明符→名+映类型拼 `n: T`，
// 未知名按值 any。障碍是位域/数组：名已净、型映 cdata。排查现场：字段缺先查切分。
func parseStructFields(inner string, known map[string]bool) []string {
	var out []string
	for _, stmt := range splitFields(inner) {
		stmt = strings.TrimSpace(stmt)
		if stmt == "" {
			continue
		}
		// 嵌套 struct/union/enum 定义：不透明。
		if strings.Contains(stmt, "{") {
			out = append(out, "-- nested: "+oneLine(stmt))
			continue
		}
		// 多声明符：`int x, y[4];`
		base, declarators := splitDeclarators(stmt)
		if base == "" || len(declarators) == 0 {
			continue
		}
		for _, d := range declarators {
			fname := cleanIdent(declaratorName(d))
			if !identRe.MatchString(fname) || fname == "" {
				continue
			}
			ctype := strings.TrimSpace(base + " " + declaratorSuffix(d))
			out = append(out, fmt.Sprintf("%s: %s", fname, mapCType(ctype, known)))
		}
	}
	return out
}

func splitFields(s string) []string {
	// 设计者视角：struct 体零深 `;` 切（括号/花括号计数），供字段展。障碍是函数指针
	// 字段含逗号：此处只切 `;`，逗号留 splitDeclarators。排查现场：字段粘连先查 depth。
	var out []string
	depth := 0
	start := 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '{', '(':
			depth++
		case '}', ')':
			if depth > 0 {
				depth--
			}
		case ';':
			if depth == 0 {
				out = append(out, s[start:i])
				start = i + 1
			}
		}
	}
	return out
}

// splitDeclarators 把 `int x, *y[4]` 拆为基类型 `int` + 声明符。
//
// 设计者视角：顶层逗号切（括号计数），首词为基、余为声明符（名+后缀），供逐映。
// 障碍是 `,` 在括号内（函数指针）：计数避。排查现场：多声明符丢先查切分。
func splitDeclarators(stmt string) (string, []string) {
	// 在深度 0（括号/方括号）处按 ',' 拆分。
	var parts []string
	depth := 0
	start := 0
	for i := 0; i < len(stmt); i++ {
		switch stmt[i] {
		case '(', '[', '{':
			depth++
		case ')', ']', '}':
			if depth > 0 {
				depth--
			}
		case ',':
			if depth == 0 {
				parts = append(parts, strings.TrimSpace(stmt[start:i]))
				start = i + 1
			}
		}
	}
	parts = append(parts, strings.TrimSpace(stmt[start:]))
	if len(parts) == 0 {
		return "", nil
	}
	// 基类型 = 第一个声明符去掉尾随名。
	first := parts[0]
	name := declaratorName(first)
	base := strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(first), strings.TrimSpace(name)))
	// 若名字提取消耗了星号（如 `*p`），恢复到基类型？
	// declaratorName 剥离 `*`；suffix() 恢复每个声明符的星号。
	if base == "" {
		// 类似 `unsigned;` 的语句（匿名位域宽度？）——跳过。
		return "", nil
	}
	return base, parts
}

func declaratorName(d string) string {
	// 设计者视角：`(*cb)` 取括内名，余取尾标识（去初始化器/饰）。障碍是 `x=5`：
	// 先截 `=`。排查现场：名错先查分支。
	d = strings.TrimSpace(d)
	// 结构体内的函数声明符（函数指针字段）？
	// `int (*cb)(int)` -> 名字 `cb`。
	if i := strings.Index(d, "(*"); i >= 0 {
		rest := d[i+2:]
		if j := strings.Index(rest, ")"); j >= 0 {
			return cleanIdent(rest[:j])
		}
	}
	// 普通：尾随标识符（剥离 *、数组、位域、初始化器）。
	// 去掉初始化器：`x = 5` -> `x`。
	if i := strings.Index(d, "="); i >= 0 {
		d = strings.TrimSpace(d[:i])
	}
	toks := identFind.FindAllString(d, -1)
	if len(toks) == 0 {
		return ""
	}
	return toks[len(toks)-1]
}

func declaratorSuffix(d string) string {
	// 设计者视角：缀 `*`/`[]` 供 mapCType 识指针/数组。障碍是函数指针双星：补之。
	// 排查现场：指针误 any 先查缀。
	// 恢复每个声明符的指针/数组标记以供映射。
	s := ""
	if strings.Contains(d, "*") {
		s += " *"
	}
	if strings.Contains(d, "[") {
		s += " []"
	}
	if strings.Contains(d, "(") && strings.Contains(d, "*") {
		s += " *"
	}
	return s
}

// parseEnumDecl 处理 `enum Name { A, B = 5, ... }`。
//
// 设计者视角：`{` 前末词为名，逗号切员（`A=expr` 显值经 evalSimpleInt，自增跟随），
// 拼 `type N = "A" | ...` 联合（TypeLua 枚举式）。障碍是匿名：名非法即跳；复杂值：
// 原样串。候选：数值 vs 字串联合；选联合可读。排查现场：成员丢先查逗号切。
func parseEnumDecl(t string) (string, string) {
	brace := bracePart(t)
	if brace == "" {
		return "", ""
	}
	// 名字在 `{` 之前。
	before := strings.TrimSpace(t[:strings.Index(t, "{")])
	f := strings.Fields(before)
	name := ""
	if len(f) >= 2 {
		name = cleanIdent(f[len(f)-1])
	}
	if !identRe.MatchString(name) {
		return "", ""
	}
	inner := brace[1 : len(brace)-1]
	var members []string
	auto := 0
	for _, part := range strings.Split(inner, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		// 剥离尾随注释（已处理）；处理 `A = expr`。
		eq := strings.Index(part, "=")
		mname := strings.TrimSpace(part)
		mval := ""
		if eq >= 0 {
			mname = strings.TrimSpace(part[:eq])
			mval = strings.TrimSpace(part[eq+1:])
		}
		mname = cleanIdent(mname)
		if !identRe.MatchString(mname) {
			continue
		}
		if mval == "" {
			members = append(members, fmt.Sprintf("%s = %d", mname, auto))
			auto++
			continue
		}
		if v, ok := evalSimpleInt(mval); ok {
			members = append(members, fmt.Sprintf("%s = %d", mname, v))
			auto = v + 1
		} else {
			// 复杂表达式（1<<3）：保持自动编号，记录原值。
			members = append(members, fmt.Sprintf("%s = %d", mname, auto))
			auto++
		}
	}
	var b strings.Builder
	fmt.Fprintf(&b, "enum %s {\n", name)
	for _, m := range members {
		fmt.Fprintf(&b, "  %s,\n", m)
	}
	b.WriteString("}")
	return name, b.String()
}

func evalSimpleInt(expr string) (int, bool) {
	// 设计者视角：去括号符→符号→下划线→ParseInt(0) 容十六/八/十，供枚举自增跟随。
	// 障碍是表达式（`1<<2`）： false 上游原样串。排查现场：自增值错先查此。
	expr = strings.TrimSpace(expr)
	expr = strings.Trim(expr, "() ")
	neg := false
	if strings.HasPrefix(expr, "-") {
		neg = true
		expr = strings.TrimSpace(expr[1:])
	} else if strings.HasPrefix(expr, "+") {
		expr = strings.TrimSpace(expr[1:])
	}
	// 通过 ParseInt base 0 处理十六/八/十进制；C23 允许下划线？
	expr = strings.ReplaceAll(expr, "_", "")
	v, err := strconv.ParseInt(expr, 0, 64)
	if err != nil {
		return 0, false
	}
	if neg {
		v = -v
	}
	return int(v), true
}

// parsePrototype 处理 `<ret> <name>(<params>)`。
//
// 设计者视角：末标识为名、余为返（补 `*` 经 ptrMark），`void`/空即无参，余 splitParams
// 逐映（`void` 吞、`...` 变 `...any`），拼 `name(...): ret` 方法串。障碍是返回指针
// 空格（`char *foo`）：ptrMark 恢复。候选：严格 vs 退 any；选退 any。不变量：
// 失败返 ""。排查现场：原型丢先查括号，类型 any 先查 map。
func parsePrototype(t string, known map[string]bool) string {
	open := strings.Index(t, "(")
	close := strings.LastIndex(t, ")")
	if open < 0 || close < 0 || close < open {
		return ""
	}
	head := strings.TrimSpace(t[:open])
	params := strings.TrimSpace(t[open+1 : close])
	// Head：最后一个标识符为函数名（返回指针时剥离 `*`）。
	toks := identFind.FindAllString(head, -1)
	if len(toks) == 0 {
		return ""
	}
	fname := toks[len(toks)-1]
	if !identRe.MatchString(fname) {
		return ""
	}
	retRaw := strings.TrimSpace(head[:strings.LastIndex(head, fname)])
	retPtr := strings.Contains(retRaw, "*") || strings.HasPrefix(strings.TrimSpace(head[len(retRaw):]), "*")
	_ = retPtr
	ret := mapCType(strings.TrimSpace(retRaw+ptrMark(t, open, fname, head)), known)
	if ret == "" {
		ret = "any"
	}
	// 参数。
	if params == "" || params == "void" {
		return fmt.Sprintf("%s(): %s", fname, ret)
	}
	var out []string
	idx := 0
	for _, pm := range splitParams(params) {
		pm = strings.TrimSpace(pm)
		if pm == "" {
			continue
		}
		if pm == "..." {
			out = append(out, "...args: any[]")
			continue
		}
		pname, ptype := splitParam(pm, idx, known)
		out = append(out, fmt.Sprintf("%s: %s", pname, ptype))
		idx++
	}
	return fmt.Sprintf("%s(%s): %s", fname, strings.Join(out, ", "), ret)
}

// ptrMark 恢复返回类型与名字之间的 `*`（如 `char *foo`）。
//
// 设计者视角：`(` 前原文含 `*` 而 head 无即补 ` *` 供映射。障碍是空格切断：
// 原文判。排查现场：`char*` 变 any 先查此。
func ptrMark(t string, open int, fname, head string) string {
	// head 已排除 fname；检查返回 token 与 fname 之间的间隙。
	i := strings.LastIndex(head, fname)
	if i < 0 {
		return ""
	}
	gap := head
	_ = gap
	// 查看 '(' 前的原始文本，寻找返回类型后的 '*'。
	pre := strings.TrimSpace(t[:open])
	if strings.Contains(pre, "*") {
		// 若映射类型已含 *，mapCType 通过后缀处理；
		// 确保传给 mapCType 的类型字符串含星号。
		if !strings.Contains(head, "*") {
			return " *"
		}
	}
	return ""
}

func splitParams(s string) []string {
	// 设计者视角：参列零深逗号切（括号计数），供逐映。障碍是函数指针参含逗号：
	// depth 避。排查现场：参粘连先查 depth。
	var out []string
	depth := 0
	start := 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '(', '[', '{':
			depth++
		case ')', ']', '}':
			if depth > 0 {
				depth--
			}
		case ',':
			if depth == 0 {
				out = append(out, s[start:i])
				start = i + 1
			}
		}
	}
	out = append(out, s[start:])
	return out
}

func splitParam(pm string, idx int, known map[string]bool) (string, string) {
	// 设计者视角：`...`→`...args: any[]`，函数指针 `(*cb)` 内联函数形（名缺编 argN），
	// 余末标识为名、余映类型（`void` 吞）。障碍是无名参（`int`）：编 argN。候选：
	// cdata vs 函数形；选函数形精确。排查现场：回调变 cdata 先查内联。
	pm = strings.TrimSpace(pm)
	if pm == "..." {
		return "...args", "any[]"
	}
	// 函数指针参数：`int (*cb)(int)` -> cdata？更好：函数类型。
	if strings.Contains(pm, "(*") {
		if name, ft := parseFuncPtrTypedef("typedef "+pm+";", known); name != "" {
			_ = name
			// 命名回调类型内联：直接使用函数形状。
			// `typedef` 包装产生 `type X = (…)->R`；内联 RHS。
			rhs := ft
			if i := strings.Index(rhs, "="); i >= 0 {
				rhs = strings.TrimSpace(rhs[i+1:])
			}
			cbName := funcPtrName(pm)
			if cbName == "" {
				cbName = fmt.Sprintf("arg%d", idx)
			}
			return cbName, rhs
		}
		return fmt.Sprintf("arg%d", idx), "cdata"
	}
	toks := identFind.FindAllString(pm, -1)
	if len(toks) == 0 {
		return fmt.Sprintf("arg%d", idx), "any"
	}
	// `void` 单独已处理；尾随标识符为名字，除非仅类型。
	// 启发式：pm 有 >=2 个标识符时，最后为名字；否则合成。
	name := ""
	typ := pm
	if len(toks) >= 2 {
		name = toks[len(toks)-1]
		// 避免把类型关键字当作名字（`unsigned int` -> int 是类型）。
		if isTypeKeyword(name) && len(toks) == 2 {
			name = fmt.Sprintf("arg%d", idx)
		} else {
			typ = strings.TrimSpace(pm[:strings.LastIndex(pm, name)])
			if strings.TrimSpace(typ) == "" {
				typ = pm
				name = fmt.Sprintf("arg%d", idx)
			}
		}
	} else {
		name = fmt.Sprintf("arg%d", idx)
		typ = pm
	}
	if !identRe.MatchString(name) {
		name = fmt.Sprintf("arg%d", idx)
	}
	// 从完整声明符保留指针/数组标记。
	full := pm
	if !strings.Contains(typ, "*") && strings.Contains(full, "*") {
		typ += " *"
	}
	if !strings.Contains(typ, "[") && strings.Contains(full, "[") {
		typ += " []"
	}
	return name, mapCType(typ, known)
}

func isTypeKeyword(s string) bool {
	// 设计者视角：C 类型关键字表，供参名/类型切分（`int` 后词为名）。障碍是 `size_t`：
	// 纳入。排查现场：无名参错切先查表。
	switch s {
	case "void", "char", "short", "int", "long", "float", "double", "signed", "unsigned", "const", "volatile", "struct", "enum", "union", "bool", "size_t":
		return true
	}
	return false
}

// parseFuncPtrTypedef 处理 `typedef R (*Name)(P...);` -> (name, "type ...")。
//
// 设计者视角：`(*` 定锚取返/名/参，返与参逐映拼 `(p)->r` 别名。障碍是形异：
// 非 `(*` 即 ""。候选：cdata vs 函数形；选函数形。排查现场：回调丢失先查锚。
func parseFuncPtrTypedef(t string, known map[string]bool) (string, string) {
	t = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(t), ";"))
	if !strings.HasPrefix(t, "typedef") {
		return "", ""
	}
	rest := strings.TrimSpace(strings.TrimPrefix(t, "typedef"))
	i := strings.Index(rest, "(*")
	if i < 0 {
		return "", ""
	}
	ret := strings.TrimSpace(rest[:i])
	j := strings.Index(rest[i:], ")")
	if j < 0 {
		return "", ""
	}
	name := cleanIdent(rest[i+2 : i+j])
	k := strings.Index(rest[i+j:], "(")
	l := strings.LastIndex(rest, ")")
	if k < 0 || l < 0 || !identRe.MatchString(name) {
		return "", ""
	}
	params := strings.TrimSpace(rest[i+j+k+1 : l])
	retT := mapCType(ret, known)
	var ps []string
	if params != "" && params != "void" {
		idx := 0
		for _, pm := range splitParams(params) {
			pn, pt := splitParam(strings.TrimSpace(pm), idx, known)
			ps = append(ps, fmt.Sprintf("%s: %s", pn, pt))
			idx++
		}
	}
	return name, fmt.Sprintf("type %s = (%s) -> %s", name, strings.Join(ps, ", "), retT)
}

func funcPtrName(pm string) string {
	// 设计者视角：参串 `(*name)` 取名供内联回调命名。障碍是无名：返 "" 上游编 argN。
	// 排查现场：回调名错先查此。
	i := strings.Index(pm, "(*")
	if i < 0 {
		return ""
	}
	rest := pm[i+2:]
	if j := strings.Index(rest, ")"); j >= 0 {
		return cleanIdent(rest[:j])
	}
	return ""
}

// mapCType 把 C 类型拼写映射为 TypeLua 类型。
//
// 设计者视角：限定符按 token 剥（防 `unsigned` 内 `signed` 子串误伤）、char* 先判
// string、余指针/数组判 cdata、标量表映 integer/number/boolean/void、单已知标识
// 留名义、余 any。障碍是未知按值类型：宁 any 不误报。候选：严 vs 宽；选宽。不变量：
// 空恒 any。排查现场：类型误报先查表，名义丢失先查 known。
func mapCType(raw string, known map[string]bool) string {
	t := strings.TrimSpace(raw)
	if t == "" {
		return "any"
	}
	// 数组声明符已折叠为 `[]` 后缀。
	isArray := strings.Contains(t, "[") || strings.HasSuffix(t, "[]")
	t = strings.ReplaceAll(t, "[]", "")
	t = strings.TrimSpace(t)
	isPtr := strings.Contains(t, "*")
	t = strings.ReplaceAll(t, "*", " ")
	t = spaceRe.ReplaceAllString(strings.TrimSpace(t), " ")
	low := strings.ToLower(t)
	// 按 token 剥离限定符（子串替换会因内含 `signed` 破坏
	// `unsigned`；见 unsigned-long 回归）。
	var qtoks []string
	for _, tok := range strings.Fields(low) {
		switch tok {
		case "const", "volatile", "signed", "struct", "enum", "union":
			continue
		default:
			qtoks = append(qtoks, tok)
		}
	}
	base := strings.TrimSpace(spaceRe.ReplaceAllString(strings.Join(qtoks, " "), " "))
	// char* -> string（在通用指针规则之前）。
	if isPtr && (base == "char" || base == "char const" || base == "const char") {
		return "string"
	}
	if isPtr || isArray {
		return "cdata"
	}
	switch base {
	case "", "void":
		return "void"
	case "bool", "_bool", "boolean":
		return "boolean"
	case "char", "signed char", "unsigned char", "short", "short int", "unsigned short", "unsigned short int",
		"int", "unsigned", "unsigned int", "long", "long int", "unsigned long", "unsigned long int",
		"long long", "long long int", "unsigned long long", "size_t", "ssize_t",
		"int8_t", "int16_t", "int32_t", "int64_t", "uint8_t", "uint16_t", "uint32_t", "uint64_t",
		"intptr_t", "uintptr_t", "ptrdiff_t":
		return "integer"
	case "float", "double", "long double":
		return "number"
	case "void *", "void*":
		return "cdata"
	}
	// 名义：单个已知标识符保留；未知 -> any。
	f := strings.Fields(t)
	if len(f) == 1 {
		id := cleanIdent(f[0])
		if identRe.MatchString(id) {
			if known[id] {
				return id
			}
			// 无头文件的常见标量 typedef：假设为整数类？
			// 保持宽容（any）以避免误报 TL3103。
			return "any"
		}
	}
	// 多 token 未知（如 `unsigned mytype`）：最后标识符名义？
	toks := identFind.FindAllString(t, -1)
	if len(toks) > 0 {
		last := toks[len(toks)-1]
		if known[last] {
			return last
		}
	}
	return "any"
}
