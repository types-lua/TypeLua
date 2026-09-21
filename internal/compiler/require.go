package compiler

import (
	"path/filepath"
	"strings"

	"github.com/typelua/typelua/internal/checker"
	"github.com/typelua/typelua/internal/mod"
	"github.com/typelua/typelua/internal/types"
)

// Cross-file require resolution for Program.Check: dotted require paths
// map to program files, which check in dependency order with memoization:
//
//	import G from "example.com.greet"  (package root: greet/init.tl)
//	import G from "example.com.greet.greet"  (plain file)
//	import G from "greet"  (manifest short name, unambiguous last segment)
//
// Lookup order per base (requiring file's directory, then tlua_modules):
// `<path>.tl` first, then `<path>/init.tl` (Lua init.lua convention).
// Unresolved targets (missing files, .d.tl stubs, cycles in progress)
// stay `any`, preserving classic single-file behavior as fallback.

// fileByPath finds a program file by cleaned path (exact match only,
// so shadowing stays predictable).
func (p *Program) fileByPath(path string) *SourceFile {
	clean := filepath.Clean(path)
	for _, sf := range p.Files {
		if filepath.Clean(sf.Path) == clean {
			return sf
		}
	}
	return nil
}

// shortModules maps unambiguous manifest short names (last path segment)
// to full module paths. Ambiguous shorts are dropped (full paths still
// work); a missing/broken manifest means no short names.
func (p *Program) shortModules() map[string]string {
	proj, err := mod.LoadProject(p.Cfg.Dir)
	if err != nil {
		return nil
	}
	counts := map[string]int{}
	for path := range proj.Manifest.Deps {
		counts[lastSegment(path)]++
	}
	out := map[string]string{}
	for path := range proj.Manifest.Deps {
		if short := lastSegment(path); counts[short] == 1 {
			out[short] = path
		}
	}
	return out
}

func lastSegment(path string) string {
	if i := strings.LastIndex(path, "/"); i >= 0 {
		return path[i+1:]
	}
	return path
}

// resolveRequire maps a dotted require path to a program source file.
func (p *Program) resolveRequire(from *SourceFile, dotted string) *SourceFile {
	if full, ok := p.shorts[dotted]; ok {
		dotted = full
	}
	segs := strings.Split(dotted, ".")
	for _, s := range segs {
		if s == "" {
			return nil
		}
	}
	rel := filepath.FromSlash(strings.Join(segs, "/"))
	bases := []string{
		filepath.Join(filepath.Dir(from.Path), rel),
		filepath.Join(p.Cfg.Dir, mod.VendorDir, rel),
	}
	for _, base := range bases {
		if sf := p.fileByPath(base + ".tl"); sf != nil {
			return sf
		}
		if sf := p.fileByPath(filepath.Join(base, "init.tl")); sf != nil {
			return sf
		}
	}
	return nil
}

// checkAll checks every file in dependency order, feeding each file the
// module types of its requirements. Files check exactly once; cycles and
// missing targets degrade to `any` for the affected edge only.
func (p *Program) checkAll(opt checker.Options) {
	const (
		unvisited = 0
		busy      = 1
		done      = 2
	)
	state := map[*SourceFile]int{}
	modTy := map[*SourceFile]types.Type{}
	var checkOne func(sf *SourceFile)
	checkOne = func(sf *SourceFile) {
		if state[sf] == done {
			return
		}
		if state[sf] == busy {
			return // cyclic require: preset Any stands for this edge
		}
		state[sf] = busy
		modTy[sf] = types.Any
		env := map[string]types.Type{}
		for _, dotted := range checker.RequiredModules(sf.File) {
			tgt := p.resolveRequire(sf, dotted)
			if tgt == nil || tgt.IsDecl {
				continue
			}
			checkOne(tgt)
			env[dotted] = modTy[tgt]
		}
		fileOpt := opt
		fileOpt.RequireTypes = env
		ds, _, mt := checker.CheckFileModule(sf.File, fileOpt)
		p.Diags = append(p.Diags, ds...)
		modTy[sf] = mt
		state[sf] = done
	}
	for _, sf := range p.Files {
		checkOne(sf)
	}
}
