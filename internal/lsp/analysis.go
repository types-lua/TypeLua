package lsp

import (
	"encoding/json"
	"regexp"
	"sort"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/typelua/typelua/internal/ast"
	"github.com/typelua/typelua/internal/checker"
	"github.com/typelua/typelua/internal/diag"
	"github.com/typelua/typelua/internal/parser"
	"github.com/typelua/typelua/internal/token"
	"github.com/typelua/typelua/internal/types"
)

// ---------- documents ----------

type document struct {
	uri     string
	version int
	content string
	lines   []int // byte offset of each 0-based line start
	file    *ast.File
	diags   []diag.Diagnostic
	index   *checker.SymIndex
}

func uriToName(uri string) string {
	if strings.HasPrefix(uri, "file://") {
		return strings.TrimPrefix(uri, "file://")
	}
	return uri
}

func splitLines(content string) []int {
	lines := []int{0}
	for i := 0; i < len(content); i++ {
		if content[i] == '\n' {
			lines = append(lines, i+1)
		}
	}
	return lines
}

func analyze(uri, content string, version int) *document {
	name := uriToName(uri)
	f, pdiags := parser.ParseFile(name, content)
	diags, idx := checker.CheckFileIndexed(f, checker.DefaultOptions())
	diags = append(append([]diag.Diagnostic{}, pdiags...), diags...)
	return &document{uri: uri, version: version, content: content, lines: splitLines(content), file: f, diags: diags, index: idx}
}

// ---------- positions ----------

// lineText returns the raw line (without trailing newline).
func (d *document) lineText(line int) string {
	if line < 0 || line >= len(d.lines) {
		return ""
	}
	start := d.lines[line]
	end := len(d.content)
	if line+1 < len(d.lines) {
		end = d.lines[line+1] - 1
	}
	return d.content[start:end]
}

// utf16Col converts our 1-based rune column to an LSP 0-based UTF-16 column.
func utf16Col(line string, runeCol1 int) int {
	if runeCol1 < 1 {
		return 0
	}
	want := runeCol1 - 1
	units := 0
	i := 0
	for n := 0; n < want && i < len(line); n++ {
		r, size := utf8.DecodeRuneInString(line[i:])
		i += size
		units += len(utf16.Encode([]rune{r}))
	}
	return units
}

func (d *document) offsetAt(line, colUTF16 int) int {
	if line < 0 {
		return 0
	}
	if line >= len(d.lines) {
		return len(d.content)
	}
	text := d.lineText(line)
	// UTF-16 column back to byte offset.
	units, i := 0, 0
	for i < len(text) && units < colUTF16 {
		r, size := utf8.DecodeRuneInString(text[i:])
		i += size
		units += len(utf16.Encode([]rune{r}))
	}
	return d.lines[line] + i
}

type lspPos struct {
	Line      int `json:"line"`
	Character int `json:"character"`
}

type lspRange struct {
	Start lspPos `json:"start"`
	End   lspPos `json:"end"`
}

func (d *document) toRange(sp token.Span) lspRange {
	return lspRange{
		Start: lspPos{Line: sp.Start.Line - 1, Character: utf16Col(d.lineText(sp.Start.Line-1), sp.Start.Column)},
		End:   lspPos{Line: sp.End.Line - 1, Character: utf16Col(d.lineText(sp.End.Line-1), sp.End.Column)},
	}
}

func spanContains(sp token.Span, off int) bool {
	if sp.Start.Offset == sp.End.Offset {
		return off == sp.Start.Offset
	}
	return sp.Start.Offset <= off && off < sp.End.Offset
}

// ---------- text sync ----------

type didOpenParams struct {
	TextDocument struct {
		URI     string `json:"uri"`
		Version int    `json:"version"`
		Text    string `json:"text"`
	} `json:"textDocument"`
}

type didChangeParams struct {
	TextDocument struct {
		URI     string `json:"uri"`
		Version int    `json:"version"`
	} `json:"textDocument"`
	ContentChanges []struct {
		Range *struct {
			Start lspPos `json:"start"`
			End   lspPos `json:"end"`
		} `json:"range"`
		Text string `json:"text"`
	} `json:"contentChanges"`
}

type didCloseParams struct {
	TextDocument struct {
		URI string `json:"uri"`
	} `json:"textDocument"`
}

func (s *Server) didOpen(raw json.RawMessage) {
	var p didOpenParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return
	}
	s.docs[p.TextDocument.URI] = analyze(p.TextDocument.URI, p.TextDocument.Text, p.TextDocument.Version)
	s.publishDiagnostics(p.TextDocument.URI)
}

func applyChange(content string, lines []int, start, end lspPos, text string) string {
	tmp := &document{content: content, lines: lines}
	s := tmp.offsetAt(start.Line, start.Character)
	e := tmp.offsetAt(end.Line, end.Character)
	if s > e || s > len(content) || e > len(content) {
		return content
	}
	return content[:s] + text + content[e:]
}

func (s *Server) didChange(raw json.RawMessage) {
	var p didChangeParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return
	}
	doc := s.docs[p.TextDocument.URI]
	content := ""
	version := p.TextDocument.Version
	if doc != nil {
		content = doc.content
		if version == 0 {
			version = doc.version
		}
	}
	lines := splitLines(content)
	for _, ch := range p.ContentChanges {
		if ch.Range == nil {
			content = ch.Text
		} else {
			content = applyChange(content, lines, ch.Range.Start, ch.Range.End, ch.Text)
		}
		lines = splitLines(content)
	}
	s.docs[p.TextDocument.URI] = analyze(p.TextDocument.URI, content, version)
	s.publishDiagnostics(p.TextDocument.URI)
}

func (s *Server) didClose(raw json.RawMessage) {
	var p didCloseParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return
	}
	delete(s.docs, p.TextDocument.URI)
}

func (s *Server) publishDiagnostics(uri string) {
	doc := s.docs[uri]
	var items []any
	if doc != nil {
		for _, d := range doc.diags {
			sev := 1
			if d.Severity == diag.Warning {
				sev = 2
			}
			items = append(items, map[string]any{
				"range":    doc.toRange(d.Span),
				"severity": sev,
				"code":     d.Code,
				"source":   "tlua",
				"message":  d.Message,
			})
		}
	}
	if items == nil {
		items = []any{}
	}
	body, _ := json.Marshal(message{JSONRPC: "2.0", Method: "textDocument/publishDiagnostics", Params: mustJSON(map[string]any{"uri": uri, "diagnostics": items})})
	s.writeFrame(body)
}

func mustJSON(v any) json.RawMessage {
	raw, _ := json.Marshal(v)
	return raw
}

// ---------- shared request plumbing ----------

type textPosParams struct {
	TextDocument struct {
		URI string `json:"uri"`
	} `json:"textDocument"`
	Position lspPos `json:"position"`
}

func (s *Server) docAt(raw json.RawMessage) (*document, lspPos, bool) {
	var p textPosParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, lspPos{}, false
	}
	doc := s.docs[p.TextDocument.URI]
	if doc == nil {
		return nil, lspPos{}, false
	}
	return doc, p.Position, true
}

// useAt finds the innermost symbol use containing the position (with an
// end-of-token fallback, since cursors often rest after the last char).
func (d *document) useAt(pos lspPos) *checker.SymUse {
	if d.index == nil {
		return nil
	}
	off := d.offsetAt(pos.Line, pos.Character)
	var best *checker.SymUse
	bestSize := -1
	consider := func(u *checker.SymUse, o int) {
		if !spanContains(u.Span, o) {
			return
		}
		size := u.Span.End.Offset - u.Span.Start.Offset
		if best == nil || size < bestSize {
			best = u
			bestSize = size
		}
	}
	for i := range d.index.Uses {
		consider(&d.index.Uses[i], off)
	}
	if best == nil && off > 0 {
		for i := range d.index.Uses {
			consider(&d.index.Uses[i], off-1)
		}
	}
	return best
}

// defSpanOf prefers a recorded definition occurrence, else the symbol span.
func (d *document) defSpanOf(root *checker.Symbol) token.Span {
	if d.index == nil || root == nil {
		return token.Span{}
	}
	for i := range d.index.Uses {
		u := &d.index.Uses[i]
		if u.IsDef && u.Root == root {
			return u.Span
		}
	}
	return root.Span
}

// ---------- hover & definition ----------

func (s *Server) hover(raw json.RawMessage) any {
	doc, pos, ok := s.docAt(raw)
	if !ok {
		return nil
	}
	u := doc.useAt(pos)
	if u == nil {
		return nil
	}
	var md string
	if u.Sym != nil && u.Member == nil {
		md = "```tlua\n" + u.Sym.KindString() + " " + u.Sym.Name + ": " + u.Sym.Type.String() + "\n```"
	} else if u.Member != nil {
		kind := u.Member.Kind
		if kind == "" {
			kind = "member"
		}
		md = "```tlua\n" + kind + " " + u.Member.OwnerName + "." + u.Member.Name + "\n```"
	} else if u.Entry != nil {
		md = "```tlua\n" + u.Entry.EntryKind() + " " + u.Entry.TypeName() + "\n```"
	} else {
		return nil
	}
	return map[string]any{"contents": map[string]any{"kind": "markdown", "value": md}}
}

type location struct {
	URI   string   `json:"uri"`
	Range lspRange `json:"range"`
}

func (s *Server) definition(raw json.RawMessage) any {
	doc, pos, ok := s.docAt(raw)
	if !ok {
		return nil
	}
	u := doc.useAt(pos)
	if u == nil {
		return nil
	}
	if u.Member != nil {
		if sp, ok := doc.memberDefSpan(u.Member); ok {
			return []location{{URI: doc.uri, Range: doc.toRange(sp)}}
		}
		return nil
	}
	if u.Entry != nil {
		return []location{{URI: doc.uri, Range: doc.toRange(u.Entry.DeclSpan())}}
	}
	if u.Sym != nil {
		return []location{{URI: doc.uri, Range: doc.toRange(doc.defSpanOf(u.Root))}}
	}
	return nil
}

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
			add(name, fieldKind(f.Type), f.Type.String())
		}
	case *types.Class:
		for name, f := range x.Fields {
			kind := "field"
			if f.IsMethod {
				kind = "method"
			}
			add(name, kind, f.Type.String())
		}
	case *types.ClassValue:
		for name, f := range x.Statics {
			kind := "field"
			if f.IsMethod {
				kind = "method"
			}
			add(name, kind, f.Type.String())
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
					"detail": v.Sym.Type.String(),
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
	return o.Member != nil && o.Member.Owner == m.Owner && o.Member.Name == m.Name
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
	if u.Member != nil {
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

// ---------- document symbols ----------

func (s *Server) documentSymbols(raw json.RawMessage) any {
	var p struct {
		TextDocument struct {
			URI string `json:"uri"`
		} `json:"textDocument"`
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil
	}
	doc := s.docs[p.TextDocument.URI]
	if doc == nil || doc.file == nil || doc.file.Block == nil {
		return []any{}
	}
	var out []any
	add := func(name string, kind int, sp, sel token.Span) {
		out = append(out, map[string]any{
			"name":           name,
			"kind":           kind,
			"range":          doc.toRange(sp),
			"selectionRange": doc.toRange(sel),
		})
	}
	for _, st := range doc.file.Block.Stmts {
		switch n := st.(type) {
		case *ast.LocalFuncDef:
			add(n.Name.Name, 12, n.Span(), n.Name.Span())
		case *ast.FuncDefStmt:
			if n.Name != nil && len(n.Name.Prefix) > 0 {
				add(n.Name.Prefix[0].Name, 12, n.Span(), n.Name.Prefix[0].Span())
			}
		case *ast.ClassDecl:
			add(n.Name.Name, 5, n.Span(), n.Name.Span())
		case *ast.InterfaceDecl:
			add(n.Name.Name, 11, n.Span(), n.Name.Span())
		case *ast.TypeAliasDecl:
			add(n.Name.Name, 26, n.Span(), n.Name.Span())
		case *ast.EnumDecl:
			add(n.Name.Name, 10, n.Span(), n.Name.Span())
		case *ast.LocalAssign:
			for _, id := range n.Names {
				add(id.Name, 13, id.Span(), id.Span())
			}
		case *ast.MacroDef:
			add(n.Name.Name+" (macro)", 12, n.Span(), n.Name.Span())
		}
	}
	if out == nil {
		out = []any{}
	}
	return out
}
