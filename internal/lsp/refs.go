package lsp

import (
	"regexp"
	"sort"

	"encoding/json"
	"github.com/typelua/typelua/internal/checker"
	"github.com/typelua/typelua/internal/token"
)

// References and rename across value, type, and member namespaces.

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

// memberMatch reports whether o is an occurrence of the same member.
func memberMatch(o *checker.SymUse, m *checker.MemberRef) bool {
	if o.Member == nil || o.Member.Name != m.Name {
		return false
	}
	if m.Owner != nil || o.Member.Owner != nil {
		return o.Member.Owner == m.Owner
	}
	// Structural members unify by variable root (aliases untracked).
	return o.Member.StructOwner == m.StructOwner && m.StructOwner != nil
}

func (d *document) memberOccurrences(m *checker.MemberRef) []checker.SymUse {
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

// memberDefSpan finds the recorded definition span of a member.
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

// typeOccurrences lists declaration + uses of a type entry.
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

// twinValueRoot finds the value symbol twinning a type declaration
// (class/enum names live in both namespaces); nil for pure types.
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
			// The value twin shares the declaration span; don't list twice.
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
	// Member path: class/interface/enum members rename within the owner.
	// Structural members (anonymous literals) list for references but
	// refuse rename: aliases would silently miss occurrences.
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
	// Type path: declaration plus all type uses; a class/enum value twin
	// renames along so neither namespace is left behind.
	if u.Entry != nil {
		return s.renameType(doc, u.Entry, p.NewName)
	}
	if u.Sym == nil {
		return nil, ErrInvalidParams, "no renameable symbol here"
	}
	if u.Sym.IsBuiltin {
		return nil, ErrInvalidParams, "cannot rename a builtin"
	}
	// A class/enum value renames through its twin type declaration.
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

// renameType renames a type declaration with all its uses; a class/enum
// value twin renames along (same name, other namespace).
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
