package sourcemap

import (
	"encoding/json"
	"strings"
)

// Mapping 把一个生成位置映射回原始源位置（Source Map V3）。
//
// 物理布局：
//
//	Mapping
//	├── GenLine int    // 生成行（1 起）
//	├── GenCol  int    // 生成列（0 起，rune）
//	├── SrcFile string // 原始文件
//	├── SrcLine int    // 原始行（0 起）
//	├── SrcCol  int    // 原始列（0 起）
//	└── Name    string // 名称表条目
type Mapping struct {
	GenLine int // 1 起
	GenCol  int // 0 起，rune
	SrcFile string
	SrcLine int // 0 起
	SrcCol  int // 0 起
	Name    string
}

const base64Chars = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"

// AppendVLQ 追加一个 base64-VLQ 值（规范：符号在 LSB）。
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

// Encode 构建 Source Map V3 JSON 文档。Mappings 必须按 (GenLine, GenCol) 排序；
// sources/content 按索引对齐。
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
