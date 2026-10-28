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
//
// 设计者视角：三态是轮询的“最小语义”——added/removed 管成员，modified 管内容
// （size+mtime 双判）。障碍是 mtime 粒度粗：同秒改内容须 size 兜底。候选：hash vs
// 元信息；选元信息，快且够（增量另有字节比对兜底）。不变量：三态互斥。排查现场：
// 改而未触发先查 mtime 粒度。
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
// 设计者视角：Change 一行一变——Path 绝对清理，Kind 三态，Config 标配置文件
// （触之须 Reset+full）。障碍是源与配置混流：Config 旗分流处理。候选：分两表 vs
// 一表+旗；选后者，Diff 单序。不变量：Diff 出口按 Path 排。排查现场：配置改未全量
// 先查 Config 旗。
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
// 设计者视角：FileMeta 以 (Size, ModTimeNano, Config) 作身份——双元判变，Config
// 参判防源与配置同径误判。障碍是 touch（mtime 变内容不变）：判变但增量字节比对
// 会消之（no-op）。候选：纳 hash vs 双元；选双元，stat 零读。不变量：UnixNano 精度。
// 排查现场：频繁误触发先查 mtime。
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
// 设计者视角：Snapshot 是基线“照片”——绝对清理键保 Diff 可比。障碍是相对/绝对混：
// Collect 统 abs 之。不变量：键恒绝对。排查现场：diff 恒变先查键系。
//
// 物理布局（概念）：
//
//	Snapshot (map)
//	└── path -> FileMeta{Size, ModTime, Config}
type Snapshot map[string]FileMeta

// OutDirAbs 把有效输出目录解析为绝对路径。
//
// 设计者视角：OutDirAbs 给“勿监视输出”划红线——相对即拼 Dir，绝对即 Clean。
// 障碍是输出落源树内：不划线即自触发循环。候选：监视全集 vs 排除输出；选排除。
// 不变量：返绝对清理径。排查现场：watch 自激先查此排除。
func OutDirAbs(cfg *config.Config) string {
	out := cfg.OutDir()
	if filepath.IsAbs(out) {
		return filepath.Clean(out)
	}
	return filepath.Clean(filepath.Join(cfg.Dir, out))
}

// underDir 报告 p 是否在 dir 内（边界感知）。
//
// 设计者视角：边界感知防 `/out` 误含 `/out2`——相等或 `dir+sep` 前缀方算内。
// 障碍是字符串前缀陷阱。候选：Rel+.. 判 vs 前缀；选前缀+分隔符，快且准。不变量：
// 纯判。排查现场：输出未排除先查分隔符。
func underDir(p, dir string) bool {
	if p == dir {
		return true
	}
	return strings.HasPrefix(p, dir+string(os.PathSeparator))
}

// abs 把 p 清理为绝对路径。
//
// 设计者视角：abs 统一键系——Abs 成即 Clean，败则 Clean 原径保不抛。障碍是 Abs
// err（坏径）：退化不断。排查现场：键 mismatch 先查此。
func abs(p string) string {
	if a, err := filepath.Abs(p); err == nil {
		return filepath.Clean(a)
	}
	return filepath.Clean(p)
}

// configPaths 返回要监视的配置文件：生效的 tlua.json
// 以及同目录存在的 tlua.toml/tlua.lock。
//
// 设计者视角：配置三件套全监——生效 json（Path 或 Dir 下）+ toml/lock（存在方入）。
// 障碍是 Path 为空：退 Dir 探测。候选：只监生效 vs 全监；选全监，任一变即 Reset。
// 不变量：返绝对径。排查现场：改 toml 未重建先查此。
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
//
// 设计者视角：Collect 快照“输入全集”——ListFiles+ListNativeAssets 去重，跳输出
// 目录，stat 失败即省（下轮 Removed 语义），配置另标 Config。障碍是自触发：输出
// 必跳；障碍是资产漏监：shim 改须重拷，故并入。候选：stat 错抛 vs 省；选省，
// 瞬态赛下轮愈。不变量：输出目录零条目。排查现场：输出改触发重建先查 underDir，
// 资产改不触发先查去重。
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
//
// 设计者视角：Diff 双向比——base 有 cur 无即 Removed，cur 有 base 无即 Added，
// 双有而元异即 Modified（含 Config 翻）。障碍是 Config 旗：任一端真即真。候选：
// 无序 vs 排序；选排序，日志稳定。不变量：空即无变。排查现场：抖动先查元判三元。
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
//
// 设计者视角：任一 Change.Config 真即须 Reset+full，保配置语义全局生效。障碍是
// 源改误判：只看旗。排查现场：配置改仍增量先查此。
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
// 设计者视角：Watcher 是“心跳+防抖”——Poll 钳 50ms 下限防忙轮询，Debounce 合并
// 连存，Collect 可注入假源供测。障碍是平台差异：轮询保全平台可用。候选：fsnotify
// vs 轮询；选轮询零依赖。不变量：Poll<50ms 即 50ms。排查现场：CPU 高先查 Poll。
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
//
// 设计者视角：Next 两段等——等变（ticker 轮询，ctx 可断，Collect 错即返）+
// 等稳（debounce 窗后重采，回到同身份则续监防空重建）。障碍是突发连存：首变后
// 即重建会频闪；故防抖重采取终态。候选：首变即返 vs 防抖；选防抖。不变量：返快照
// 即下轮 base。排查现场：空重建先查回同分支，重建频闪先查 Debounce。
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
