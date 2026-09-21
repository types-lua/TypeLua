package lsp

import (
	"github.com/typelua/typelua/internal/token"
	"unicode/utf16"
	"unicode/utf8"
)

// Line/column conversions between UTF-16 LSP positions and byte offsets.

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
