package sourcemap

import (
	"encoding/json"
	"strings"
)

// Mapping links a generated position to an original source position.
// Lines/columns follow the source-map V3 convention: generated lines are
// implicit (one group per line), generated columns are 0-based, source
// lines/columns below are 0-based, names index into the names table.
type Mapping struct {
	GenLine int // 1-based
	GenCol  int // 0-based, runes
	SrcFile string
	SrcLine int // 0-based
	SrcCol  int // 0-based
	Name    string
}

const base64Chars = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"

// AppendVLQ appends one base64-VLQ value (spec: sign in LSB).
func AppendVLQ(dst []byte, v int) []byte {
	var uv uint32
	if v < 0 {
		uv = uint32(-v)<<1 | 1
	} else {
		uv = uint32(v) << 1
	}
	for {
		d := uv & 31
		uv >>= 5
		if uv > 0 {
			d |= 32
		}
		dst = append(dst, base64Chars[d])
		if uv == 0 {
			break
		}
	}
	return dst
}

// Encode builds a source-map V3 JSON document. Mappings must be sorted by
// (GenLine, GenCol); sources/content align by index.
func Encode(file string, sources []string, contents []string, names []string, mappings []Mapping) ([]byte, error) {
	nameIdx := map[string]int{}
	for i, n := range names {
		if _, ok := nameIdx[n]; !ok {
			nameIdx[n] = i
		}
	}
	srcIdx := map[string]int{}
	for i, s := range sources {
		if _, ok := srcIdx[s]; !ok {
			srcIdx[s] = i
		}
	}
	var sb strings.Builder
	prevGenLine := 1
	var prevGenCol, prevSrc, prevLine, prevCol, prevName int
	needComma := false
	for _, m := range mappings {
		for prevGenLine < m.GenLine {
			sb.WriteByte(';')
			prevGenLine++
			prevGenCol = 0
			needComma = false
		}
		if needComma {
			sb.WriteByte(',')
		}
		var seg []byte
		seg = AppendVLQ(seg, m.GenCol-prevGenCol)
		prevGenCol = m.GenCol
		si, ok := srcIdx[m.SrcFile]
		if !ok {
			si = len(sources)
			sources = append(sources, m.SrcFile)
			contents = append(contents, "")
			srcIdx[m.SrcFile] = si
		}
		seg = AppendVLQ(seg, si-prevSrc)
		prevSrc = si
		seg = AppendVLQ(seg, m.SrcLine-prevLine)
		prevLine = m.SrcLine
		seg = AppendVLQ(seg, m.SrcCol-prevCol)
		prevCol = m.SrcCol
		if m.Name != "" {
			ni, ok := nameIdx[m.Name]
			if !ok {
				ni = len(names)
				names = append(names, m.Name)
				nameIdx[m.Name] = ni
			}
			seg = AppendVLQ(seg, ni-prevName)
			prevName = ni
		}
		sb.Write(seg)
		needComma = true
	}
	doc := map[string]any{
		"version":        3,
		"file":           file,
		"sources":        sources,
		"sourcesContent": contents,
		"names":          names,
		"mappings":       sb.String(),
	}
	return json.Marshal(doc)
}
