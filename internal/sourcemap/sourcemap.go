package sourcemap

import (
	"encoding/json"
	"strings"
)

// Mapping 把一个生成位置映射回原始源位置详见实现，排查看 Span。
//
// 设计者视角：Mapping 是调试器的“路标”——Gen(1 起行/0 起 rune 列)钉生成锚，
// Src(0 起行列+文件)指回源，Name 供按名跳。障碍是列制：生成列须 rune 而非 byte，
// 否则中文源码全偏（见 emitter.write）。候选：byte vs rune；选 rune 合 V3 实情。
// 不变量：GenLine>=1。排查现场：映射漂移先查 emitter 的 line/col 账。
//
// 物理布局：
//
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
//
// 设计者视角：AppendVLQ 是 V3 的“打包机”——负数 `(-v<<1|1)`、非负 `(v<<1)`，
// 5 位一组+续位 32，查 base64 表。障碍是 int 位宽：转 uint32 后逐 5 位吞，无溢出。
// 候选：手写 vs 库；选手写零依赖。不变量：零值产一字节。排查现场：映射串乱码先查
// 符号位与续位。
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
//
// 设计者视角：Encode 把映射压成 `mappings` 差分串——行差用 `;` 跳，列/源/行/列/名
// 全 VLQ 差分。障碍是乱序输入：未排序即差分负跳错乱，故要求调用方排好（emitter
// 顺写天然有序）；障碍是未知源：自动追 sources/contents 补空。候选：绝对 vs 差分；
// 选差分合规范。不变量：sources 与 contents 同长。排查现场：行错位先查 prelude
// shift（见 PrependPrelude），名缺失先查 names 表。
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
