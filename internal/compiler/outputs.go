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
