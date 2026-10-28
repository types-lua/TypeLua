package lsp

import (
	"encoding/json"
	"github.com/typelua/typelua/internal/diag"
)

// Text synchronization (open/change/close) and diagnostic publishing.
//
// 设计者视角：同步三件——open 全量建档+推诊，change 逐 patch（全量无 Range 即替，
// 余经 offsetAt 换算拼接）重析+推诊，close 删档。publish 按 Error 1/Warn 2 推，空恒
// []。障碍是 version 0（客户端省）：沿用旧版；patch 越界：原样保。候选：增量检查
// vs 全量重析；选全量。不变量：change 后档恒新。排查现场：诊断陈旧先查推诊，内容
// 错乱先查 patch 换算。

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
	// 设计者视角：open 即 analyze 建档+推诊。障碍是坏参：静默弃。排查现场：开档无诊
	// 先查 analyze。
	var p didOpenParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return
	}
	s.docs[p.TextDocument.URI] = analyze(p.TextDocument.URI, p.TextDocument.Text, p.TextDocument.Version)
	s.publishDiagnostics(p.TextDocument.URI)
}

func applyChange(content string, lines []int, start, end lspPos, text string) string {
	// 设计者视角：单 patch 经 offsetAt 定位拼接，越界保原。障碍是 UTF-16 换算：
	// 借临时 document 行表。排查现场：改后错位先查换算。
	tmp := &document{content: content, lines: lines}
	s := tmp.offsetAt(start.Line, start.Character)
	e := tmp.offsetAt(end.Line, end.Character)
	if s > e || s > len(content) || e > len(content) {
		return content
	}
	return content[:s] + text + content[e:]
}

func (s *Server) didChange(raw json.RawMessage) {
	// 设计者视角：change 逐 patch 累加（每贴重算行表），末重析推诊。障碍是多 patch
	// 同批：须序贴。候选：合批 vs 逐贴；选逐贴合 LSP。不变量：档 version 单调。
	// 排查现场：内容分叉先查行表刷新。
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
	// 设计者视角：close 删档（诊断由客户端清）。障碍是未知 URI：delete 无害。排查现场：
	// 内存涨先查 close。
	var p didCloseParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return
	}
	delete(s.docs, p.TextDocument.URI)
}

func (s *Server) publishDiagnostics(uri string) {
	// 设计者视角：推诊把 doc.diags 译 severity/code/message+range，空推 [] 清旧。
	// 障碍是 Info 级：统一按 Error 1（Warn 2 另判），保客户端显。排查现场：旧错不消
	// 先查空推。
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
	// 设计者视角：mustJSON 供通知载荷编码，错即空（publish 不可崩）。障碍是 Marshal
	// 失败：吞错。排查现场：通知无载先查此。
	raw, _ := json.Marshal(v)
	return raw
}
