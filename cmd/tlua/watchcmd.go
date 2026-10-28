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

// cliOverrides 是每次重载都覆盖 tlua.json 的标志值。
//
// 设计者视角：CLI 恒胜配置——paths/outDir/target/map/shake 各配 has 旗，
// 重载亦覆。障碍是零值歧义：has 区分“未传”与“传零”。排查现场：覆盖失效先查 has。
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
	// 设计者视角：按 has 旗覆写，paths 非空即覆。障碍是零值覆盖：须 has 门。
	// 排查现场：重载后旗丢先查 has。
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

// countSessionErrors 统计会话中的错误级别诊断。
//
// 设计者视角：扫 Diags 数 Error（Warn 不计），供 watch 报数。障碍是级别混：
// 只认 Error。排查现场：数不对先查级别。
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
	// 设计者视角：`[15:04:05] msg` 时戳日志，watch 人读。障碍是并发：单 goroutine 调。
	// 排查现场：日志乱先查并发。
	fmt.Printf("[%s] %s\n", time.Now().Format("15:04:05"), fmt.Sprintf(format, args...))
}

// safeCycle 运行 fn 并把 panic 转为返回值，使单个坏周期
// （parser/checker/emitter bug、半写文件）永远不能杀死
// 长运行的 watch 会话。
//
// 设计者视角：recover 转值，watch 记 warn 续跑（见 runWatch）。障碍是 panic 丢栈：
// 错文+debug.Stack 同记。候选：崩 vs 续；选续。不变量：panic 永不穿。排查现场：
// 静默失败先查返回值。
func safeCycle(fn func()) (panicked any) {
	defer func() {
		if r := recover(); r != nil {
			panicked = r
		}
	}()
	fn()
	return nil
}

// pruneStaleOutputs 删除自上周期以来源消失的输出。prev 把绝对源路径
// 映射到我们为其发射的 dst；curSrc 持有当前周期的绝对源集。
// 只有以 .lua 结尾的路径（及其 .map 伴生）才会被移除，
// 删除失败报告但不致命。
//
// 设计者视角：源消即删产物（.lua+.map），非 lua 不碰（资产另管），失败 warn 续。
// 障碍是误删资产：Ext 判。候选：删 vs 留；选删保 dist 洁。不变量：只删 .lua 系。
// 排查现场：过期残留先查 abs 比对。
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
	// 设计者视角：Abs+Clean 归一供 stale 比对，败退 Clean。障碍是相对混：归一。
	// 排查现场：误删/漏删先查此。
	if a, err := filepath.Abs(p); err == nil {
		return filepath.Clean(a)
	}
	return filepath.Clean(p)
}

// reloadCfg 重新读取 tlua.json，使 include/exclude/target/outDir 编辑
// 无需重启 watch 即可生效。CLI 覆盖始终优先。任何失败都保留
// 前一个（最后好的）配置并继续会话——半保存的 JSON
// 绝不能杀死 watch。
//
// 设计者视角：径空重找 FindUp，缺/坏皆保旧+warn，成则覆 CLI。障碍是半写 JSON：
// 保旧续跑。候选：崩 vs 保旧；选保旧。不变量：失败返 prev。排查现场：配置不生效
// 先查 warn。
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
	// 设计者视角：--watch/-w 双旗+poll/debounce，供 check/build 复用。障碍是命名：
	// 双旗同义。排查现场：watch 未进先查 enabled。
	fs.BoolVar(&wf.watch, "watch", false, "watch input files and recompile on change (like tsc --watch)")
	fs.BoolVar(&wf.watchAlias, "w", false, "alias for --watch")
	fs.IntVar(&wf.pollMs, "pollInterval", 500, "watch polling interval in milliseconds")
	fs.IntVar(&wf.debounceMs, "debounce", 200, "watch debounce window in milliseconds")
}

func resolveWatchFlags(wf *watchFlags) {
	// 设计者视角：enabled=watch||w，poll 钳 50ms~60s，debounce 钳 0~5s，越界 note+钳。
	// 障碍是过小 poll 忙轮询：钳之。候选：错拒 vs 钳；选钳+note。排查现场：CPU 高先查
	// poll。
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

// runWatch 是 tsc --watch 等价：构建/检查一次，然后在每个输入或
// 配置变化时重建，直到被中断。诊断失败从不停止会话；
// 只有 SIGINT/SIGTERM 结束它（退出码 0）。
//
// 稳态周期是增量的：compiler.Session 复用已解析 AST 与缓存的检查结果，
// 只重新处理 watch 报告的变化（及其 require 闭包）。
// 配置变化重置会话以完全重建。
//
// 设计者视角：首 cycle 全量建基+基线快照，后 Next 长轮询（防抖+信号），
// 变即 describe+cycle（配置变先 Reset），错仅 warn 续。障碍是半写/坏周期：
// safeCycle 兜；障碍是 Collect 错：保旧基线。候选：增量 vs 全量；选增量+配置重置。
// 不变量：中断方退 0。排查现场：不重建先查基线，配置不生效先查 Reset。
func runWatch(mode watchMode, cfg *config.Config, cfgPath string, ov cliOverrides, wf watchFlags) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	watchLog("Starting compilation in watch mode...")
	emittedBySrc := map[string]string{}
	var baseline watch.Snapshot
	sess := compiler.NewSession(cfg)

	cycle := func(configChanged bool, changes []watch.Change) {
		if configChanged {
			// reloadCfg 在任何失败时返回 prev，因此指针变化恰好意味着
			// "重新加载成功"：重置以完全重建，否则保持增量状态。
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
		// 构建后基线：周期中途的编辑在下次拾取。
		if snap, err := watch.Collect(cfg); err != nil {
			watchLog("warning: cannot list files (%v); keeping previous watch set", err)
		} else {
			baseline = snap
		}
	}

	// 初始编译（会话为空：完全同步）。
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

// runWatchCycle 执行一次带 watch 风格报告的检查/构建。
// emittedBySrc 原地更新以追踪过时输出。检查与构建
// 通过会话（第一周期后增量）。
//
// 设计者视角：check 轨 Sync(不发射)+印诊+计数，build 轨 Sync(发射)+印诊+刷映射+
// 剪过期（prune）。障碍是空工程错：`FileCount==0` 方显错。候选：全量 vs 增量；
// 选会话增量。不变量：emittedBySrc 与 curSrc 同步。排查现场：过期残留先查剪，
// 计数错先查 count。
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
	// 刷新 src->dst 映射并删除源消失的输出。
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
	// 设计者视角：变 log 限 5 条详、余计数，空变亦报（防抖回同）。障碍是刷屏：限 5。
	// 排查现场：无 log 先查此。
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
	// 设计者视角：watch 独立命令=loadCfg+ov 组装+runWatch 构建轨。障碍是旗透传：
	// ov has 全置。排查现场：覆盖失效先查 ov。
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
