// Package watch implements tsc-style `--watch` polling for tlua.
//
// Design notes (correctness first):
//   - The watched set is derived from config.Config.ListFiles plus the
//     config files themselves (tlua.json/tlua.toml/tlua.lock), so watch
//     triggers exactly when the build input set changes — nothing more.
//   - Anything under the resolved outDir is never watched, so emitting
//     can never retrigger a rebuild (self-trigger loop).
//   - Polling (no external deps, works on every platform/FS) with
//     debounce coalescing: rapid saves collapse into one rebuild, and the
//     baseline is refreshed after every rebuild so edits made mid-build
//     are never lost.
package watch

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/typelua/typelua/internal/config"
)

// ChangeKind describes how a watched file differs from the baseline.
type ChangeKind string

const (
	Added    ChangeKind = "added"
	Removed  ChangeKind = "removed"
	Modified ChangeKind = "modified"
)

// Change is a single watched-file difference, sorted by Path by Diff.
type Change struct {
	Path string
	Kind ChangeKind
	// Config is true when the path is a config file (tlua.json/toml/lock)
	// rather than a program source.
	Config bool
}

// FileMeta is the identity we track per watched file.
type FileMeta struct {
	Size    int64
	ModTime int64 // UnixNano
	Config  bool
}

// Snapshot maps absolute-clean paths to their tracked identity.
type Snapshot map[string]FileMeta

// OutDirAbs resolves the effective output directory to an absolute path.
func OutDirAbs(cfg *config.Config) string {
	out := cfg.OutDir()
	if filepath.IsAbs(out) {
		return filepath.Clean(out)
	}
	return filepath.Clean(filepath.Join(cfg.Dir, out))
}

// underDir reports whether p is inside dir (boundary-aware).
func underDir(p, dir string) bool {
	if p == dir {
		return true
	}
	return strings.HasPrefix(p, dir+string(os.PathSeparator))
}

// abs cleans p to an absolute path.
func abs(p string) string {
	if a, err := filepath.Abs(p); err == nil {
		return filepath.Clean(a)
	}
	return filepath.Clean(p)
}

// configPaths returns the config files to watch: the tlua.json in effect
// plus tlua.toml/tlua.lock beside it when they exist.
func configPaths(cfg *config.Config) []string {
	var out []string
	if cfg.Path != "" {
		out = append(out, abs(cfg.Path))
	} else if cfg.Dir != "" {
		if _, err := os.Stat(filepath.Join(cfg.Dir, "tlua.json")); err == nil {
			out = append(out, abs(filepath.Join(cfg.Dir, "tlua.json")))
		}
	}
	if cfg.Dir != "" {
		for _, n := range []string{"tlua.toml", "tlua.lock"} {
			if _, err := os.Stat(filepath.Join(cfg.Dir, n)); err == nil {
				out = append(out, abs(filepath.Join(cfg.Dir, n)))
			}
		}
	}
	return out
}

// Collect snapshots the current watch set. Files that cannot be statted
// are omitted so their disappearance reads as Removed (the rebuild will
// surface the underlying TL4001); transient read races resolve on the
// next cycle.
func Collect(cfg *config.Config) (Snapshot, error) {
	list, err := cfg.ListFiles()
	if err != nil {
		return nil, err
	}
	outAbs := OutDirAbs(cfg)
	snap := Snapshot{}
	for _, f := range list {
		a := abs(f)
		if outAbs != "" && underDir(a, outAbs) {
			continue // never watch our own outputs
		}
		st, err := os.Stat(a)
		if err != nil || st.IsDir() {
			continue
		}
		snap[a] = FileMeta{Size: st.Size(), ModTime: st.ModTime().UnixNano()}
	}
	for _, c := range configPaths(cfg) {
		st, err := os.Stat(c)
		if err != nil || st.IsDir() {
			// A config that vanished is itself a change only when it was
			// in the baseline; absence here vs presence there => Removed.
			continue
		}
		if outAbs != "" && underDir(c, outAbs) {
			continue
		}
		snap[c] = FileMeta{Size: st.Size(), ModTime: st.ModTime().UnixNano(), Config: true}
	}
	return snap, nil
}

// Diff compares a baseline against the current snapshot. The result is
// sorted by path for deterministic output.
func Diff(base, cur Snapshot) []Change {
	var out []Change
	for p, bm := range base {
		cm, ok := cur[p]
		if !ok {
			out = append(out, Change{Path: p, Kind: Removed, Config: bm.Config})
			continue
		}
		if cm.Size != bm.Size || cm.ModTime != bm.ModTime || cm.Config != bm.Config {
			out = append(out, Change{Path: p, Kind: Modified, Config: cm.Config || bm.Config})
		}
	}
	for p, cm := range cur {
		if _, ok := base[p]; !ok {
			out = append(out, Change{Path: p, Kind: Added, Config: cm.Config})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

// HasConfigChange reports whether any change touches a config file.
func HasConfigChange(changes []Change) bool {
	for _, c := range changes {
		if c.Config {
			return true
		}
	}
	return false
}

// Watcher polls Collect until the snapshot differs from the baseline.
type Watcher struct {
	// Poll is the polling interval. Values < 50ms are clamped to 50ms.
	Poll time.Duration
	// Debounce coalesces rapid successive saves into one rebuild.
	Debounce time.Duration
	// Collect snapshots the watch set; defaults to Collect(cfg) via closure.
	Collect func() (Snapshot, error)
}

// Next blocks until the watch set differs from base, then waits out the
// debounce window and re-snapshots so one rebuild covers the whole burst.
// It returns the fresh snapshot (the caller's next baseline) and the
// coalesced changes. ctx cancellation aborts the wait. Collect errors are
// returned immediately; the caller should back off before retrying.
func (w *Watcher) Next(ctx context.Context, base Snapshot) (Snapshot, []Change, error) {
	poll := w.Poll
	if poll < 50*time.Millisecond {
		poll = 50 * time.Millisecond
	}
	debounce := w.Debounce
	if debounce < 0 {
		debounce = 0
	}
	tick := time.NewTicker(poll)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return base, nil, ctx.Err()
		case <-tick.C:
			cur, err := w.Collect()
			if err != nil {
				return base, nil, err
			}
			if len(Diff(base, cur)) == 0 {
				continue
			}
			// Burst coalescing: wait out the debounce, then capture the
			// final state. A change observed here always rebuilds once.
			if debounce > 0 {
				timer := time.NewTimer(debounce)
				select {
				case <-ctx.Done():
					timer.Stop()
					return base, nil, ctx.Err()
				case <-timer.C:
				}
				cur2, err := w.Collect()
				if err != nil {
					return base, nil, err
				}
				if changes := Diff(base, cur2); len(changes) > 0 {
					return cur2, changes, nil
				}
				// Flapped back to identical identity; keep watching from
				// the newest state.
				base = cur2
				continue
			}
			return cur, Diff(base, cur), nil
		}
	}
}
