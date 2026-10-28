package lsp

import (
	"encoding/json"
	"github.com/typelua/typelua/internal/checker"
	"github.com/typelua/typelua/internal/token"
)

// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：问答三件套——docAt 解 URI 取档，useAt 取最内 occurrence（含词尾
// 回退），defSpanOf 取定义位（记录优先、符号兜底）。障碍是光标停词后：须 off-1
// 二探；障碍是影子符号：Root 归一（见 checker.symRoot）。候选：AST 重找 vs 索引；
// 选索引，O(n) 够小。不变量：useAt 取最小 span。排查现场：跳转错先查回退与 Root。

// ---------- shared request plumbing ----------

type textPosParams struct {
	TextDocument struct {
		URI string `json:"uri"`
	} `json:"textDocument"`
	Position lspPos `json:"position"`
}

func (s *Server) docAt(raw json.RawMessage) (*document, lspPos, bool) {
	// 设计者视角：解 textDocument+position，档缺/坏参即 false。障碍是未知 URI：
	// 返 false 上游回空。排查现场：请求无响应先查 URI。
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

// 排查看 Span/诊断，如 `local x=1` 错圈整句。
// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：见上总注——最小 span 胜（嵌套取内），未中则 off-1 再探。障碍是大文件
// 线扫：Uses 全量，够快。排查现场：点词尾无响应先查回退。
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

// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：定义位优先用记录的 IsDef occurrence（源坐标真），无则兜底符号 span。
// 障碍是影子 Root：须按 Root 比。排查现场：跳转偏先查记录缺失。
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
