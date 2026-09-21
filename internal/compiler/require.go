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
//   1. relative to the requiring file's directory ("math_utils", "sub.mod"),
//   2. under <config-dir>/tlua_modules (vendored dependencies).
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

// resolveRequire maps a dotted require path to a program source file.
func (p *Program) resolveRequire(from *SourceFile, dotted string) *SourceFile {
	segs := strings.Split(dotted, ".")
	for _, s := range segs {
		if s == "" {
			return nil
		}
	}
	rel := filepath.FromSlash(strings.Join(segs, "/")) + ".tl"
	if sf := p.fileByPath(filepath.Join(filepath.Dir(from.Path), rel)); sf != nil {
		return sf
	}
	if sf := p.fileByPath(filepath.Join(p.Cfg.Dir, mod.VendorDir, rel)); sf != nil {
		return sf
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
