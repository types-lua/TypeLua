// Package mod implements the TypeLua package system: a decentralized,
// git-based module manager with a TOML manifest (tlua.toml) and an exact
// lockfile (tlua.lock), in the spirit of go.mod/go.sum.
package mod

import (
	"fmt"
	"regexp"
	"strings"
)

// Manifest file names.
const (
	ManifestName = "tlua.toml"
	LockName     = "tlua.lock"
	// VendorDir holds vendored module sources for builds.
	VendorDir = "tlua_modules"
)

// pathRe validates module paths: host-like first segment plus slashes.
// 第一段必须包含点（去中心化托管），除非依赖覆盖了拉取 URL。
var pathRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._~%-]*/[A-Za-z0-9._~%-]+(/[A-Za-z0-9._~%-]+)*$`)

// ValidPath reports whether p is a plausible module path.
//
// 设计者视角：径须 `host/x` 形（首段字母数起，段字符集限），`..` 禁。障碍是单段：
// pathRe 强制含 `/`。候选：正则 vs 手判；选正则稳。不变量：`..` 恒 false。排查现场：
// 径被拒先查字符集。
func ValidPath(p string) bool {
	if !pathRe.MatchString(p) {
		return false
	}
	if strings.Contains(p, "..") {
		return false
	}
	return true
}

// ValidHostPath additionally requires a dotted first segment.
//
// 设计者视角：去中心化要求首段带点（`example.com/x`），显式 URL/path 可豁（见
// Validate）。障碍是自托管：URL 覆可无点。排查现场：自托管被拒先查覆写。
func ValidHostPath(p string) bool {
	if !ValidPath(p) {
		return false
	}
	first := p
	if i := strings.Index(p, "/"); i >= 0 {
		first = p[:i]
	}
	return strings.Contains(first, ".")
}

// Dep is one manifest dependency (Cargo-style):
//
// 设计者视角：Dep 三选一源——Query 版询、URL 源覆、Path 本地替（互斥，后两者与版
// 关系见 Validate）。障碍是三源混填：URL+Path 互斥。候选：多源 vs 单源；选显式互斥。
// 不变量：URL 与 Path 互斥。排查现场：源错先查互斥。
//
//	"example.com/greet" = "^v1.0.0"                          (version shorthand)
//	"example.com/notes" = { version = "^v0.2.0" }             (explicit)
//	"example.com/words" = { version = "^v1.0.0", url = "..." } (self-hosted fetch source)
//	"example.com/local" = { path = "./deps/local" }           (local directory, versions bypassed)
type Dep struct {
	Query string // "latest", exact "v1.2.3", caret "^v1.2.0", branch, or commit SHA
	URL   string // optional fetch-source override
	Path  string // optional local directory (replaces fetching entirely)
}

// Manifest is tlua.toml.
//
// 设计者视角：Name+Version+Deps+Replace 四件，Deps 径→求，Replace 径→本地。障碍是
// 空 Deps nil 图：NewManifest 给空图防 nil 写。排查现场：nil map 写 panic 先查构造。
type Manifest struct {
	Name    string
	Version string
	Deps    map[string]*Dep // module path -> requirement
	Replace map[string]string
}

// NewManifest creates an empty manifest.
//
// 设计者视角：空 Deps/Replace 双图，免调用方判 nil。障碍是零值 Manifest：Deps nil 写
// 即 panic；故构造。排查现场：map 写 panic 先查此。
func NewManifest(name string) *Manifest {
	return &Manifest{Name: name, Deps: map[string]*Dep{}, Replace: map[string]string{}}
}

// Validate checks manifest invariants.
//
// 设计者视角：五律——名非空、径合法、无点径须 URL/Path 覆、URL⊕Path、版询合法
// （裸 URL 可追 latest，裸 Path 免版）。障碍是自托管无点径：URL 覆即放。候选：严 vs
// 松；选严保可解。不变量：过即解析器可跑。排查现场：`get` 拒先查此。
func (m *Manifest) Validate() error {
	if strings.TrimSpace(m.Name) == "" {
		return fmt.Errorf("package name is empty")
	}
	for p, d := range m.Deps {
		if !ValidPath(p) {
			return fmt.Errorf("invalid module path %q", p)
		}
		if d.URL == "" && d.Path == "" && !ValidHostPath(p) {
			return fmt.Errorf("module path %q needs a dotted host, an explicit url, or a path", p)
		}
		if d.URL != "" && d.Path != "" {
			return fmt.Errorf("module %q: url and path are exclusive", p)
		}
		if d.Path == "" {
			if strings.TrimSpace(d.Query) == "" {
				// A bare url without version tracks latest.
				if d.URL == "" {
					return fmt.Errorf("module %q needs a version query", p)
				}
			} else if _, err := ParseQuery(d.Query); err != nil {
				return fmt.Errorf("module %q: %v", p, err)
			}
		}
	}
	for p, r := range m.Replace {
		if !ValidPath(p) {
			return fmt.Errorf("invalid replace path %q", p)
		}
		if strings.TrimSpace(r) == "" {
			return fmt.Errorf("empty replace target for %q", p)
		}
	}
	return nil
}
