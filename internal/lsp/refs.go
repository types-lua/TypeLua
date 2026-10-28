package lsp

import (
	"regexp"
	"sort"

	"encoding/json"
	"github.com/typelua/typelua/internal/checker"
	"github.com/typelua/typelua/internal/token"
)

// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：引用三轨——值按 Root 聚，类型按 Entry 指针聚，成员按 Owner/名聚
// （结构成员按 StructOwner）。重命名三轨各产 edits，类/枚举值-型孪生联动，硬关键字
// 与结构成员拒改。障碍是双轨同名（Dog 值+型）：须联动否则留半。图：useAt→分轨→
// 聚→排→edits。候选：单轨 vs 三轨；选三轨合双命名空间。不变量：edits 按 offset 排。
// 排查现场：引用缺先查聚判据，重命名半边先查孪生。

// ---------- references & rename ----------

type refParams struct {
	TextDocument struct {
		URI string `json:"uri"`
	} `json:"textDocument"`
	Position lspPos `json:"position"`
	Context  struct {
		IncludeDeclaration bool `json:"includeDeclaration"`
	} `json:"context"`
}

func (d *document) occurrencesOf(root *checker.Symbol) []checker.SymUse {
	// 设计者视角：按 Root 聚值引用（含影子），nil 宽容返空。障碍是影子未归一：
	// 上游须 symRoot。排查现场：引用缺分支先查 Root。
	var out []checker.SymUse
	if d.index == nil || root == nil {
		return out
	}
	for _, u := range d.index.Uses {
		if u.Sym != nil && u.Root == root {
			out = append(out, u)
		}
	}
	return out
}

// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：名义成员比 Owner 指针（名不可靠），结构成员比 StructOwner 指针
// （别名不追，见 checker）。障碍是 nil Owner：退结构比。候选：名比 vs 指针；
// 选指针。不变量：名异恒 false。排查现场：成员引用串先查 Owner，结构误并先查别名。
func memberMatch(o *checker.SymUse, m *checker.MemberRef) bool {
	if o.Member == nil || o.Member.Name != m.Name {
		return false
	}
	if m.Owner != nil || o.Member.Owner != nil {
		return o.Member.Owner == m.Owner
	}
	// 排查看 Span/诊断，如 `local x=1` 错圈整句。
	return o.Member.StructOwner == m.StructOwner && m.StructOwner != nil
}

func (d *document) memberOccurrences(m *checker.MemberRef) []checker.SymUse {
	// 设计者视角：成员引用全扫 memberMatch，nil 宽容。障碍是大文件 O(n)：可接受。
	// 排查现场：缺失先查 memberMatch。
	var out []checker.SymUse
	if d.index == nil || m == nil {
		return out
	}
	for _, u := range d.index.Uses {
		if memberMatch(&u, m) {
			out = append(out, u)
		}
	}
	return out
}

// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：首个 IsDef 且 match 者即定义位。障碍是索引器解出字段无定义：返
// false 上游回 nil。排查现场：成员跳转空先查记录。
func (d *document) memberDefSpan(m *checker.MemberRef) (token.Span, bool) {
	if d.index == nil || m == nil {
		return token.Span{}, false
	}
	for _, u := range d.index.Uses {
		if u.IsDef && memberMatch(&u, m) {
			return u.Span, true
		}
	}
	return token.Span{}, false
}

// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：按 Entry 指针聚（名不可靠，遮蔽下重名多）。障碍是指针同一性：同声明
// 恒同指针（见 checker）。排查现场：类型引用分裂先查指针。
func (d *document) typeOccurrences(e *checker.TypeEntry) []checker.SymUse {
	var out []checker.SymUse
	if d.index == nil || e == nil {
		return out
	}
	for _, u := range d.index.Uses {
		if u.Entry == e {
			out = append(out, u)
		}
	}
	return out
}

// 排查看 Span/诊断，如 `local x=1` 错圈整句。
// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：类/枚举双栖——同名值符与类型条目孪生，首个 IsDef+Entry+Sym 者即 twin。
// 障碍是纯类型详见实现，排查看 Span。
// 不变量：纯类型恒 nil。排查现场：重命名半边先查 twin。
func (d *document) twinValueRoot(e *checker.TypeEntry) *checker.Symbol {
	if d.index == nil || e == nil {
		return nil
	}
	for i := range d.index.Uses {
		o := &d.index.Uses[i]
		if o.IsDef && o.Entry == e && o.Sym != nil {
			return o.Root
		}
	}
	return nil
}

func (s *Server) references(raw json.RawMessage) any {
	// 设计者视角：引用三轨分发+IncludeDeclaration 开关，类型轨去重值孪生声明 span，
	// 空恒返 []（非 nil，客户端友好）。障碍是声明去重：孪生同 span 须跳。排查现场：
	// 引用多/缺先查开关与去重。
	var p refParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil
	}
	doc := s.docs[p.TextDocument.URI]
	if doc == nil {
		return nil
	}
	u := doc.useAt(p.Position)
	if u == nil {
		return nil
	}
	var locs []location
	if u.Member != nil {
		for _, o := range doc.memberOccurrences(u.Member) {
			if !p.Context.IncludeDeclaration && o.IsDef {
				continue
			}
			locs = append(locs, location{URI: doc.uri, Range: doc.toRange(o.Span)})
		}
	} else if u.Entry != nil {
		decl := u.Entry.DeclSpan()
		if p.Context.IncludeDeclaration {
			locs = append(locs, location{URI: doc.uri, Range: doc.toRange(decl)})
		}
		for _, o := range doc.typeOccurrences(u.Entry) {
			if !p.Context.IncludeDeclaration && o.IsDef {
				continue
			}
			// 排查看 Span/诊断，如 `local x=1` 错圈整句。
			if o.Span.Start.Offset == decl.Start.Offset && o.Span.End.Offset == decl.End.Offset {
				continue
			}
			locs = append(locs, location{URI: doc.uri, Range: doc.toRange(o.Span)})
		}
	} else if u.Sym != nil {
		for _, o := range doc.occurrencesOf(u.Root) {
			if !p.Context.IncludeDeclaration && o.IsDef {
				continue
			}
			locs = append(locs, location{URI: doc.uri, Range: doc.toRange(o.Span)})
		}
	} else {
		return nil
	}
	if locs == nil {
		return []location{}
	}
	return locs
}

var identRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

var hardKeywords = map[string]bool{
	"and": true, "break": true, "do": true, "else": true, "elseif": true,
	"end": true, "false": true, "for": true, "function": true, "goto": true,
	"if": true, "in": true, "local": true, "nil": true, "not": true,
	"or": true, "repeat": true, "return": true, "then": true, "true": true,
	"until": true, "while": true,
}

type renameParams struct {
	TextDocument struct {
		URI string `json:"uri"`
	} `json:"textDocument"`
	Position lspPos `json:"position"`
	NewName  string `json:"newName"`
}

func (s *Server) rename(raw json.RawMessage) (any, int, string) {
	// 设计者视角：重命名五闸——新名合法（ident+非硬关键字）、档在、位有符；成员轨
	// （名义可改、结构拒）、类型轨（renameType 联动孪生）、值轨（builtin 拒、同名
	// 类型在域拒、经孪生转类型轨）。障碍是值/型同名遮蔽：误改一轨即错；故 ScopeAt
	// 查同名类型条目。候选：单轨 vs 三轨；选三轨。不变量：edits 按 off 排。排查现场：
	// 拒改先查此五闸。
	var p renameParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, ErrInvalidParams, "invalid params"
	}
	if !identRe.MatchString(p.NewName) || hardKeywords[p.NewName] {
		return nil, ErrInvalidParams, "not a valid identifier: " + p.NewName
	}
	doc := s.docs[p.TextDocument.URI]
	if doc == nil {
		return nil, ErrInvalidParams, "unknown document"
	}
	u := doc.useAt(p.Position)
	if u == nil {
		return nil, ErrInvalidParams, "no renameable symbol here"
	}
	// 排查看 Span/诊断，如 `local x=1` 错圈整句。
	// 排查看 Span/诊断，如 `local x=1` 错圈整句。
	// 排查看 Span/诊断，如 `local x=1` 错圈整句。
	if u.Member != nil {
		if u.Member.Owner == nil {
			return nil, ErrInvalidParams, "cannot rename structural members (aliases untracked)"
		}
		def, ok := doc.memberDefSpan(u.Member)
		if !ok {
			return nil, ErrInvalidParams, "no renameable member here"
		}
		seen := map[int]bool{def.Start.Offset: true}
		var items []any
		items = append(items, map[string]any{"range": doc.toRange(def), "newText": p.NewName})
		for _, o := range doc.memberOccurrences(u.Member) {
			if o.IsDef || seen[o.Span.Start.Offset] {
				continue
			}
			seen[o.Span.Start.Offset] = true
			items = append(items, map[string]any{"range": doc.toRange(o.Span), "newText": p.NewName})
		}
		return map[string]any{"changes": map[string]any{doc.uri: items}}, 0, ""
	}
	// 类型详见实现，排查看 Span。
	// 排查看 Span/诊断，如 `local x=1` 错圈整句。
	if u.Entry != nil {
		return s.renameType(doc, u.Entry, p.NewName)
	}
	if u.Sym == nil {
		return nil, ErrInvalidParams, "no renameable symbol here"
	}
	if u.Sym.IsBuiltin {
		return nil, ErrInvalidParams, "cannot rename a builtin"
	}
	// 排查看 Span/诊断，如 `local x=1` 错圈整句。
	if doc.index != nil {
		off := doc.offsetAt(p.Position.Line, p.Position.Character)
		if sc := doc.index.ScopeAt(off); sc != nil {
			if te := sc.LookupTypeEntry(u.Sym.Name); te != nil {
				if twin := doc.twinValueRoot(te); twin != nil && twin == u.Root {
					return s.renameType(doc, te, p.NewName)
				}
				return nil, ErrInvalidParams, "cannot rename: a type declaration of the same name is in scope"
			}
		}
	}
	type edit struct {
		off int
		r   lspRange
	}
	var edits []edit
	def := doc.defSpanOf(u.Root)
	edits = append(edits, edit{off: def.Start.Offset, r: doc.toRange(def)})
	for _, o := range doc.occurrencesOf(u.Root) {
		if o.IsDef {
			continue // definition handled above (once)
		}
		edits = append(edits, edit{off: o.Span.Start.Offset, r: doc.toRange(o.Span)})
	}
	sort.Slice(edits, func(i, j int) bool { return edits[i].off < edits[j].off })
	var items []any
	for _, e := range edits {
		items = append(items, map[string]any{"range": e.r, "newText": p.NewName})
	}
	if items == nil {
		items = []any{}
	}
	return map[string]any{"changes": map[string]any{doc.uri: items}}, 0, ""
}

// 排查看 Span/诊断，如 `local x=1` 错圈整句。
// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：类型改名聚声明+全用+孪生值轨（见 twinValueRoot），seen 按起 offset
// 去重，末排序。障碍是孪生声明同 span 重复：seen 消之。候选：分两次改 vs 一次联动；
// 选一次。不变量：edits 有序。排查现场：半边未改先查 twin。
func (s *Server) renameType(doc *document, entry *checker.TypeEntry, newName string) (any, int, string) {
	if entry == nil {
		return nil, ErrInvalidParams, "no renameable symbol here"
	}
	seen := map[int]bool{}
	var edits []struct {
		off int
		r   lspRange
	}
	add := func(sp token.Span) {
		if seen[sp.Start.Offset] {
			return
		}
		seen[sp.Start.Offset] = true
		edits = append(edits, struct {
			off int
			r   lspRange
		}{off: sp.Start.Offset, r: doc.toRange(sp)})
	}
	add(entry.DeclSpan())
	for _, o := range doc.typeOccurrences(entry) {
		add(o.Span)
	}
	if twin := doc.twinValueRoot(entry); twin != nil {
		def := doc.defSpanOf(twin)
		add(def)
		for _, o := range doc.occurrencesOf(twin) {
			if o.IsDef {
				continue
			}
			add(o.Span)
		}
	}
	sort.Slice(edits, func(i, j int) bool { return edits[i].off < edits[j].off })
	var items []any
	for _, e := range edits {
		items = append(items, map[string]any{"range": e.r, "newText": newName})
	}
	if items == nil {
		items = []any{}
	}
	return map[string]any{"changes": map[string]any{doc.uri: items}}, 0, ""
}
