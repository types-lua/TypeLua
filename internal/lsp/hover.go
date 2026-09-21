package lsp

import (
	"encoding/json"
)

// Hover and go-to-definition.

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
