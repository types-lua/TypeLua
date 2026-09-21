package lsp

import (
	"encoding/json"
	"github.com/typelua/typelua/internal/checker"
	"github.com/typelua/typelua/internal/token"
)

// Shared request plumbing: document lookup and symbol occurrence search.

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
