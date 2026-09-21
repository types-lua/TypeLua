package mod

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// Version is a parsed semantic version (leading `v` optional).
type Version struct {
	Major, Minor, Patch int
	Pre                 string // prerelease, "" when absent
	Raw                 string // canonical "v1.2.3" form (without prerelease details lost)
}

// ParseVersion parses strict `vMAJOR.MINOR.PATCH[-prerelease]` (`v` optional,
// `+build` ignored for precedence but preserved in Raw).
func ParseVersion(s string) (Version, error) {
	orig := s
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "v")
	build := ""
	if i := strings.Index(s, "+"); i >= 0 {
		build = s[i:]
		s = s[:i]
	}
	pre := ""
	if i := strings.Index(s, "-"); i >= 0 {
		pre = s[i+1:]
		s = s[:i]
		if pre == "" {
			return Version{}, fmt.Errorf("invalid version %q", orig)
		}
		for _, id := range strings.Split(pre, ".") {
			if id == "" {
				return Version{}, fmt.Errorf("invalid version %q", orig)
			}
			for i := 0; i < len(id); i++ {
				c := id[i]
				if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c == '-') {
					return Version{}, fmt.Errorf("invalid version %q", orig)
				}
			}
		}
	}
	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return Version{}, fmt.Errorf("invalid version %q (want vMAJOR.MINOR.PATCH)", orig)
	}
	nums := make([]int, 3)
	for i, p := range parts {
		if p == "" {
			return Version{}, fmt.Errorf("invalid version %q", orig)
		}
		// No leading zeros (semver).
		if len(p) > 1 && p[0] == '0' {
			return Version{}, fmt.Errorf("invalid version %q", orig)
		}
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return Version{}, fmt.Errorf("invalid version %q", orig)
		}
		nums[i] = n
	}
	raw := fmt.Sprintf("v%d.%d.%d", nums[0], nums[1], nums[2])
	if pre != "" {
		raw += "-" + pre
	}
	if build != "" {
		raw += build
	}
	return Version{Major: nums[0], Minor: nums[1], Patch: nums[2], Pre: pre, Raw: raw}, nil
}

// Compare orders versions: -1, 0, +1 (prerelease < release; prerelease
// identifiers compare per semver: numeric < alphanumeric, numeric numerically).
func (v Version) Compare(o Version) int {
	if v.Major != o.Major {
		return cmpInt(v.Major, o.Major)
	}
	if v.Minor != o.Minor {
		return cmpInt(v.Minor, o.Minor)
	}
	if v.Patch != o.Patch {
		return cmpInt(v.Patch, o.Patch)
	}
	return comparePre(v.Pre, o.Pre)
}

func cmpInt(a, b int) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

func comparePre(a, b string) int {
	if a == b {
		return 0
	}
	if a == "" {
		return 1 // release > prerelease
	}
	if b == "" {
		return -1
	}
	as, bs := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(as) && i < len(bs); i++ {
		an, aerr := strconv.Atoi(as[i])
		bn, berr := strconv.Atoi(bs[i])
		switch {
		case aerr == nil && berr == nil:
			if an != bn {
				return cmpInt(an, bn)
			}
		case aerr == nil:
			return -1 // numeric < alphanumeric
		case berr == nil:
			return 1
		default:
			if as[i] != bs[i] {
				if as[i] < bs[i] {
					return -1
				}
				return 1
			}
		}
	}
	return cmpInt(len(as), len(bs))
}

// QueryKind classifies a version query.
type QueryKind int

const (
	QueryLatest QueryKind = iota
	QueryExact
	QueryCaret
	QueryRevision // branch name or commit SHA (resolved by the fetcher)
)

// Query is a parsed manifest version query.
type Query struct {
	Kind QueryKind
	// Exact/Caret base version.
	Base Version
	// Raw revision for QueryRevision.
	Rev string
	// Original text (for messages and lockfile fidelity of exact pins).
	Text string
}

// ParseQuery parses "latest", exact versions, carets, or revisions.
func ParseQuery(s string) (Query, error) {
	t := strings.TrimSpace(s)
	if t == "" {
		return Query{}, fmt.Errorf("empty version query")
	}
	if t == "latest" {
		return Query{Kind: QueryLatest, Text: t}, nil
	}
	if strings.HasPrefix(t, "^") {
		v, err := ParseVersion(strings.TrimPrefix(t, "^"))
		if err != nil {
			return Query{}, err
		}
		return Query{Kind: QueryCaret, Base: v, Text: t}, nil
	}
	if v, err := ParseVersion(t); err == nil {
		return Query{Kind: QueryExact, Base: v, Text: t}, nil
	}
	// Otherwise a branch name or commit SHA; validated by the fetcher.
	if strings.ContainsAny(t, " \t\n\"'") {
		return Query{}, fmt.Errorf("invalid version query %q", s)
	}
	return Query{Kind: QueryRevision, Rev: t, Text: t}, nil
}

// Matches reports whether v satisfies a caret/exact query (latest and
// revisions are resolved by the fetcher, never matched here).
func (q Query) Matches(v Version) bool {
	switch q.Kind {
	case QueryExact:
		return v.Compare(q.Base) == 0
	case QueryCaret:
		if v.Compare(q.Base) < 0 {
			return false
		}
		// npm-style upper bounds.
		switch {
		case q.Base.Major > 0:
			return v.Major == q.Base.Major
		case q.Base.Minor > 0:
			return v.Major == 0 && v.Minor == q.Base.Minor
		default:
			return v.Major == 0 && v.Minor == 0 && v.Patch == q.Base.Patch
		}
	}
	return false
}

// MaxVersion returns the highest version in vs (nil when empty).
func MaxVersion(vs []Version) *Version {
	if len(vs) == 0 {
		return nil
	}
	best := vs[0]
	for _, v := range vs[1:] {
		if v.Compare(best) > 0 {
			best = v
		}
	}
	return &best
}

// SortVersions sorts ascending.
func SortVersions(vs []Version) {
	sort.Slice(vs, func(i, j int) bool { return vs[i].Compare(vs[j]) < 0 })
}
