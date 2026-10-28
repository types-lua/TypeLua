package mod

import (
	"context"
	"fmt"
	"sort"
)

// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：Path+Version+Commit+Direct 四元，Version 为 tag 文（exact 原串保真），
// Commit 为钉死 SHA。障碍是 revision 钉：Version 存 rev 文、Commit 存 SHA。排查现场：
// 版 commit 对不上先查 select 分支。
type Resolved struct {
	Path    string
	Version string // exact tag (or revision text)
	Commit  string
	Direct  bool
}

// 排查看 Span/诊断，如 `local x=1` 错圈整句。
// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：四法供 MVS——Tags 列版、Manifest 取依赖、ResolveRev 定修订、LocalDir
// 指本地。障碍是叶模块：Manifest 返 nil。候选：接口 vs 具体；选接口可测。不变量：
// 叶恒 nil。排查现场：解析缺边先查 Manifest。
type Registry interface {
	// Tags returns tag name -> commit for the module's repository.
	Tags(ctx context.Context, path string, dep Dep) (map[string]string, error)
	// 排查看 Span/诊断，如 `local x=1` 错圈整句。
	// 排查看 Span/诊断，如 `local x=1` 错圈整句。
	Manifest(ctx context.Context, path, commit string) (*Manifest, error)
	// 排查看 Span/诊断，如 `local x=1` 错圈整句。
	ResolveRev(ctx context.Context, path string, dep Dep, rev string) (string, error)
	// 排查看 Span/诊断，如 `local x=1` 错圈整句。
	// 排查看 Span/诊断，如 `local x=1` 错圈整句。
	LocalDir(path string) (string, bool)
}

// 解析Minimal/Version，如 `minimal` 例，排查看 Span。
// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：MVS 薄封 resolveWith（丢源表），根先验。障碍是根非法：前置拦。
// 不变量：语义同 resolveWith 首返。排查现场：源需查用 resolveWith。
func Resolve(ctx context.Context, root *Manifest, reg Registry) (*Lock, error) {
	l, _, err := resolveWith(ctx, root, reg)
	return l, err
}

// Resolve如 `f(1)`，排查看 Span。
//
// 设计者视角：验根→run 闭包→lock+源表。障碍是 URL 覆冲突：pinSource 拒。排查现场：
// 源冲突先查 pin。
func resolveWith(ctx context.Context, root *Manifest, reg Registry) (*Lock, map[string]string, error) {
	if err := root.Validate(); err != nil {
		return nil, nil, err
	}
	r := &resolver{root: root, reg: reg}
	if err := r.run(ctx); err != nil {
		return nil, nil, err
	}
	return r.lock(), r.sources, nil
}

type resolver struct {
	root *Manifest
	reg  Registry
	// 排查看 Span/诊断，如 `local x=1` 错圈整句。
	queries map[string][]Query
	// 排查看 Span/诊断，如 `local x=1` 错圈整句。
	sources map[string]string
	// 排查看 Span/诊断，如 `local x=1` 错圈整句。
	selected map[string]*Resolved
	// 排查看 Span/诊断，如 `local x=1` 错圈整句。
	processed map[string]bool
}

func (r *resolver) run(ctx context.Context) error {
	// 设计者视角：MVS 工作列——根询入队（含本地钉源），循环 selectOne/selectLocal，
	// 选中未展者取 Manifest 展传递询（本地只展不选版）。障碍是同版重复展：
	// processed 去重；障碍是本地版旁路：不入 queries。候选：递归 vs 队列；选队列防
	// 深。排查现场：传递漏先查队列追加，循环不止先查 processed。
	r.queries = map[string][]Query{}
	r.sources = map[string]string{}
	r.selected = map[string]*Resolved{}
	r.processed = map[string]bool{}
	for p, d := range r.root.Deps {
		if _, ok := r.reg.LocalDir(p); ok {
			// 排查看 Span/诊断，如 `local x=1` 错圈整句。
			// 排查看 Span/诊断，如 `local x=1` 错圈整句。
			if err := r.pinSource(p, *d); err != nil {
				return err
			}
			continue
		}
		q, err := ParseQuery(d.Query)
		if err != nil {
			return fmt.Errorf("module %q: %v", p, err)
		}
		r.queries[p] = append(r.queries[p], q)
		if err := r.pinSource(p, *d); err != nil {
			return err
		}
	}
	// 排查看 Span/诊断，如 `local x=1` 错圈整句。
	queue := sortedKeys(r.queries)
	for p := range r.localSeeds() {
		queue = append(queue, p)
	}
	for len(queue) > 0 {
		p := queue[0]
		queue = queue[1:]
		if _, ok := r.reg.LocalDir(p); !ok {
			changed, err := r.selectOne(ctx, p)
			if err != nil {
				return err
			}
			if !changed {
				continue
			}
		} else if done, err := r.selectLocal(ctx, p); err != nil {
			return err
		} else if !done {
			continue
		}
		sel := r.selected[p]
		key := p + "\x00" + sel.Version
		if r.processed[key] {
			continue
		}
		r.processed[key] = true
		m, err := r.reg.Manifest(ctx, p, sel.Commit)
		if err != nil {
			return fmt.Errorf("module %q: %v", p, err)
		}
		if m == nil {
			continue
		}
		for dp, dd := range m.Deps {
			if _, ok := r.reg.LocalDir(dp); ok {
				// 排查看 Span/诊断，如 `local x=1` 错圈整句。
				queue = append(queue, dp)
				continue
			}
			q, err := ParseQuery(dd.Query)
			if err != nil {
				return fmt.Errorf("module %q requires %q: %v", p, dp, err)
			}
			r.queries[dp] = append(r.queries[dp], q)
			if err := r.pinSource(dp, *dd); err != nil {
				return err
			}
			queue = append(queue, dp)
		}
	}
	return nil
}

// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：根 Deps 中本地者为种子（须入队展传递）。障碍是传递本地：run 内另补。
// 排查现场：本地传递漏先查队列。
func (r *resolver) localSeeds() map[string]bool {
	out := map[string]bool{}
	for p := range r.root.Deps {
		if _, ok := r.reg.LocalDir(p); ok {
			out[p] = true
		}
	}
	return out
}

// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：本地版即 manifest Version（非法兜 `v0.0.0-local`），commit=`local`。
// 障碍是无版：兜底保可选。候选：旁路 vs 选版；选版保 lock 有版。排查现场：本地版错
// 先查 manifest。
func (r *resolver) selectLocal(ctx context.Context, p string) (bool, error) {
	m, err := r.reg.Manifest(ctx, p, "local")
	if err != nil {
		return false, fmt.Errorf("module %q: %v", p, err)
	}
	ver := "v0.0.0-local"
	if m != nil && m.Version != "" {
		if _, err := ParseVersion(m.Version); err == nil {
			ver = m.Version
		}
	}
	if cur := r.selected[p]; cur != nil && cur.Version == ver && cur.Commit == "local" {
		return false, nil
	}
	r.selected[p] = &Resolved{Path: p, Version: ver, Commit: "local"}
	return true, nil
}

// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：显式 URL 钉源，异源同径即冲突错（保单源）。障碍是空 URL：跳。
// 不变量：同径同源。排查现场：源冲突先查两处 Deps。
func (r *resolver) pinSource(p string, d Dep) error {
	url := d.URL
	if prev, ok := r.sources[p]; ok && prev != url {
		return fmt.Errorf("module %q has conflicting sources %q vs %q (align urls or add a replace)", p, prev, url)
	}
	r.sources[p] = url
	return nil
}

// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：按询类分流——revision 定点、latest 追 Tags 最大、exact/caret 走
// selectVersion。障碍是无 tag：latest 错。候选：合流 vs 分流；选分流清。排查现场：
// 选版错先查询类。
func (r *resolver) selectOne(ctx context.Context, p string) (bool, error) {
	qs := r.queries[p]
	// 排查看 Span/诊断，如 `local x=1` 错圈整句。
	var rev string
	var revSet bool
	var semQueries []Query
	for _, q := range qs {
		if q.Kind == QueryRevision {
			if !revSet {
				rev, revSet = q.Rev, true
			} else if rev != q.Rev {
				return false, fmt.Errorf("module %q has conflicting revisions %q vs %q", p, rev, q.Rev)
			}
			continue
		}
		semQueries = append(semQueries, q)
	}
	if revSet {
		if len(semQueries) != 0 {
			return false, fmt.Errorf("module %q mixes revision %q with version queries", p, rev)
		}
		return r.selectRevision(ctx, p, rev)
	}
	return r.selectVersion(ctx, p, semQueries)
}

// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：revision 经 Registry 定 commit，Version 存 rev 文（lock 保真）。
// 障碍是浮动分支：快照于此。排查现场：分支漂移先查 lock。
func (r *resolver) selectRevision(ctx context.Context, p, rev string) (bool, error) {
	if cur := r.selected[p]; cur != nil && cur.Version == rev {
		return false, nil
	}
	dep := Dep{Query: rev}
	if u := r.sources[p]; u != "" {
		dep.URL = u
	}
	commit, err := r.reg.ResolveRev(ctx, p, dep, rev)
	if err != nil {
		return false, fmt.Errorf("module %q: %v", p, err)
	}
	r.selected[p] = &Resolved{Path: p, Version: rev, Commit: commit}
	return true, nil
}

// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：MVS 取 max(各询下界)：exact 下界即自身、caret 下界即 Base、latest
// 遇 Tags 最大，候选 max 且须全询 Matches，已选低即升。障碍是 exact 冲突（两异钉）：
// 无交即错；障碍是已选够高：Matches 全过即 false。图：询→下界→max→验→选。候选：
// MVS vs 最新；选 MVS 保最小够用。不变量：选版恒全询过。排查现场：冲突先查双 exact。
func (r *resolver) selectVersion(ctx context.Context, p string, qs []Query) (bool, error) {
	dep := Dep{}
	if u := r.sources[p]; u != "" {
		dep.URL = u
	}
	tags, err := r.reg.Tags(ctx, p, dep)
	if err != nil {
		return false, fmt.Errorf("module %q: %v", p, err)
	}
	type tagged struct {
		v      Version
		tag    string
		commit string
	}
	var avail []tagged
	for tag, commit := range tags {
		v, err := ParseVersion(tag)
		if err != nil {
			continue // non-semver tags are invisible to version queries
		}
		if v.Pre != "" {
			continue // prereleases never auto-selected
		}
		avail = append(avail, tagged{v, tag, commit})
	}
	best := -1
	var exact *tagged
	for _, q := range qs {
		pick := -1
		switch q.Kind {
		case QueryLatest:
			for i, t := range avail {
				if pick < 0 || t.v.Compare(avail[pick].v) > 0 {
					pick = i
				}
			}
		case QueryExact:
			for i, t := range avail {
				if t.v.Compare(q.Base) == 0 {
					// 排查看 Span/诊断，如 `local x=1` 错圈整句。
					// 排查看 Span/诊断，如 `local x=1` 错圈整句。
					if pick < 0 || t.tag == q.Text {
						pick = i
					}
				}
			}
			if pick < 0 {
				return false, fmt.Errorf("module %q: no tag matches %q", p, q.Text)
			}
			if exact == nil {
				exact = &avail[pick]
			} else if avail[pick].v.Compare(exact.v) != 0 {
				return false, fmt.Errorf("module %q: conflicting exact pins %q vs %q", p, exact.tag, avail[pick].tag)
			}
			continue // exacts constrain below; not MVS minima
		case QueryCaret:
			for i, t := range avail {
				if q.Matches(t.v) && (pick < 0 || t.v.Compare(avail[pick].v) > 0) {
					pick = i
				}
			}
		}
		if pick < 0 {
			return false, fmt.Errorf("module %q: no version matches query %q", p, q.Text)
		}
		if best < 0 || avail[pick].v.Compare(avail[best].v) > 0 {
			best = pick
		}
	}
	if exact != nil {
		// 排查看 Span/诊断，如 `local x=1` 错圈整句。
		for _, q := range qs {
			if q.Kind == QueryCaret && !q.Matches(exact.v) {
				return false, fmt.Errorf("module %q: exact pin %q conflicts with %q", p, exact.tag, q.Text)
			}
		}
		sel := *exact
		if cur := r.selected[p]; cur != nil && cur.Version == sel.tag && cur.Commit == sel.commit {
			return false, nil
		}
		r.selected[p] = &Resolved{Path: p, Version: sel.tag, Commit: sel.commit}
		return true, nil
	}
	if best < 0 {
		return false, fmt.Errorf("module %q: no queries", p)
	}
	sel := avail[best]
	if cur := r.selected[p]; cur != nil && cur.Version == sel.tag && cur.Commit == sel.commit {
		return false, nil
	}
	r.selected[p] = &Resolved{Path: p, Version: sel.tag, Commit: sel.commit}
	return true, nil
}

// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：selected 按径排入 Entries，根单标 Direct。障碍是本地 Dir：此处空，
// backfill 另填。排查现场：Direct 错先查根集。
func (r *resolver) lock() *Lock {
	l := NewLock()
	for p, s := range r.selected {
		_, direct := r.root.Deps[p]
		l.Set(LockEntry{Path: p, Version: s.Version, Commit: s.Commit, Direct: direct})
	}
	return l
}

func sortedKeys[V any](m map[string]V) []string {
	// 设计者视角：键排序保工作列确定（map 随机须排）。障碍是泛型：V any。排查现场：
	// 抖动先查排序。
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}
