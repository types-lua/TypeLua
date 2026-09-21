package mod

import (
	"context"
	"fmt"
	"sort"
)

// Resolved is one MVS-selected module.
type Resolved struct {
	Path    string
	Version string // exact tag (or revision text)
	Commit  string
	Direct  bool
}

// Registry supplies tag listings and manifests for resolution.
// Implementations: git-backed cache (fetch.go), in-memory fakes (tests).
type Registry interface {
	// Tags returns tag name -> commit for the module's repository.
	Tags(ctx context.Context, path string, dep Dep) (map[string]string, error)
	// Manifest returns the module's tlua.toml at commit
	// (nil manifest = leaf module without dependencies).
	Manifest(ctx context.Context, path, commit string) (*Manifest, error)
	// ResolveRev resolves a branch name or commit SHA to a commit.
	ResolveRev(ctx context.Context, path string, dep Dep, rev string) (string, error)
	// LocalDir reports a locally-forced content directory for path
	// (root manifest replaces and discovered transitive path links).
	LocalDir(path string) (string, bool)
}

// Resolve runs Minimal Version Selection over the root manifest,
// returning the full module closure (direct + indirect) as a lock.
func Resolve(ctx context.Context, root *Manifest, reg Registry) (*Lock, error) {
	l, _, err := resolveWith(ctx, root, reg)
	return l, err
}

// resolveWith also returns the fetch source (explicit URL or "") per path.
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
	// queries accumulates version queries per path.
	queries map[string][]Query
	// sources pins the fetch source per path (url override or derived).
	sources map[string]string
	// selected holds MVS results.
	selected map[string]*Resolved
	// processed tracks (path, version) pairs already expanded.
	processed map[string]bool
}

func (r *resolver) run(ctx context.Context) error {
	r.queries = map[string][]Query{}
	r.sources = map[string]string{}
	r.selected = map[string]*Resolved{}
	r.processed = map[string]bool{}
	for p, d := range r.root.Deps {
		if _, ok := r.reg.LocalDir(p); ok {
			// Locally forced content: versions bypassed entirely. The
			// queue still expands it for transitive requirements below.
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
	// Worklist of paths needing (re)selection.
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
				// Content forced locally; queue for expansion only.
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

// localSeeds lists root requirements served from local directories.
func (r *resolver) localSeeds() map[string]bool {
	out := map[string]bool{}
	for p := range r.root.Deps {
		if _, ok := r.reg.LocalDir(p); ok {
			out[p] = true
		}
	}
	return out
}

// selectLocal selects locally-forced content by manifest version.
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

// pinSource records the fetch URL for a path, rejecting conflicts.
func (r *resolver) pinSource(p string, d Dep) error {
	url := d.URL
	if prev, ok := r.sources[p]; ok && prev != url {
		return fmt.Errorf("module %q has conflicting sources %q vs %q (align urls or add a replace)", p, prev, url)
	}
	r.sources[p] = url
	return nil
}

// selectOne picks the MVS version for p; true when (re)selected.
func (r *resolver) selectOne(ctx context.Context, p string) (bool, error) {
	qs := r.queries[p]
	// Revision queries must agree (incomparable otherwise).
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

// selectRevision resolves a branch/SHA pin.
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

// selectVersion picks max(minima) over semver queries (MVS).
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
					// Same version, different tag spellings ("v1.2.3" vs
					// "1.2.3"): prefer the query's own spelling.
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
		// An exact pin dominates: carets must still accept it.
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

// lock converts the selection into a Lock (direct = root-listed).
func (r *resolver) lock() *Lock {
	l := NewLock()
	for p, s := range r.selected {
		_, direct := r.root.Deps[p]
		l.Set(LockEntry{Path: p, Version: s.Version, Commit: s.Commit, Direct: direct})
	}
	return l
}

func sortedKeys[V any](m map[string]V) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}
