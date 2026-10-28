package mod

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// Version is a parsed semantic version (leading `v` optional).
//
// 设计者视角：三数+预发+Raw（规范 `vM.m.p[-pre][+build]`，build 不参比但留 Raw）。
// 障碍是前导零：semver 禁（`01` 非法）。候选：宽 vs 严；选严合 semver。不变量：
// Raw 恒规范。排查现场：版被拒先查前导零/三段。
type Version struct {
	Major, Minor, Patch int
	Pre                 string // prerelease, "" when absent
	Raw                 string // canonical "v1.2.3" form (without prerelease details lost)
}

// ParseVersion parses strict `vMAJOR.MINOR.PATCH[-prerelease]` (`v` optional,
// `+build` ignored for precedence but preserved in Raw).
//
// 设计者视角：去 `v`→切 build→切 pre（逐 id 字符集验）→三数验（非空、无前导零、
// 非负）→拼 Raw。障碍是空 pre 段（`v1-`）：拒；build 只留不比。候选：lenient vs
// strict；选 strict。不变量：错恒带原串。排查现场：版错先查段数。
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
		// 排查看 Span/诊断，如 `local x=1` 错圈整句。
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

// 排查看 Span/诊断，如 `local x=1` 错圈整句。
// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：主次补逐比，末比 pre（空=正式胜）。障碍是数字 vs 字母 id：数字小。
// 候选：字串比 vs semver 比；选后者。排查现场：选版错先查 pre 律。
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
	// 设计者视角：三值比小件。排查现场：符号反先查此。
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

func comparePre(a, b string) int {
	// 设计者视角：pre 空胜（正式>预发），逐 id 数字/字母分律，短 prefix 小。障碍是
	// `1` vs `alpha`：数小。排查现场：预发选错先查此。
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

// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：四态——latest 追新、exact 钉死、caret 兼容、revision 分支/SHA 快照。
// 障碍是 revision 与版串歧义：版解析成先判版，不成方 revision。排查现场：查询错类
// 先查 ParseQuery 序。
type QueryKind int

const (
	QueryLatest QueryKind = iota
	QueryExact
	QueryCaret
	QueryRevision // branch name or commit SHA (resolved by the fetcher)
)

// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：Kind+Base/Rev+Text（Text 留 lock 保真）。障碍是 exact 钉死须留原串。
// 不变量：Text 恒原。排查现场：lock 版串变先查 Text。
type Query struct {
	Kind QueryKind
	// 排查看 Span/诊断，如 `local x=1` 错圈整句。
	Base Version
	// 排查看 Span/诊断，如 `local x=1` 错圈整句。
	Rev string
	// 排查看 Span/诊断，如 `local x=1` 错圈整句。
	Text string
}

// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：`latest`→Latest，`^`→Caret（内版须合法），版串→Exact，余（无空白引号）
// →Revision 交 fetcher。障碍是空/含空白：拒。候选：版优先 vs 修订优先；选版优先。
// 排查现场：分支名被当版错先查字符。
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
	// 排查看 Span/诊断，如 `local x=1` 错圈整句。
	if strings.ContainsAny(t, " \t\n\"'") {
		return Query{}, fmt.Errorf("invalid version query %q", s)
	}
	return Query{Kind: QueryRevision, Rev: t, Text: t}, nil
}

// 排查看 Span/诊断，如 `local x=1` 错圈整句。
// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：exact 等值，caret 下限+npm 上界（Major>0 锁主，0.Minor>0 锁次，
// 0.0.x 锁补丁）。latest/rev 此处恒 false（fetcher 决）。障碍是 0 版上界特律。
// 排查现场：兼容选错先查上界。
func (q Query) Matches(v Version) bool {
	switch q.Kind {
	case QueryExact:
		return v.Compare(q.Base) == 0
	case QueryCaret:
		if v.Compare(q.Base) < 0 {
			return false
		}
		// 排查看 Span/诊断，如 `local x=1` 错圈整句。
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

// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：线性取大，空返 nil（latest 无版时上游定）。排查现场：空 panic 先查 nil。
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

// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：Compare 升序，供 MVS 取大。障碍是预发序：Compare 已含。排查现场：版序
// 错先查 Compare。
func SortVersions(vs []Version) {
	sort.Slice(vs, func(i, j int) bool { return vs[i].Compare(vs[j]) < 0 })
}
