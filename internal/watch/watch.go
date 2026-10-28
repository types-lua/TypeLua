// Package watch 实现 tsc 风格的 `--watch` 轮询重建。
//
// 设计说明（正确性优先）：
//   - 监视集由 config.Config.ListFiles 加配置文件本身（tlua.json/tlua.toml/tlua.lock）
//     导出，因此仅在构建输入集变化时触发重建——不多不少。
//   - 解析后的 outDir 下的任何内容都不监视，因此发射永远不能
//     重触发重建（自触发循环）。
//   - 轮询（无外部依赖，全平台/文件系统可用）+ 防抖合并：
//     快速保存折叠为一次重建，每次重建后刷新基线，
//     构建中途的编辑不丢失。
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

// ChangeKind 描述被监视文件与基线的差异方式。
type ChangeKind string

const (
	// Added 表示监视集中新增的文件。
	Added ChangeKind = "added"
	// Removed 表示监视集中消失的文件。
	Removed ChangeKind = "removed"
	// Modified 表示元信息变化的文件。
	Modified ChangeKind = "modified"
)

// Change 是单个被监视文件的差异，Diff 按 Path 排序。
//
// 物理布局：
//
//	Change
//	├── Path   string     // 绝对/清理后的路径
//	├── Kind   ChangeKind  // added/removed/modified
//	└── Config bool       // 是否为配置文件（tlua.json/toml/lock）
type Change struct {
	Path string
	Kind ChangeKind
	// Config 为真时表示路径是配置文件（tlua.json/toml/lock）
	// 而非程序源。
	Config bool
}

// FileMeta 是被监视文件的追踪身份（大小 + 修改时间）。
//
// 物理布局：
//
//	FileMeta
//	├── Size    int64 // 字节数
//	├── ModTime int64 // UnixNano
//	└── Config  bool  // 是否配置文件
type FileMeta struct {
	Size    int64
	ModTime int64 // UnixNano
	Config  bool
}

// Snapshot 是监视集的快照：清理后的绝对路径 -> FileMeta。
//
// 物理布局（概念）：
//
//	Snapshot (map)
//	└── path -> FileMeta{Size, ModTime, Config}
type Snapshot map[string]FileMeta

// OutDirAbs 把有效输出目录解析为绝对路径。
func OutDirAbs(cfg *config.Config) string {
	out := cfg.OutDir()
	if filepath.IsAbs(out) {
		return filepath.Clean(out)
	}
	return filepath.Clean(filepath.Join(cfg.Dir, out))
}

// underDir 报告 p 是否在 dir 内（边界感知）。
func underDir(p, dir string) bool {
	if p == dir {
		return true
	}
	return strings.HasPrefix(p, dir+string(os.PathSeparator))
}

// abs 把 p 清理为绝对路径。
func abs(p string) string {
	if a, err := filepath.Abs(p); err == nil {
		return filepath.Clean(a)
	}
	return filepath.Clean(p)
}

// configPaths 返回要监视的配置文件：生效的 tlua.json
// 以及同目录存在的 tlua.toml/tlua.lock。
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

// Collect 快照当前监视集。无法 stat 的文件被省略，
// 其消失读作 Removed（重建会暴露底层 TL4001）；
// 瞬态读取竞争在下个周期解决。原生资产（*.lua shim、*.so/*.dylib/*.dll/*.a）
// 加入 .tl 源，使 shim/二进制编辑重触发直通复制。
func Collect(cfg *config.Config) (Snapshot, error) {
	list, err := cfg.ListFiles()
	if err != nil {
		return nil, err
	}
	if assets, err := cfg.ListNativeAssets(); err == nil {
		seen := map[string]bool{}
		for _, f := range list {
			seen[filepath.Clean(f)] = true
		}
		for _, a := range assets {
			if !seen[filepath.Clean(a)] {
				list = append(list, a)
			}
		}
	}
	outAbs := OutDirAbs(cfg)
	snap := Snapshot{}
	for _, f := range list {
		a := abs(f)
		if outAbs != "" && underDir(a, outAbs) {
			continue // 永不监视我们自己的输出
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
			// 消失的配置本身只有在基线中存在时才是变化；
			// 此处缺失 vs 彼处存在 => Removed。
			continue
		}
		if outAbs != "" && underDir(c, outAbs) {
			continue
		}
		snap[c] = FileMeta{Size: st.Size(), ModTime: st.ModTime().UnixNano(), Config: true}
	}
	return snap, nil
}

// Diff 比较基线与当前快照。结果按路径排序以保证确定性。
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

// HasConfigChange 报告是否有变化触及配置文件。
func HasConfigChange(changes []Change) bool {
	for _, c := range changes {
		if c.Config {
			return true
		}
	}
	return false
}

// Watcher 轮询 Collect，直到快照与基线不同。
//
// 物理布局：
//
//	Watcher
//	├── Poll    time.Duration  // 轮询间隔（<50ms 钳到 50ms）
//	├── Debounce time.Duration  // 防抖窗口
//	└── Collect func() (Snapshot, error) // 快照采集（默认 Collect(cfg)）
type Watcher struct {
	// Poll 是轮询间隔。小于 50ms 的值钳到 50ms。
	Poll time.Duration
	// Debounce 把快速连续保存合并为一次重建。
	Debounce time.Duration
	// Collect 快照监视集；默认通过闭包使用 Collect(cfg)。
	Collect func() (Snapshot, error)
}

// Next 阻塞直到监视集与 base 不同，然后等待防抖窗口并重新快照，
// 使一次重建覆盖整个突发。返回新鲜快照（调用方的下一个基线）
// 与合并后的变化。ctx 取消中止等待。Collect 错误立即返回；
// 调用方应在重试前退避。
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
			// 突发合并：等待防抖窗口，然后捕获最终状态。
			// 此处观察到的变化总是重建一次。
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
				// 已回到相同身份；从最新状态继续监视。
				base = cur2
				continue
			}
			return cur, Diff(base, cur), nil
		}
	}
}
