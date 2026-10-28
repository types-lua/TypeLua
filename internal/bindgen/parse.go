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
	// 为枚举解析重构 "Name { ... }"。
	name := typedefTrailingName(t)
	return name + " " + bracePart(t)
}

func cleanIdent(s string) string {
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
	t = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(t), ";"))
	f := strings.Fields(t)
	if len(f) == 2 && (f[0] == "struct" || f[0] == "enum" || f[0] == "union") && identRe.MatchString(f[1]) {
		return f[1]
	}
	return ""
}

// parseStructDecl 处理 `struct Name {...}` 与 `typedef struct [Tag] {...} Alias`。
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
	switch s {
	case "void", "char", "short", "int", "long", "float", "double", "signed", "unsigned", "const", "volatile", "struct", "enum", "union", "bool", "size_t":
		return true
	}
	return false
}

// parseFuncPtrTypedef 处理 `typedef R (*Name)(P...);` -> (name, "type ...")。
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
