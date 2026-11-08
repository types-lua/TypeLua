package compiler

import (
	"path/filepath"
	"sort"
	"strings"

	"github.com/typelua/typelua/internal/ast"
	"github.com/typelua/typelua/internal/checker"
	"github.com/typelua/typelua/internal/config"
	"github.com/typelua/typelua/internal/diag"
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
	return fileByPathIn(p.Files, path)
}

func fileByPathIn(files []*SourceFile, path string) *SourceFile {
	clean := filepath.Clean(path)
	for _, sf := range files {
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
	return resolveRequireIn(p.Files, p.shorts, p.Cfg.Dir, from, dotted)
}

func resolveRequireIn(files []*SourceFile, shorts map[string]string, cfgDir string, from *SourceFile, dotted string) *SourceFile {
	if full, ok := shorts[dotted]; ok {
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
			filepath.Join(cfgDir, mod.VendorDir, rel),
			// Project-root fallback: lets files in subdirectories (tests/,
			// examples/) import project-level modules by dotted name without
			// duplicating them under every directory. Tried last, so file
			// siblings and vendored dependencies keep precedence.
			filepath.Join(cfgDir, rel),
		}
		for _, base := range bases {
			if sf := fileByPathIn(files, base+".tl"); sf != nil {
				return sf
			}
			if sf := fileByPathIn(files, filepath.Join(base, "init.tl")); sf != nil {
				return sf
			}
		}
	}
	return nil
}

// checkDriver runs memoized depth-first checking over a file set, feeding
// each file the module types of its requirements. Files check exactly
// once; cycles and missing targets degrade to `any` for the affected edge
// only. Roots are visited in slice order, so runs over the same set with
// the same seed produce identical results. The driver is single-threaded:
// checker scopes share ambient entries with warm mutable caches by design,
// so parallel checking would corrupt results (see ambient.go).
type checkDriver struct {
	files   []*SourceFile
	shorts  map[string]string
	cfgDir  string
	ambient *checker.Scope
	opt     checker.Options
	state   map[*SourceFile]int
	modTy   map[*SourceFile]types.Type
	// requires lists a file's dotted requires; nil selects the live walk
	// (checker.RequiredModules). Sessions inject parse-time caches.
	requires func(sf *SourceFile) []string
	// onCheck receives each completed file in visit order.
	onCheck func(sf *SourceFile, ds []diag.Diagnostic, mt types.Type)
}

func liveRequires(sf *SourceFile) []string {
	return checker.RequiredModules(sf.File)
}

const (
	checkUnvisited = 0
	checkBusy      = 1
	checkDone      = 2
)

// seedDone pre-marks files as already checked (ambient decls, or cached
// files in incremental runs) with their known module types.
func (d *checkDriver) seedDone(sf *SourceFile, mt types.Type) {
	d.state[sf] = checkDone
	d.modTy[sf] = mt
}

func (d *checkDriver) checkOne(sf *SourceFile) {
	if d.state[sf] == checkDone {
		return
	}
	if d.state[sf] == checkBusy {
		return // cyclic require: preset Any stands for this edge
	}
	d.state[sf] = checkBusy
	d.modTy[sf] = types.Any
	env := map[string]types.Type{}
	// requires defaults to a live walk (pre-own-expansion at this point in
	// full runs); incremental runs inject parse-time cached requires so
	// rechecks of already-expanded ASTs see identical edges.
	reqs := liveRequires(sf)
	if d.requires != nil {
		reqs = d.requires(sf)
	}
	for _, dotted := range reqs {
		tgt := resolveRequireIn(d.files, d.shorts, d.cfgDir, sf, dotted)
		if tgt == nil || tgt.IsDecl {
			continue
		}
		d.checkOne(tgt)
		env[dotted] = d.modTy[tgt]
	}
	fileOpt := d.opt
	fileOpt.RequireTypes = env
	fileOpt.Ambient = d.ambient
	ds, _, mt := checker.CheckFileModule(sf.File, fileOpt)
	d.modTy[sf] = mt
	d.state[sf] = checkDone
	d.onCheck(sf, ds, mt)
}

// checkAll checks every file in dependency order, feeding each file the
// module types of its requirements. Files check exactly once; cycles and
// missing targets degrade to `any` for the affected edge only.
//
// Ambient .d.tl files (filtered by `types` allowlist) are checked once up
// front into a shared parent scope (builtins + globals, like tsconfig
// types); program files then check as children so shadowing stays detached.
func (p *Program) checkAll(opt checker.Options) {
	ambient := p.buildAmbient(opt)
	d := &checkDriver{
		files:   p.Files,
		shorts:  p.shorts,
		cfgDir:  p.Cfg.Dir,
		ambient: ambient,
		opt:     opt,
		state:   map[*SourceFile]int{},
		modTy:   map[*SourceFile]types.Type{},
		onCheck: func(sf *SourceFile, ds []diag.Diagnostic, mt types.Type) {
			p.Diags = append(p.Diags, ds...)
		},
	}
	// Decl files contribute only ambient (checked once above); they never
	// carry a runtime module type, preserving the classic any-fallback.
	for _, sf := range p.Files {
		if sf.IsDecl {
			d.seedDone(sf, types.Any)
		}
	}
	for _, sf := range p.Files {
		d.checkOne(sf)
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
