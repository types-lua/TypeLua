package compiler

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/typelua/typelua/internal/mod"
)

// Native asset passthrough (cross-language contract): *.lua shims and
// native libraries (*.so/*.dylib/*.dll shared, *.a static) travel from
// source/vendor into dist
// preserving layout, so vendored `init.lua` + `ffi.load` relative paths keep
// working without manual copy steps.
//
// Rules (bug-avoidance first):
//   - Only static assets: .lua/.so/.dylib/.dll/.a. Headers/docs never copy.
//   - `.tl` emit wins: an asset whose destination collides with an emitted
//     .lua output is skipped (source of truth is the .tl file).
//   - outDir/.git/node_modules never walked; config exclude patterns apply
//     to project assets (vendor always copies: it is already lock-pinned).
//   - Missing vendor dir is fine (pure-Lua projects copy zero assets).
//   - Stale native assets from deleted sources linger until `clean`
//     (watch-mode stale cleanup covers .tl outputs only); documented.

func isNativeAsset(name string) bool {
	lower := strings.ToLower(name)
	return strings.HasSuffix(lower, ".lua") ||
		strings.HasSuffix(lower, ".so") ||
		strings.HasSuffix(lower, ".dylib") ||
		strings.HasSuffix(lower, ".dll") ||
		strings.HasSuffix(lower, ".a")
}

var _ = isNativeAsset

// emitNativeAssets copies static native assets into outDir. emittedSet holds
// destinations already written by .tl emit (collision guard). Returns copied
// destination paths. Discovery reuses Config.ListNativeAssets so build and
// watch agree on the input set exactly.
func (p *Program) emitNativeAssets(emittedSet map[string]bool) ([]string, error) {
	outDir := p.Cfg.OutDir()
	if !filepath.IsAbs(outDir) {
		outDir = filepath.Join(p.Cfg.Dir, outDir)
	}
	assets, _ := p.Cfg.ListNativeAssets()
	root := filepath.Join(p.Cfg.Dir, p.Cfg.RootDir())
	vendor := filepath.Join(p.Cfg.Dir, mod.VendorDir)
	var copied []string
	for _, src := range assets {
		var dst string
		if rel, err := filepath.Rel(root, src); err == nil && !strings.HasPrefix(rel, "..") {
			dst = filepath.Join(outDir, rel)
		} else if rel, err := filepath.Rel(vendor, src); err == nil && !strings.HasPrefix(rel, "..") {
			dst = filepath.Join(outDir, mod.VendorDir, rel)
		} else {
			continue
		}
		if emittedSet[filepath.Clean(dst)] {
			continue // .tl emit wins
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return copied, err
		}
		raw, err := os.ReadFile(src)
		if err != nil {
			continue // transient race (deleted mid-build); next cycle heals
		}
		// Content compare keeps mtimes stable for unchanged assets.
		if _, err := writeFileIfChanged(dst, raw); err != nil {
			return copied, err
		}
		emittedSet[filepath.Clean(dst)] = true
		copied = append(copied, dst)
	}
	return copied, nil
}
