package mod

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/typelua/typelua/internal/toml"
)

// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：Decode→取 package 表（名+版，未知键拒）→取 dependencies（串=版询，
// 内联表=version/url/path，裸 URL 追 latest）→path 依赖兼作 replace→禁 [replace] 表
// （已迁 path 式）→拒未知顶表→Validate。障碍是旧 [replace]：显错指迁；障碍是裸 URL
// 无版：latest。候选：宽容未知键 vs 严拒；选严拒防笔误。不变量：过即 Validate 过。
// 排查现场：解析拒先查未知键。
func ParseManifest(src string) (*Manifest, error) {
	d, err := toml.Decode(src)
	if err != nil {
		return nil, err
	}
	m := NewManifest("")
	if pt, ok := d.Tables["package"]; ok {
		if v, ok := pt.Values["name"]; ok {
			if v.Kind != toml.String {
				return nil, fmt.Errorf("package.name must be a string")
			}
			m.Name = v.Str
		}
		if v, ok := pt.Values["version"]; ok {
			if v.Kind != toml.String {
				return nil, fmt.Errorf("package.version must be a string")
			}
			m.Version = v.Str
		}
		for k := range pt.Values {
			if k != "name" && k != "version" {
				return nil, fmt.Errorf("unknown package key %q", k)
			}
		}
	}
	const depTable = "dependencies"
	if dt, ok := d.Tables[depTable]; ok {
		for path, v := range dt.Values {
			dep := &Dep{}
			switch v.Kind {
			case toml.String:
				dep.Query = v.Str
			case toml.Inline:
				for k, fv := range v.Map {
					if fv.Kind != toml.String {
						return nil, fmt.Errorf("dependency %q: %q must be a string", path, k)
					}
					switch k {
					case "version":
						dep.Query = fv.Str
					case "url":
						dep.URL = fv.Str
					case "path":
						dep.Path = fv.Str
					default:
						return nil, fmt.Errorf("dependency %q: unknown key %q", path, k)
					}
				}
			default:
				return nil, fmt.Errorf("dependency %q must be a version string or inline table", path)
			}
			if dep.Query == "" && dep.Path == "" {
				// 排查看 Span/诊断，如 `local x=1` 错圈整句。
				dep.Query = "latest"
			}
			m.Deps[path] = dep
		}
	}
	// 排查看 Span/诊断，如 `local x=1` 错圈整句。
	for path, dep := range m.Deps {
		if dep.Path != "" {
			m.Replace[path] = dep.Path
		}
	}
	if _, ok := d.Tables["replace"]; ok {
		return nil, fmt.Errorf("the [replace] table was removed; use `{ path = ... }` inside [dependencies]")
	}
	// 排查看 Span/诊断，如 `local x=1` 错圈整句。
	// 排查看 Span/诊断，如 `local x=1` 错圈整句。
	for key := range d.Tables {
		if key == "" || key == "package" || key == "dependencies" {
			continue
		}
		return nil, fmt.Errorf("unknown table %q", key)
	}
	if len(d.Arrays) != 0 {
		return nil, fmt.Errorf("unexpected array table in manifest")
	}
	if err := m.Validate(); err != nil {
		return nil, err
	}
	return m, nil
}

// 排查看 Span/诊断，如 `local x=1` 错圈整句。
// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：按 package→dependencies 定序（径排序），纯版询用糖串，余用内联表，
// 保往返稳定 diff 小。障碍是 map 乱序：SortedKeys 定之。候选：多表 vs 单表；
// 选单表 Cargo 风。不变量：Parse(Encode(m))==m（语义）。排查现场：diff 抖先查排序。
func EncodeManifest(m *Manifest) string {
	var b toml.Builder
	b.Comment("tlua package manifest (tlua.toml)")
	b.Table("package")
	b.Str("name", m.Name)
	if m.Version != "" {
		b.Str("version", m.Version)
	}
	if len(m.Deps) != 0 {
		b.Blank()
		b.Table("dependencies")
		for _, p := range toml.SortedKeys(m.Deps) {
			d := m.Deps[p]
			key := toml.KeyString(p)
			switch {
			case d.Query != "" && d.URL == "" && d.Path == "":
				b.Str(p, d.Query)
			default:
				var parts []string
				if d.Query != "" {
					parts = append(parts, "version = "+quoteToml(d.Query))
				}
				if d.URL != "" {
					parts = append(parts, "url = "+quoteToml(d.URL))
				}
				if d.Path != "" {
					parts = append(parts, "path = "+quoteToml(d.Path))
				}
				b.Raw(key + " = { " + strings.Join(parts, ", ") + " }\n")
			}
		}
	}
	return b.String()
}

func quoteToml(s string) string {
	// 设计者视角：strconv.Quote 保 TOML 串转义。障碍是手拼引号漏转义；故库之。
	// 排查现场：含引号依赖名错先查此。
	return strconv.Quote(s)
}

// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：逐 [[module]] 取 path/version/commit/direct/dir（错型即错，未知键拒），
// 去重+Set 排序，禁多余表，末 Validate。障碍是手改 lock：严拒。候选：宽 vs 严；
// 选严。不变量：过即 Validate 过。排查现场：lock 拒先查未知键/重复。
func ParseLock(src string) (*Lock, error) {
	d, err := toml.Decode(src)
	if err != nil {
		return nil, err
	}
	l := NewLock()
	seen := map[string]bool{}
	for _, t := range d.Arrays["module"] {
		e := LockEntry{}
		for k, v := range t.Values {
			switch k {
			case "path":
				if v.Kind != toml.String {
					return nil, fmt.Errorf("module.path must be a string")
				}
				e.Path = v.Str
			case "version":
				if v.Kind != toml.String {
					return nil, fmt.Errorf("module.version must be a string")
				}
				e.Version = v.Str
			case "commit":
				if v.Kind != toml.String {
					return nil, fmt.Errorf("module.commit must be a string")
				}
				e.Commit = v.Str
			case "direct":
				if v.Kind != toml.Boolean {
					return nil, fmt.Errorf("module.direct must be a boolean")
				}
				e.Direct = v.Bool
			case "dir":
				if v.Kind != toml.String {
					return nil, fmt.Errorf("module.dir must be a string")
				}
				e.Dir = v.Str
			default:
				return nil, fmt.Errorf("unknown module key %q", k)
			}
		}
		if e.Path == "" {
			return nil, fmt.Errorf("module entry without path")
		}
		if seen[e.Path] {
			return nil, fmt.Errorf("duplicate lock entry %q", e.Path)
		}
		seen[e.Path] = true
		l.Set(e)
	}
	if len(d.Tables) > 1 || (len(d.Tables) == 1 && !hasOnlyRoot(d)) {
		return nil, fmt.Errorf("unexpected table in lockfile")
	}
	if err := l.Validate(); err != nil {
		return nil, err
	}
	return l, nil
}

func hasOnlyRoot(d *toml.Doc) bool {
	// 设计者视角：lock 只允根表（空键），余皆拒。障碍是误加 [x]：拦。排查现场：lock
	// 拒先查多余表。
	for k := range d.Tables {
		if k != "" {
			return false
		}
	}
	return true
}

// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：按径排序逐 [[module]]，dir 非空方写，保 diff 稳。障碍是乱序：先排。
// 不变量：Parse(Encode(l)) 往返。排查现场：lock 抖先查排序。
func EncodeLock(l *Lock) string {
	var b toml.Builder
	b.Comment("tlua lockfile (tlua.lock) - exact pins; edit via `tlua mod get`")
	entries := append([]LockEntry(nil), l.Entries...)
	sort.Slice(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })
	for i, e := range entries {
		if i > 0 {
			b.Blank()
		}
		b.ArrayTable("module")
		b.Str("path", e.Path)
		b.Str("version", e.Version)
		b.Str("commit", e.Commit)
		b.Bool("direct", e.Direct)
		if e.Dir != "" {
			b.Str("dir", e.Dir)
		}
	}
	return b.String()
}
