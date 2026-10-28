package lsp

import (
	"regexp"
	"sort"
	"strings"

	"encoding/json"
	"github.com/typelua/typelua/internal/checker"
	"github.com/typelua/typelua/internal/types"
)

// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：补全两路——成员位（`a.`/`a:b:`）走类型成员，余者走作用域名+关键字。
// 障碍是 any/unknown 基：成员不可列，退 plain；私成员须 insideOwner。候选：前缀
// 过滤客户端 vs 服务端；选服务端全列详见实现，排查看 Span。
// 恒返排序。排查现场：成员缺失先查基类型与私有权。

// ---------- completion ----------

var luaKeywords = []string{
	"and", "break", "do", "else", "elseif", "end", "false", "for",
	"function", "goto", "if", "in", "local", "nil", "not", "or",
	"repeat", "return", "then", "true", "until", "while",
	"type", "interface", "enum", "class", "as", "extends", "export",
	"declare", "super", "self", "macro", "infer", "static", "private",
	"public", "protected", "abstract", "implements", "readonly",
}

func completionKind(kind string) int {
	// 设计者视角：kind 译 LSP 数字详见实现，排查看 Span。
	// 障碍是客户端图标依赖数字：须稳。排查现场：图标错先查此表。
	switch kind {
	case "function":
		return 3
	case "method":
		return 2
	case "field":
		return 5
	case "member":
		return 12
	case "keyword":
		return 14
	case "class":
		return 7
	case "interface", "type":
		return 8
	case "enum":
		return 13
	case "parameter", "variable":
		return 6
	default:
		return 6
	}
}

var dotCompleteRe = regexp.MustCompile(`([A-Za-z_][A-Za-z0-9_.:]*)([.:])\s*$`)

// memberCompletion answers `a.` / `a.b:` completion from the base type:
// a single identifier or a member chain (`a.b.c.`). Private members show
// 排查看 Span/诊断，如 `local x=1` 错圈整句。
// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：成员补全四步——行尾正则嗅 `x./:`，链按 ./: 切，首名沿 ScopeAt 上爬
// 定基，后段 LookupFieldType 逐跳，末 addMembers 展成员（union 归并、私有过滤）。
// 障碍是 `..` 拼接误触：空段即退 plain；any 基退 plain。候选：AST 重解 vs 符号表；
// 选符号表，快。不变量：ok 假即调用方走 plain。排查现场：链断先查 Lookup，私成员
// 漏/多先查 insideOwner。
func (d *document) memberCompletion(pos lspPos) ([]any, bool) {
	if d.index == nil {
		return nil, false
	}
	off := d.offsetAt(pos.Line, pos.Character)
	line := pos.Line
	if line < 0 || line >= len(d.lines) {
		return nil, false
	}
	start := d.lines[line]
	if off < start || off > len(d.content) {
		return nil, false
	}
	m := dotCompleteRe.FindStringSubmatch(d.content[start:off])
	if m == nil {
		return nil, false
	}
	chain := strings.Split(m[1], ".")
	// 排查看 Span/诊断，如 `local x=1` 错圈整句。
	var parts []string
	for _, seg := range chain {
		for _, s := range strings.Split(seg, ":") {
			if s == "" {
				return nil, false // `..` concat or stray punctuation
			}
			parts = append(parts, s)
		}
	}
	var sym *checker.Symbol
	for sc := d.index.ScopeAt(off); sc != nil; sc = sc.ParentScope() {
		for _, v := range sc.ScopeVars() {
			if v.Name == parts[0] && v.Sym != nil {
				sym = v.Sym
				break
			}
		}
		if sym != nil {
			break
		}
	}
	if sym == nil || sym.Type == nil {
		return nil, false
	}
	t := sym.Type
	for _, p := range parts[1:] {
		nt, ok := checker.LookupFieldType(t, p)
		if !ok {
			return nil, false
		}
		t = nt
	}
	if t == nil {
		return nil, false
	}
	if t.Kind() == types.KAny || t.Kind() == types.KUnknown {
		return nil, false
	}
	items := []any{}
	seen := map[string]bool{}
	add := func(name, kind, detail string, access types.Access, owner string) {
		if seen[name] {
			return
		}
		seen[name] = true
		if access == types.Private && !d.insideOwner(off, owner) {
			return
		}
		items = append(items, map[string]any{
			"label":  name,
			"kind":   completionKind(kind),
			"detail": detail,
		})
	}
	addMembers(t, add)
	sort.Slice(items, func(i, j int) bool {
		return items[i].(map[string]any)["label"].(string) < items[j].(map[string]any)["label"].(string)
	})
	return items, true
}

// 排查看 Span/诊断，如 `local x=1` 错圈整句。
// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：私有权判经 ScopeAt 上爬找类型条目，比 DeclRange 含 off；零值范围
// （无位）放行。障碍是影子同名类：就近条目为准。候选：名比 vs 指针；选就近名，
// 够用。不变量：owner 空恒 true。排查现场：私成员处处见先查 DeclRange。
func (d *document) insideOwner(off int, owner string) bool {
	if owner == "" || d.index == nil {
		return true
	}
	sc := d.index.ScopeAt(off)
	if sc == nil {
		return false
	}
	for ; sc != nil; sc = sc.ParentScope() {
		if e := sc.LookupTypeEntry(owner); e != nil {
			r := e.DeclRange()
			if r.Start.Offset == 0 && r.End.Offset == 0 {
				return true
			}
			return spanContains(r, off)
		}
	}
	return false
}

// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：按值类型展成员——Object 直列，Class 分字段/方法，ClassValue 列静态
// +`new`，Enum 列子，Union 递归归并（seen 去重在外）。障碍是类型位擦除：ClassValue
// 须透 Class 取 owner 供私有判。候选：深展 vs 直展；选直展。排查现场：成员缺先查
// 类型分支覆盖。
func addMembers(t types.Type, add func(name, kind, detail string, access types.Access, owner string)) {
	switch x := t.(type) {
	case *types.Object:
		for name, f := range x.Fields {
			add(name, fieldKind(f.Type), types.Abbrev(f.Type.String()), f.Access, x.Name)
		}
	case *types.Class:
		for name, f := range x.Fields {
			kind := "field"
			if f.IsMethod {
				kind = "method"
			}
			add(name, kind, types.Abbrev(f.Type.String()), f.Access, x.Name)
		}
	case *types.ClassValue:
		owner := ""
		if x.Class != nil {
			owner = x.Class.Name
		}
		for name, f := range x.Statics {
			kind := "field"
			if f.IsMethod {
				kind = "method"
			}
			add(name, kind, types.Abbrev(f.Type.String()), f.Access, owner)
		}
		add("new", "method", "constructor", types.Public, owner)
	case *types.Enum:
		for name, m := range x.Members {
			add(name, "member", m.String(), types.Public, x.Name)
		}
	case *types.Union:
		for _, m := range x.Types {
			addMembers(m, add)
		}
	}
}

func fieldKind(t types.Type) string {
	// 设计者视角：Func 即 method 余 field，补全图标据此。障碍是方法存为 Func：须判。
	// 排查现场：方法图标错先查此。
	if _, ok := t.(*types.Func); ok {
		return "method"
	}
	return "field"
}

func (s *Server) completion(raw json.RawMessage) any {
	// 设计者视角：补全先成员后 plain——成员命中即返，余者沿 ScopeAt 上爬收值/类型名
	// （seen 去重）+关键字，末排序。障碍是 index nil（坏档）：退关键字。候选：模糊
	// 排 vs 字母；选字母稳。不变量：恒 isIncomplete=false。排查现场：变量缺失先查
	// ScopeAt，类型缺失先查 ScopeTypes。
	doc, pos, ok := s.docAt(raw)
	if !ok {
		return map[string]any{"isIncomplete": false, "items": []any{}}
	}
	if items, ok := doc.memberCompletion(pos); ok {
		if items == nil {
			items = []any{}
		}
		return map[string]any{"isIncomplete": false, "items": items}
	}
	items := []any{}
	seen := map[string]bool{}
	if doc.index != nil {
		off := doc.offsetAt(pos.Line, pos.Character)
		for sc := doc.index.ScopeAt(off); sc != nil; sc = sc.ParentScope() {
			for _, v := range sc.ScopeVars() {
				if v.Sym == nil || seen[v.Name] {
					continue
				}
				seen[v.Name] = true
				items = append(items, map[string]any{
					"label":  v.Name,
					"kind":   completionKind(v.Sym.KindString()),
					"detail": types.Abbrev(v.Sym.Type.String()),
				})
			}
			for _, t := range sc.ScopeTypes() {
				key := "type:" + t.Name
				if seen[key] {
					continue
				}
				seen[key] = true
				items = append(items, map[string]any{
					"label":  t.Name,
					"kind":   completionKind("type"),
					"detail": t.Kind,
				})
			}
		}
	}
	for _, kw := range luaKeywords {
		if seen[kw] {
			continue
		}
		seen[kw] = true
		items = append(items, map[string]any{"label": kw, "kind": completionKind("keyword")})
	}
	sort.Slice(items, func(i, j int) bool {
		return items[i].(map[string]any)["label"].(string) < items[j].(map[string]any)["label"].(string)
	})
	return map[string]any{"isIncomplete": false, "items": items}
}
