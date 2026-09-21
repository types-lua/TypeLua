package lsp

import (
	"encoding/json"
	"github.com/typelua/typelua/internal/ast"
	"github.com/typelua/typelua/internal/token"
)

// Document symbols (top-level declarations).

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
		case *ast.ImportStmt:
			if n.Name != nil {
				add(n.Name.Name, 13, n.Span(), n.Name.Span())
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
