package lsp

import (
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"runtime"

	"github.com/typelua/typelua/internal/token"
	"github.com/typelua/typelua/internal/types"
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
		md = "```tlua\n" + u.Sym.KindString() + " " + u.Sym.Name + ": " + types.Abbrev(u.Sym.Type.String()) + "\n```"
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
			return doc.location(doc.uri, sp)
		}
		return nil
	}
	if u.Entry != nil {
		return s.locationFor(doc, u.Entry.File, u.Entry.DeclSpan())
	}
	if u.Sym != nil {
		root := u.Root
		if root == nil {
			root = u.Sym
		}
		if root.File == "" {
			return nil // builtin: there is no source location
		}
		span := root.Span
		if doc.file != nil && root.File == doc.file.Name {
			if def := doc.defSpanOf(root); def.Start.Line > 0 {
				span = def
			}
		}
		return s.locationFor(doc, root.File, span)
	}
	return nil
}

// locationsFor maps a span to the file that actually contains it. Returning
// the open document's URI for an ambient/imported definition used to jump to a
// random line in the current file (the line/column belonged to another file).
func (s *Server) locationsFor(doc *document, file string, span token.Span) []location {
	if file == "" || span.Start.Line < 1 || span.End.Line < 1 {
		return nil
	}
	if doc != nil && doc.file != nil && doc.file.Name == file {
		if rng, ok := doc.rangeOf(span); ok {
			return []location{{URI: doc.uri, Range: rng}}
		}
		return nil
	}
	if rng, ok := s.rangeInFile(file, span); ok {
		return []location{{URI: pathToURI(file), Range: rng}}
	}
	return nil
}

func (s *Server) locationFor(doc *document, file string, span token.Span) any {
	locs := s.locationsFor(doc, file, span)
	if len(locs) == 0 {
		return nil
	}
	return locs
}

func (d *document) location(uri string, span token.Span) any {
	rng, ok := d.rangeOf(span)
	if !ok {
		return nil
	}
	return []location{{URI: uri, Range: rng}}
}

func (d *document) rangeOf(span token.Span) (lspRange, bool) {
	if span.Start.Line < 1 || span.End.Line < 1 ||
		span.Start.Line > len(d.lines) || span.End.Line > len(d.lines) ||
		span.Start.Column < 1 || span.End.Column < 1 {
		return lspRange{}, false
	}
	return d.toRange(span), true
}

// rangeInFile converts a span in a file that may not be open. Open buffers win
// so an unsaved definition still points at the current text.
func (s *Server) rangeInFile(file string, span token.Span) (lspRange, bool) {
	for _, open := range s.docs {
		if open != nil && open.file != nil && open.file.Name == file {
			return open.rangeOf(span)
		}
	}
	data, err := os.ReadFile(file)
	if err != nil {
		return lspRange{}, false
	}
	doc := &document{content: string(data), lines: splitLines(string(data))}
	return doc.rangeOf(span)
}

func pathToURI(file string) string {
	path := filepath.ToSlash(file)
	if runtime.GOOS == "windows" && len(path) >= 2 && path[1] == ':' {
		path = "/" + path
	}
	return (&url.URL{Scheme: "file", Path: path}).String()
}
