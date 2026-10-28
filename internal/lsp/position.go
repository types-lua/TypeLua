package lsp

import (
	"github.com/typelua/typelua/internal/token"
	"unicode/utf16"
	"unicode/utf8"
)

// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：三制翻译官——token(1 起 rune)、LSP(0 起 UTF-16)、文档(byte)。
// 代理对详见实现，排查看 Span。
// 障碍是中文/emoji：byte 列全偏。候选：rune vs UTF-16；选按协议 UTF-16。不变量：
// 越界钳制。排查现场：跳转偏先查此换算。

// ---------- positions ----------

// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：取行裸串供列换算，末行无 `\n` 即到文末。障碍是行越界：返 ""。排查现场：
// 空行列错先查行表。
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

// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：rune 列→UTF-16 列逐字 Encode 累 units，<1 钳 0。障碍是代理对：
// len(Encode) 2。排查现场：中文列偏先查此处。
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
	// 设计者视角：LSP 位→byte 偏移逆算——行越界钳头尾，行内逐字耗 units。障碍是超长
	// 列：耗尽即行末。候选：二分 vs 线走；选线走，行短。不变量：返值恒界内。排查现场：
	// 点击末跳转错先查钳制。
	if line < 0 {
		return 0
	}
	if line >= len(d.lines) {
		return len(d.content)
	}
	text := d.lineText(line)
	// 排查看 Span/诊断，如 `local x=1` 错圈整句。
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
	// 设计者视角：Span→LSP Range 双端换算（行 -1，列经 utf16Col）。障碍是跨行：
	// 起止各算。排查现场：高亮错位先查行列基（1→0）。
	return lspRange{
		Start: lspPos{Line: sp.Start.Line - 1, Character: utf16Col(d.lineText(sp.Start.Line-1), sp.Start.Column)},
		End:   lspPos{Line: sp.End.Line - 1, Character: utf16Col(d.lineText(sp.End.Line-1), sp.End.Column)},
	}
}

func spanContains(sp token.Span, off int) bool {
	// 设计者视角：含判左闭右开，零长（空位）仅等值命中。障碍是光标停词尾：调用方
	// 另有 off-1 回退（见 useAt）。排查现场：点选无响应先查回退。
	if sp.Start.Offset == sp.End.Offset {
		return off == sp.Start.Offset
	}
	return sp.Start.Offset <= off && off < sp.End.Offset
}
