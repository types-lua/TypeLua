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

// bracePart returns the first {...} substring (inclusive).
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

// typedefTrailingName returns the trailing identifier of a typedef (after }).
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
	// Reconstruct "Name { ... }" for enum parsing.
	name := typedefTrailingName(t)
	return name + " " + bracePart(t)
}

func cleanIdent(s string) string {
	s = strings.TrimSpace(s)
	s = strings.Trim(s, "*() [],;")
	s = strings.TrimSpace(s)
	// Strip array suffix: `buf[64]` -> `buf`.
	if i := strings.Index(s, "["); i >= 0 {
		s = strings.TrimSpace(s[:i])
	}
	// Strip bitfield: `x:4` -> `x`.
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

// parseStructDecl handles `struct Name {...}` and `typedef struct [Tag] {...} Alias`.
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
			// typedef struct Tag {...}; (no alias) -> use Tag.
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
	// struct Tag {...} — Tag is the ident after struct (if not `{`).
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
		// Ensure Tag precedes `{` (not trailing alias confusion).
		before := rest[:strings.Index(rest, "{")]
		bf := strings.Fields(before)
		if len(bf) > 0 {
			return cleanIdent(bf[len(bf)-1])
		}
	}
	return ""
}

// parseStructFields splits struct body on ';' at depth 0.
func parseStructFields(inner string, known map[string]bool) []string {
	var out []string
	for _, stmt := range splitFields(inner) {
		stmt = strings.TrimSpace(stmt)
		if stmt == "" {
			continue
		}
		// Nested struct/union/enum definitions: opaque.
		if strings.Contains(stmt, "{") {
			out = append(out, "-- nested: "+oneLine(stmt))
			continue
		}
		// Multiple declarators: `int x, y[4];`
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

// splitDeclarators separates `int x, *y[4]` into base `int` + declarators.
func splitDeclarators(stmt string) (string, []string) {
	// Split on ',' at depth 0 (brackets/parens).
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
	// Base type = first declarator minus its trailing name.
	first := parts[0]
	name := declaratorName(first)
	base := strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(first), strings.TrimSpace(name)))
	// If name extraction consumed stars (e.g. `*p`), restore them to base?
	// declaratorName strips `*`; suffix() recovers per-declarator stars.
	if base == "" {
		// Statement like `unsigned;` (anonymous bitfield width?) — skip.
		return "", nil
	}
	return base, parts
}

func declaratorName(d string) string {
	d = strings.TrimSpace(d)
	// Function declarator inside struct (function pointer field)?
	// `int (*cb)(int)` -> name `cb`.
	if i := strings.Index(d, "(*"); i >= 0 {
		rest := d[i+2:]
		if j := strings.Index(rest, ")"); j >= 0 {
			return cleanIdent(rest[:j])
		}
	}
	// Normal: trailing identifier (strip *, arrays, bitfield, initializer).
	// Remove initializer: `x = 5` -> `x`.
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
	// Recover per-declarator pointer/array marks for mapping.
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

// parseEnumDecl handles `enum Name { A, B = 5, ... }`.
func parseEnumDecl(t string) (string, string) {
	brace := bracePart(t)
	if brace == "" {
		return "", ""
	}
	// Name precedes `{`.
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
		// Strip trailing comments already gone; handle `A = expr`.
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
			// Complex expr (1<<3): keep auto numbering, note original.
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
	// Hex/octal/decimal via ParseInt base 0; underscores allowed in C23?
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

// parsePrototype handles `<ret> <name>(<params>)`.
func parsePrototype(t string, known map[string]bool) string {
	open := strings.Index(t, "(")
	close := strings.LastIndex(t, ")")
	if open < 0 || close < 0 || close < open {
		return ""
	}
	head := strings.TrimSpace(t[:open])
	params := strings.TrimSpace(t[open+1 : close])
	// Head: last ident is function name (strip `*` for returning pointer).
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
	// Params.
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

// ptrMark recovers a `*` between ret type and name (e.g. `char *foo`).
func ptrMark(t string, open int, fname, head string) string {
	// head already excludes fname; check gap between ret tokens and fname.
	i := strings.LastIndex(head, fname)
	if i < 0 {
		return ""
	}
	gap := head
	_ = gap
	// Look at raw text before '(' for '*' after ret type.
	pre := strings.TrimSpace(t[:open])
	if strings.Contains(pre, "*") {
		// If mapped type already includes *, mapCType handles it via suffix;
		// ensure a star is present in the type string passed to mapCType.
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
	// Function pointer param: `int (*cb)(int)` -> cdata? Better: function type.
	if strings.Contains(pm, "(*") {
		if name, ft := parseFuncPtrTypedef("typedef "+pm+";", known); name != "" {
			_ = name
			// Named callback type inline: use the function shape directly.
			// `typedef` wrapper produced `type X = (…)->R`; inline the RHS.
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
	// `void` single already handled; trailing ident is name unless type-only.
	// Heuristic: if pm has >=2 idents, last is name; else synthesize.
	name := ""
	typ := pm
	if len(toks) >= 2 {
		name = toks[len(toks)-1]
		// Avoid swallowing type keywords as names (`unsigned int` -> int is type).
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
	// Preserve pointer/array marks from full declarator.
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

// parseFuncPtrTypedef handles `typedef R (*Name)(P...);` -> (name, "type ...").
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

// mapCType maps a C type spelling to a TypeLua type.
func mapCType(raw string, known map[string]bool) string {
	t := strings.TrimSpace(raw)
	if t == "" {
		return "any"
	}
	// Array declarator already folded to `[]` suffix.
	isArray := strings.Contains(t, "[") || strings.HasSuffix(t, "[]")
	t = strings.ReplaceAll(t, "[]", "")
	t = strings.TrimSpace(t)
	isPtr := strings.Contains(t, "*")
	t = strings.ReplaceAll(t, "*", " ")
	t = spaceRe.ReplaceAllString(strings.TrimSpace(t), " ")
	low := strings.ToLower(t)
	// Strip qualifiers token-wise (substring replace would corrupt
	// `unsigned` via inner `signed`; see unsigned-long regression).
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
	// char* -> string (before generic pointer rule).
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
	// Nominal: single known identifier stays; unknown -> any.
	f := strings.Fields(t)
	if len(f) == 1 {
		id := cleanIdent(f[0])
		if identRe.MatchString(id) {
			if known[id] {
				return id
			}
			// Common scalar typedefs without header: assume integer-ish?
			// Stay permissive (any) to avoid false TL3103.
			return "any"
		}
	}
	// Multi-token unknown (e.g. `unsigned mytype`): last ident nominal?
	toks := identFind.FindAllString(t, -1)
	if len(toks) > 0 {
		last := toks[len(toks)-1]
		if known[last] {
			return last
		}
	}
	return "any"
}
