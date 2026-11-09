package lsp

import (
	"encoding/json"
	"github.com/typelua/typelua/internal/diag"
)

// Text synchronization (open/change/close) and diagnostic publishing.

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
	s.docs[p.TextDocument.URI] = s.analyze(p.TextDocument.URI, p.TextDocument.Text, p.TextDocument.Version)
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
	s.docs[p.TextDocument.URI] = s.analyze(p.TextDocument.URI, content, version)
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
