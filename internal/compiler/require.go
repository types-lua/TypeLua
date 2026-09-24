package compiler

import (
	"path/filepath"
	"sort"
	"strings"

	"github.com/typelua/typelua/internal/ast"
	"github.com/typelua/typelua/internal/checker"
	"github.com/typelua/typelua/internal/config"
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

// candidateRels expands a dotted require path into candidate relatives.
// k=1 is all slashes (historical behavior, tried first); larger k preserves
// leading dots for domain-style module paths ("example.com.greet" also
// tries "example.com/greet", matching the manifest/vendor layout where the
// first segment keeps its dots).
func candidateRels(dotted string) []string {
	segs := strings.Split(dotted, ".")
	var out []string
	for k := 1; k <= len(segs); k++ {
		head := strings.Join(segs[:k], ".")
		if k == len(segs) {
			out = append(out, filepath.FromSlash(head))
		} else {
			out = append(out, filepath.FromSlash(head+"/"+strings.Join(segs[k:], "/")))
		}
	}
	return out
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
	for _, rel := range candidateRels(dotted) {
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
	}
	return nil
}

// checkAll checks every file in dependency order, feeding each file the
// module types of its requirements. Files check exactly once; cycles and
// missing targets degrade to `any` for the affected edge only.
//
// Ambient .d.tl files (filtered by `types` allowlist) are checked once up
// front into a shared parent scope (builtins + globals, like tsconfig
// types); program files then check as children so shadowing stays detached.
func (p *Program) checkAll(opt checker.Options) {
	const (
		unvisited = 0
		busy      = 1
		done      = 2
	)
	ambient := p.buildAmbient(opt)
	state := map[*SourceFile]int{}
	modTy := map[*SourceFile]types.Type{}
	// Decl files contribute only ambient (checked once above); they never
	// carry a runtime module type, preserving the classic any-fallback.
	for _, sf := range p.Files {
		if sf.IsDecl {
			state[sf] = done
			modTy[sf] = types.Any
		}
	}
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
		fileOpt.Ambient = ambient
		ds, _, mt := checker.CheckFileModule(sf.File, fileOpt)
		p.Diags = append(p.Diags, ds...)
		modTy[sf] = mt
		state[sf] = done
	}
	for _, sf := range p.Files {
		checkOne(sf)
	}
}

// buildAmbient checks global .d.tl files once into a shared parent scope.
// Returns nil when no ambient decls participate (classic isolated mode).
func (p *Program) buildAmbient(opt checker.Options) *checker.Scope {
	var decls []*ast.File
	allow := p.Cfg.AmbientTypes()
	for _, sf := range p.Files {
		if !sf.IsDecl || sf.File == nil {
			continue
		}
		if !config.IsAmbientIncluded(sf.Path, allow) {
			continue
		}
		decls = append(decls, sf.File)
	}
	if len(decls) == 0 {
		return nil
	}
	sort.Slice(decls, func(i, j int) bool { return decls[i].Name < decls[j].Name })
	base, diags := checker.BuildAmbient(decls, opt)
	p.Diags = append(p.Diags, diags...)
	return base
}
