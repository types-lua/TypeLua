package types

import (
	"strings"
)

// Abbrev limits bracket nesting and total length of a type string for
// human-facing messages (ultra-deep types otherwise flood diagnostics).
// Structure outside the budget is preserved; cut content becomes "...".
// Only double quotes suspend bracket counting: messages wrap types in
// single quotes, which must not hide nesting (apostrophes in prose are
// harmless for the same reason).
// Normal messages (nesting well under the budget) pass through byte
// identical, so message-matching tests are unaffected.
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
