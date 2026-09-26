package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"runtime/debug"
	"syscall"
	"time"

	"github.com/typelua/typelua/internal/compiler"
	"github.com/typelua/typelua/internal/config"
	"github.com/typelua/typelua/internal/diag"
	"github.com/typelua/typelua/internal/watch"
)

type watchMode int

const (
	modeCheck watchMode = iota
	modeBuild
)

// cliOverrides are flag values that win over tlua.json on every reload.
type cliOverrides struct {
	paths        []string
	outDir       string
	target       string
	sourceMap    bool
	treeShake    bool
	hasOutDir    bool
	hasTarget    bool
	hasSrcMap    bool
	hasTreeShake bool
}

func applyOverrides(cfg *config.Config, ov cliOverrides) {
	if len(ov.paths) > 0 {
		cfg.Files = ov.paths
	}
	if ov.hasOutDir {
		cfg.CompilerOptions.OutDir = ov.outDir
	}
	if ov.hasTarget {
		cfg.CompilerOptions.Target = ov.target
	}
	if ov.hasSrcMap {
		cfg.CompilerOptions.SourceMap = ov.sourceMap
	}
	if ov.hasTreeShake {
		cfg.CompilerOptions.TreeShake = ov.treeShake
	}
}

// countSessionErrors counts error-severity diagnostics in a session.
func countSessionErrors(sess *compiler.Session) int {
	n := 0
	for _, d := range sess.Diags() {
		if d.Severity == diag.Error {
			n++
		}
	}
	return n
}

func watchLog(format string, args ...any) {
	fmt.Printf("[%s] %s\n", time.Now().Format("15:04:05"), fmt.Sprintf(format, args...))
}

// safeCycle runs fn and converts a panic into a returned value so a single
// bad cycle (parser/checker/emitter bug, half-written file) can never kill
// a long-running watch session.
func safeCycle(fn func()) (panicked any) {
	defer func() {
		if r := recover(); r != nil {
			panicked = r
		}
	}()
	fn()
	return nil
}

// pruneStaleOutputs deletes outputs whose source vanished since the last
// cycle. prev maps absolute source paths to the dst we emitted for them;
// curSrc holds the current cycle's absolute source set. Only paths ending
// in .lua (plus their .map sidecar) are ever removed, and unlink failures
// are reported, never fatal.
func pruneStaleOutputs(prev map[string]string, curSrc map[string]bool) []string {
	var deleted []string
	for src, dst := range prev {
		if curSrc[absPath(src)] {
			continue
		}
		if filepath.Ext(dst) != ".lua" {
			continue
		}
		if err := os.Remove(dst); err != nil && !os.IsNotExist(err) {
			watchLog("warning: cannot remove stale output %s: %v", dst, err)
			continue
		} else if err == nil {
			deleted = append(deleted, dst)
		}
		if err := os.Remove(dst + ".map"); err != nil && !os.IsNotExist(err) {
			watchLog("warning: cannot remove stale source map %s: %v", dst+".map", err)
		} else if err == nil {
			deleted = append(deleted, dst+".map")
		}
	}
	return deleted
}

func absPath(p string) string {
	if a, err := filepath.Abs(p); err == nil {
		return filepath.Clean(a)
	}
	return filepath.Clean(p)
}

// reloadCfg re-reads tlua.json so include/exclude/target/outDir edits apply
// without restarting watch. CLI overrides always win. On any failure the
// previous (last good) config is kept and the session continues — a
// half-saved JSON must never kill watch.
func reloadCfg(prev *config.Config, cfgPath string, ov cliOverrides) (*config.Config, string) {
	path := cfgPath
	if path == "" {
		cwd, _ := os.Getwd()
		path = config.FindUp(cwd)
		if path == "" {
			return prev, cfgPath
		}
	}
	if _, err := os.Stat(path); err != nil {
		watchLog("warning: config %s unreadable (%v); keeping previous configuration", path, err)
		return prev, cfgPath
	}
	cfg, err := config.Load(path)
	if err != nil {
		watchLog("warning: bad %s (%v); keeping previous configuration", path, err)
		return prev, cfgPath
	}
	applyOverrides(cfg, ov)
	return cfg, path
}

type watchFlags struct {
	watch      bool
	watchAlias bool
	pollMs     int
	debounceMs int
	poll       time.Duration
	debounce   time.Duration
	enabled    bool
}

func bindWatchFlags(fs *flag.FlagSet, wf *watchFlags) {
	fs.BoolVar(&wf.watch, "watch", false, "watch input files and recompile on change (like tsc --watch)")
	fs.BoolVar(&wf.watchAlias, "w", false, "alias for --watch")
	fs.IntVar(&wf.pollMs, "pollInterval", 500, "watch polling interval in milliseconds")
	fs.IntVar(&wf.debounceMs, "debounce", 200, "watch debounce window in milliseconds")
}

func resolveWatchFlags(wf *watchFlags) {
	wf.enabled = wf.watch || wf.watchAlias
	poll := wf.pollMs
	if poll < 50 {
		wf.pollMs = 50
		fmt.Printf("note: --pollInterval %dms too small, using 50ms\n", poll)
		poll = 50
	} else if poll > 60000 {
		wf.pollMs = 60000
		fmt.Printf("note: --pollInterval %dms too large, using 60000ms\n", poll)
		poll = 60000
	}
	deb := wf.debounceMs
	if deb < 0 {
		wf.debounceMs = 0
		fmt.Printf("note: --debounce %dms negative, using 0ms\n", deb)
		deb = 0
	} else if deb > 5000 {
		wf.debounceMs = 5000
		fmt.Printf("note: --debounce %dms too large, using 5000ms\n", deb)
		deb = 5000
	}
	wf.poll = time.Duration(poll) * time.Millisecond
	wf.debounce = time.Duration(deb) * time.Millisecond
}

// runWatch is the tsc --watch equivalent: build/check once, then rebuild
// on every input or config change until interrupted. Diagnostic failures
// never stop the session; only SIGINT/SIGTERM ends it (exit 0).
//
// Steady-state cycles are incremental: a compiler.Session reuses parsed
// ASTs and cached check results, reprocessing only watcher-reported
// changes (plus their require closure). Config changes reset the session
// for a full rebuild.
func runWatch(mode watchMode, cfg *config.Config, cfgPath string, ov cliOverrides, wf watchFlags) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	watchLog("Starting compilation in watch mode...")
	emittedBySrc := map[string]string{}
	var baseline watch.Snapshot
	sess := compiler.NewSession(cfg)

	cycle := func(configChanged bool, changes []watch.Change) {
		if configChanged {
			// reloadCfg returns prev on any failure, so a changed pointer
			// exactly means "reloaded successfully": reset for a full
			// rebuild, otherwise keep the incremental state.
			if nextCfg, nextPath := reloadCfg(cfg, cfgPath, ov); nextCfg != cfg {
				cfg, cfgPath = nextCfg, nextPath
				sess.Reset(cfg)
			}
		}
		if r := safeCycle(func() {
			runWatchCycle(mode, cfg, sess, changes, emittedBySrc)
		}); r != nil {
			stack := debug.Stack()
			if len(stack) > 8192 {
				stack = stack[:8192]
			}
			watchLog("internal error during compilation (watch continues): %v\n%s", r, stack)
		}
		// Post-build baseline: edits made mid-cycle are picked up next.
		if snap, err := watch.Collect(cfg); err != nil {
			watchLog("warning: cannot list files (%v); keeping previous watch set", err)
		} else {
			baseline = snap
		}
	}

	// Initial compilation (session is empty: full sync).
	cycle(false, nil)
	if baseline == nil {
		if snap, err := watch.Collect(cfg); err == nil {
			baseline = snap
		} else {
			baseline = watch.Snapshot{}
		}
	}

	w := &watch.Watcher{
		Poll:     wf.poll,
		Debounce: wf.debounce,
	}
	for {
		w.Collect = func() (watch.Snapshot, error) { return watch.Collect(cfg) }
		next, changes, err := w.Next(ctx, baseline)
		if err != nil {
			if ctx.Err() != nil {
				watchLog("Watch terminated.")
				return nil
			}
			watchLog("warning: watch failed (%v); retrying", err)
			select {
			case <-ctx.Done():
				watchLog("Watch terminated.")
				return nil
			case <-time.After(wf.poll):
			}
			continue
		}
		baseline = next
		describeChanges(changes)
		cycle(watch.HasConfigChange(changes), changes)
	}
}

// runWatchCycle performs one check/build pass with watch-style reporting.
// emittedBySrc is updated in place for stale-output tracking. Checking and
// building go through the session (incremental after the first cycle).
func runWatchCycle(mode watchMode, cfg *config.Config, sess *compiler.Session, changes []watch.Change, emittedBySrc map[string]string) {
	if mode == modeCheck {
		if _, err := sess.Sync(false, changes, false); err != nil {
			watchLog("warning: incremental check failed (%v); keeping previous results", err)
		}
		if out := sess.FormatDiags(); out != "" {
			fmt.Print(out)
		}
		n := countSessionErrors(sess)
		if n == 0 {
			fmt.Printf("checked %d file(s): no errors\n", sess.FileCount())
		} else {
			fmt.Printf("found %d error(s) in %d file(s)\n", n, sess.FileCount())
		}
		watchLog("Found %d error(s). Watching for file changes.", n)
		return
	}
	emitted, err := sess.Sync(false, changes, true)
	if err != nil && sess.FileCount() == 0 {
		fmt.Printf("error: %v\n", err)
		watchLog("Found 0 error(s). Watching for file changes.")
		return
	}
	if out := sess.FormatDiags(); out != "" {
		fmt.Print(out)
	}
	n := countSessionErrors(sess)
	if err != nil {
		fmt.Printf("emit failed: %v\n", err)
	} else {
		fmt.Printf("built %d file(s)\n", len(emitted))
	}
	// Refresh src->dst mapping and drop outputs whose source is gone.
	curSrc := map[string]bool{}
	for _, sf := range sess.Files() {
		if sf.IsDecl {
			continue
		}
		curSrc[absPath(sf.Path)] = true
	}
	for _, sf := range sess.Files() {
		if sf.IsDecl {
			continue
		}
		if dst, ok := compiler.OutputForFile(cfg, sf.Path); ok {
			emittedBySrc[absPath(sf.Path)] = dst
		}
	}
	for _, d := range pruneStaleOutputs(emittedBySrc, curSrc) {
		fmt.Printf("removed stale %s\n", d)
	}
	for src := range emittedBySrc {
		if !curSrc[src] {
			delete(emittedBySrc, src)
		}
	}
	_ = emitted
	watchLog("Found %d error(s). Watching for file changes.", n)
}

func describeChanges(changes []watch.Change) {
	if len(changes) == 0 {
		watchLog("File change detected. Starting incremental compilation...")
		return
	}
	const show = 5
	if len(changes) <= show {
		for _, c := range changes {
			watchLog("File change detected (%s %s). Starting incremental compilation...", c.Kind, c.Path)
		}
		return
	}
	watchLog("File change detected (%d files changed). Starting incremental compilation...", len(changes))
	for _, c := range changes[:show] {
		watchLog("  %s %s", c.Kind, c.Path)
	}
}

func cmdWatch(args []string) error {
	fs := flag.NewFlagSet("watch", flag.ContinueOnError)
	outDir := fs.String("outDir", "", "output directory")
	target := fs.String("target", "", "lua target: 5.4 or 5.1")
	sourceMap := fs.Bool("sourceMap", false, "emit .lua.map source maps")
	treeShake := fs.Bool("treeShake", false, "drop zero-reference pure local functions")
	var wf watchFlags
	bindWatchFlags(fs, &wf)
	if err := fs.Parse(args); err != nil {
		return err
	}
	resolveWatchFlags(&wf)
	cfg, cfgPath, err := loadCfgFor(fs.Args(), *outDir, *target, *sourceMap, *treeShake)
	if err != nil {
		return err
	}
	ov := cliOverrides{
		paths:        fs.Args(),
		outDir:       *outDir,
		target:       *target,
		sourceMap:    *sourceMap,
		treeShake:    *treeShake,
		hasOutDir:    *outDir != "",
		hasTarget:    *target != "",
		hasSrcMap:    *sourceMap,
		hasTreeShake: *treeShake,
	}
	return runWatch(modeBuild, cfg, cfgPath, ov, wf)
}
