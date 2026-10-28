package compiler

import (
	"path/filepath"
	"strings"

	"github.com/typelua/typelua/internal/config"
)

// OutputForFile maps a source path to its emitted .lua path.
//
// It is the single source of truth shared by Program.Emit and watch-mode
// stale-output cleanup, so both must agree on every input (relative or
// absolute source paths, custom rootDir/outDir). ok=false for `.d.tl`
// inputs, which are checked but never emitted.
//
// 设计者视角：OutputForFile 是输出布局的“唯一门牌办”——Emit 与 watch 清理同问
// 此处，杜绝两套算法分叉。障碍是相对/绝对混杂：outDir 非绝对即相对 Dir 拼，
// Rel 失败退 Base；非 .tl 显式 files 条目保留名加 .lua。候选：各处自算 vs 集中；
// 选集中。不变量：.d.tl 恒 false。排查现场：输出错位先查 RootDir/OutDir，
// 清理误删先查此处与 Emit 是否同源。
func OutputForFile(cfg *config.Config, src string) (dst string, ok bool) {
	if strings.HasSuffix(src, ".d.tl") {
		return "", false
	}
	outDir := cfg.OutDir()
	if !filepath.IsAbs(outDir) {
		outDir = filepath.Join(cfg.Dir, outDir)
	}
	rel, err := filepath.Rel(filepath.Join(cfg.Dir, cfg.RootDir()), src)
	if err != nil {
		rel = filepath.Base(src)
	}
	// Mirrors Emit: only `.tl` is trimmed; anything else keeps its name
	// and gains `.lua` (explicit `files:` entries may be non-.tl).
	rel = strings.TrimSuffix(rel, ".tl") + ".lua"
	return filepath.Join(outDir, rel), true
}
