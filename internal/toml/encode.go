package toml

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// Builder emits deterministic TOML for fixed schemas (manifests, locks).
type Builder struct {
	sb strings.Builder
}

// String returns the built document (ends with exactly one newline,
// or empty if nothing was written).
func (b *Builder) String() string {
	s := b.sb.String()
	if s == "" {
		return ""
	}
	return strings.TrimRight(s, "\n") + "\n"
}

// Table starts `[a.b."c"]` (segments quoted as needed).
func (b *Builder) Table(segs ...string) {
	b.sb.WriteString("[" + joinKey(segs) + "]\n")
}

// ArrayTable starts `[[a.b]]`.
func (b *Builder) ArrayTable(segs ...string) {
	b.sb.WriteString("[[" + joinKey(segs) + "]]\n")
}

// Blank writes an empty line.
func (b *Builder) Blank() {
	b.sb.WriteString("\n")
}

// Comment writes a `#` line.
func (b *Builder) Comment(s string) {
	b.sb.WriteString("# " + s + "\n")
}

func joinKey(segs []string) string {
	out := make([]string, len(segs))
	for i, s := range segs {
		out[i] = KeyString(s)
	}
	return strings.Join(out, ".")
}

// KeyString renders one key segment, quoting when necessary.
func KeyString(s string) string {
	bare := true
	if s == "" {
		bare = false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '_' || c == '-') {
			bare = false
			break
		}
	}
	if bare {
		return s
	}
	return strconv.Quote(s)
}

// Str writes `key = "value"`.
func (b *Builder) Str(key, val string) {
	fmt.Fprintf(&b.sb, "%s = %s\n", KeyString(key), strconv.Quote(val))
}

// Int writes `key = 123`.
func (b *Builder) Int(key string, val int64) {
	fmt.Fprintf(&b.sb, "%s = %d\n", KeyString(key), val)
}

// Bool writes `key = true`.
func (b *Builder) Bool(key string, val bool) {
	if val {
		fmt.Fprintf(&b.sb, "%s = true\n", KeyString(key))
	} else {
		fmt.Fprintf(&b.sb, "%s = false\n", KeyString(key))
	}
}

// StrArray writes `key = ["a", "b"]`.
func (b *Builder) StrArray(key string, vals []string) {
	parts := make([]string, len(vals))
	for i, v := range vals {
		parts[i] = strconv.Quote(v)
	}
	fmt.Fprintf(&b.sb, "%s = [%s]\n", KeyString(key), strings.Join(parts, ", "))
}

// SortedKeys returns sorted map keys (deterministic emission).
func SortedKeys[V any](m map[string]V) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}
