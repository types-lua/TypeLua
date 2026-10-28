package mod

import (
	"fmt"
	"sort"
)

// LockEntry pins one module: exact version plus the commit it resolved to.
// Direct marks manifest-listed modules; the rest are indirect.
// Dir records a local source directory (project-relative, forward
// slashes) for locally-forced entries; empty otherwise.
//
// 设计者视角：lock 一行一钉——Path+Version+Commit 三元，Direct 标根单，Dir 记本地源
// （相对斜杠跨检出可携）。障碍是本地无 commit：`local` 占位+Dir 真址。候选：绝对 vs
// 相对；选相对可携。不变量：Dir 空即远程。排查现场：跨机 vendor 错先查 Dir。
type LockEntry struct {
	Path    string
	Version string
	Commit  string
	Direct  bool
	Dir     string
}

// Lock is tlua.lock.
//
// 设计者视角：lock 是“钉死单”——Entries 全闭包精确版+commit。障碍是手改：Validate
// 守三律（径合法、去重、版+commit 非空）。排查现场：构建漂移先看 lock。
type Lock struct {
	Entries []LockEntry
}

// NewLock creates an empty lock.
//
// 设计者视角：空 lock 供 resolver 渐填（见 resolve.go）。障碍是 nil 图：返空体非 nil。
// 排查现场：空指针先查此。
func NewLock() *Lock {
	return &Lock{}
}

// Lookup returns the entry for path, if present.
//
// 设计者视角：线性查（模块数小），nil lock 宽容返 nil。障碍是大小写：精确比。
// 排查现场：钉住未命中先查径。
func (l *Lock) Lookup(path string) *LockEntry {
	if l == nil {
		return nil
	}
	for i := range l.Entries {
		if l.Entries[i].Path == path {
			return &l.Entries[i]
		}
	}
	return nil
}

// Set inserts or replaces an entry, keeping path order deterministic.
//
// 设计者视角：按 Path 覆/增后排序，保 Encode 稳。障碍是重复：覆不增。排查现场：
// lock 抖动先查排序。
func (l *Lock) Set(e LockEntry) {
	for i := range l.Entries {
		if l.Entries[i].Path == e.Path {
			l.Entries[i] = e
			return
		}
	}
	l.Entries = append(l.Entries, e)
	sort.Slice(l.Entries, func(i, j int) bool { return l.Entries[i].Path < l.Entries[j].Path })
}

// Validate checks lock invariants.
//
// 设计者视角：三律——径合法、去重、版与 commit 非空。障碍是手改 lock：此处拦。
// 候选：宽 vs 严；选严。不变量：过即合法。排查现场：fetch 拒先查此。
func (l *Lock) Validate() error {
	seen := map[string]bool{}
	for _, e := range l.Entries {
		if !ValidPath(e.Path) {
			return fmt.Errorf("invalid module path %q", e.Path)
		}
		if seen[e.Path] {
			return fmt.Errorf("duplicate lock entry %q", e.Path)
		}
		seen[e.Path] = true
		if e.Version == "" || e.Commit == "" {
			return fmt.Errorf("lock entry %q needs version and commit", e.Path)
		}
	}
	return nil
}
