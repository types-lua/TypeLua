package types

import (
	"strings"
)

// Abbrev limits bracket nesting and total length of a type string for
// human-facing messages (ultra-deep types otherwise flood diagnostics).
// Structure outside the budget is preserved; cut content becomes "...".
// 仅双引号会暂停括号计数：消息用单引号包裹类型，因此不得隐藏嵌套
// （散文中的撇号同理无害）。
// Normal messages (nesting well under the budget) pass through byte
// identical, so message-matching tests are unaffected.
//
// 设计者视角：诊断消息里类型字符串可能深达数千层（泛型实例化链），直接打印
// 会把报错与日志冲垮。候选方案有三：截断整体字符串、限制递归深度、按括号
// 结构裁剪。前两者会破坏消息匹配测试与可读性；本实现按括号嵌套深度裁剪，
// 超预算的子树折叠为 "..."，其余字节原样通过，保证正常消息 byte-identical。
// 不变量：只在双引号内暂停括号计数——因为消息用单引号包裹类型，双引号
// 内的括号属于字符串字面量，不得误计；撇号在散文中无害。排查现场：若测试
// 期望的缩写边界不对，先检查 maxDepth/maxLen 与引号状态机，而不是改消息。
func Abbrev(s string) string {
	const maxDepth = 8
	const maxLen = 1500
	var sb strings.Builder
	depth := 0
	// skipTo[i] is the depth to return to before resuming output.
	skipping := false
	skipDepth := 0
	inStr := byte(0)
	escaped := false
	runes := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		if inStr != 0 {
			sb.WriteByte(c)
			runes++
			if escaped {
				escaped = false
			} else if c == '\\' {
				escaped = true
			} else if c == inStr {
				inStr = 0
			}
			continue
		}
		if c == '"' {
			inStr = c
			sb.WriteByte(c)
			runes++
			continue
		}
		switch c {
		case '{', '[', '(':
			depth++
			if depth > maxDepth && !skipping {
				sb.WriteString("...")
				skipping = true
				skipDepth = depth - 1
				// Swallow the opener itself.
				continue
			}
		case '}', ']', ')':
			if skipping && depth-1 == skipDepth {
				// Closed back to the cut level: resume after it.
				depth--
				skipping = false
				continue
			}
			if depth > 0 {
				depth--
			}
		}
		if skipping {
			continue
		}
		sb.WriteByte(c)
		runes++
		if runes >= maxLen {
			sb.WriteString("...")
			return sb.String()
		}
	}
	return sb.String()
}
