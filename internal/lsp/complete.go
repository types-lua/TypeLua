package lsp

import (
	"regexp"
	"sort"

	"encoding/json"
	"github.com/typelua/typelua/internal/checker"
	"github.com/typelua/typelua/internal/types"
)

// Completion: scope names, keywords, and member-aware dot/colon lists.

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

var dotCompleteRe = regexp.MustCompile(`([A-Za-z_][A-Za-z0-9_]*)([.:])$`)

// memberCompletion answers `ident.` / `ident:` completion from the
// identifier's type. Only single identifiers resolve (no chains).
// ok=false means "not a member position": fall back to plain completion.
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
	ident := m[1]
	var sym *checker.Symbol
	for sc := d.index.ScopeAt(off); sc != nil; sc = sc.ParentScope() {
		for _, v := range sc.ScopeVars() {
			if v.Name == ident && v.Sym != nil {
				sym = v.Sym
				break
			}
		}
		if sym != nil {
			break
		}
	}
	if sym == nil {
		return nil, false
	}
	t := sym.Type
	if t == nil {
		return nil, false
	}
	if t.Kind() == types.KAny || t.Kind() == types.KUnknown {
		return nil, false
	}
	items := []any{}
	seen := map[string]bool{}
	add := func(name, kind, detail string) {
		if seen[name] {
			return
		}
		seen[name] = true
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

// addMembers lists directly visible members of a value type (unions merge).
func addMembers(t types.Type, add func(name, kind, detail string)) {
	switch x := t.(type) {
	case *types.Object:
		for name, f := range x.Fields {
			add(name, fieldKind(f.Type), types.Abbrev(f.Type.String()))
		}
	case *types.Class:
		for name, f := range x.Fields {
			kind := "field"
			if f.IsMethod {
				kind = "method"
			}
			add(name, kind, types.Abbrev(f.Type.String()))
		}
	case *types.ClassValue:
		for name, f := range x.Statics {
			kind := "field"
			if f.IsMethod {
				kind = "method"
			}
			add(name, kind, types.Abbrev(f.Type.String()))
		}
		add("new", "method", "constructor")
	case *types.Enum:
		for name, m := range x.Members {
			add(name, "member", m.String())
		}
	case *types.Union:
		for _, m := range x.Types {
			addMembers(m, add)
		}
	}
}

func fieldKind(t types.Type) string {
	if _, ok := t.(*types.Func); ok {
		return "method"
	}
	return "field"
}

func (s *Server) completion(raw json.RawMessage) any {
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
