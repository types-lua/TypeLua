package mod

import (
	"fmt"
	"sort"
)

// LockEntry pins one module: exact version plus the commit it resolved to.
// Direct marks manifest-listed modules; the rest are indirect.
type LockEntry struct {
	Path    string
	Version string
	Commit  string
	Direct  bool
}

// Lock is tlua.lock.
type Lock struct {
	Entries []LockEntry
}

// NewLock creates an empty lock.
func NewLock() *Lock {
	return &Lock{}
}

// Lookup returns the entry for path, if present.
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
