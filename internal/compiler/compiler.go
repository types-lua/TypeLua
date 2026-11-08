package compiler

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/typelua/typelua/internal/ast"
	"github.com/typelua/typelua/internal/checker"
	"github.com/typelua/typelua/internal/config"
	"github.com/typelua/typelua/internal/diag"
	"github.com/typelua/typelua/internal/emitter"
	"github.com/typelua/typelua/internal/parser"
	"github.com/typelua/typelua/internal/sourcemap"
)

// SourceFile is a loaded unit.
type SourceFile struct {
	Path    string
	Content string
	File    *ast.File
	IsDecl  bool // *.d.tl
}

// Program is the full pipeline (like tsc Program).
type Program struct {
	Cfg   *config.Config
	Files []*SourceFile
	Diags []diag.Diagnostic
	// Per-file checker diags (same as Diags aggregated).
	// shorts maps manifest short names to full module paths for this run.
	shorts map[string]string
}

func NewProgram(cfg *config.Config) *Program {
	return &Program{Cfg: cfg}
}

// Load reads + parses all files. Lex/parse diags are collected.
// Files parse in parallel (bounded worker pool); results assemble in
// sorted-path order, so Files/Diags are identical to sequential loading.
func (p *Program) Load() error {
	list, err := p.Cfg.ListFiles()
	if err != nil {
		return err
	}
	sort.Strings(list)
	type parsed struct {
		data    []byte
		readErr error
		file    *ast.File
		pdiags  []diag.Diagnostic
	}
	results := make([]parsed, len(list))
	parallelDo(len(list), func(i int) {
		fp := list[i]
		data, err := os.ReadFile(fp)
		if err != nil {
			results[i].readErr = err
			return
		}
		rel := fp
		if r, err := filepath.Rel(p.Cfg.Dir, fp); err == nil {
			rel = r
		}
		// parser.ParseFile is pure (no package-level mutable state).
		f, pdiags := parser.ParseFile(rel, string(data))
		results[i] = parsed{data: data, file: f, pdiags: pdiags}
	})
	for i, fp := range list {
		r := results[i]
		if r.readErr != nil {
			p.Diags = append(p.Diags, diag.Diagnostic{Code: diag.CannotReadFile, Message: fmt.Sprintf("cannot read file: %v", r.readErr)})
			continue
		}
		p.Diags = append(p.Diags, r.pdiags...)
		p.Files = append(p.Files, &SourceFile{Path: fp, Content: string(r.data), File: r.file, IsDecl: strings.HasSuffix(fp, ".d.tl")})
	}
	return nil
}

// AddVirtual parses an in-memory source and appends it to the program.
// Used by tooling (`tlua test` injects its runner and helper module) and by
// tests that need generated sources without touching disk. Diagnostics from
// the parse are collected like any other file.
func (p *Program) AddVirtual(path, content string) {
	f, diags := parser.ParseFile(path, content)
	p.Diags = append(p.Diags, diags...)
	p.Files = append(p.Files, &SourceFile{
		Path:    path,
		Content: content,
		File:    f,
		IsDecl:  strings.HasSuffix(path, ".d.tl"),
	})
}

// LoadSources is for tests / in-memory use.
func (p *Program) LoadSources(srcs map[string]string) {
	var names []string
	for n := range srcs {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		f, pdiags := parser.ParseFile(n, srcs[n])
		p.Diags = append(p.Diags, pdiags...)
		p.Files = append(p.Files, &SourceFile{Path: n, Content: srcs[n], File: f, IsDecl: strings.HasSuffix(n, ".d.tl")})
	}
}

// Check runs the type checker on all files in dependency order (see
// require.go): each file sees the module types of its requirements,
// falling back to `any` per edge when unresolved.
func (p *Program) Check() {
	opt := checker.Options{
		Strict:          p.Cfg.Strict(),
		StrictNilChecks: p.Cfg.StrictNilChecks(),
		NoImplicitAny:   p.Cfg.NoImplicitAny(),
		Target:          p.Cfg.Target(),
	}
	p.shorts = p.shortModules()
	rel := newRelativeResolver(p.Files, p.Cfg.Dir)
	opt.RelativeImport = rel.target
	p.checkAll(opt)
}

// HasErrors reports whether any error-severity diagnostic exists.
func (p *Program) HasErrors() bool {
	for _, d := range p.Diags {
		if d.Severity == diag.Error {
			return true
		}
	}
	return false
}

// Emit writes .lua outputs to outDir. Returns emitted paths. Skips .d.tl and `declare` bodies.
// Type-only decls are erased by the emitter; `declare function` empty bodies are skipped at file level?
// For safety, we emit every non-decl file (even empty) to preserve build shape.
// Native assets (*.lua shims, *.so/*.dylib/*.dll/*.a) are then copied preserving
// layout so vendored cross-language bindings run without manual steps.
//
// Performance: emit compute (AST render + sourcemap encode, the CPU-heavy
// part) runs in parallel; filesystem writes stay sequential in file order,
// preserving exact error/partial-output semantics. Writes are content
// compared first, so unchanged outputs keep their mtime (no downstream
// rebuild loops); skipped paths still count as emitted.
func (p *Program) Emit() ([]string, error) {
	withPrelude := NeedsModPath(p)
	opt := emitter.Options{
		Target:          p.Cfg.Target(),
		TreeShake:       p.Cfg.CompilerOptions.TreeShake,
		RelativeModules: newRelativeResolver(p.Files, p.Cfg.Dir).moduleMap(),
	}
	jobs := collectEmitJobs(p.Cfg, p.Files)
	computeEmitJobs(jobs, opt, withPrelude)
	emittedSet := map[string]bool{}
	emitted, err := writeEmitJobs(jobs, nil, emittedSet)
	if err != nil {
		return emitted, err
	}
	assets, err := p.emitNativeAssets(emittedSet)
	if err != nil {
		return emitted, err
	}
	return append(emitted, assets...), nil
}

// emitJob is one file's output unit (mapping in, rendered bytes out).
type emitJob struct {
	sf       *SourceFile
	dst      string
	srcRel   string
	out      string
	mapRaw   []byte
	mapErr   error
	wantMap  bool
	mapName  string
	mapLabel string // "--# sourceMappingURL=..." footer, appended after map write
	mappings []sourcemap.Mapping
}

// collectEmitJobs maps non-decl files to outputs in program order.
func collectEmitJobs(cfg *config.Config, files []*SourceFile) []emitJob {
	var jobs []emitJob
	for _, sf := range files {
		if sf.IsDecl {
			continue
		}
		dst, ok := OutputForFile(cfg, sf.Path)
		if !ok {
			continue
		}
		rel, err := filepath.Rel(filepath.Join(cfg.Dir, cfg.RootDir()), sf.Path)
		if err != nil {
			rel = filepath.Base(sf.Path)
		}
		srcRel := filepath.ToSlash(rel)
		jb := emitJob{sf: sf, dst: dst, srcRel: srcRel}
		if cfg.CompilerOptions.SourceMap {
			// Handle `.d.tl` already skipped; `foo.tl` -> `foo.lua`.
			base := filepath.Base(dst)
			jb.wantMap = true
			jb.mapName = dst + ".map"
			jb.mapLabel = "--# sourceMappingURL=" + base + ".map\n"
		}
		jobs = append(jobs, jb)
	}
	return jobs
}

// computeEmitJobs runs the pure render phase in parallel:
// EmitFileWithMap, PrependPrelude and sourcemap.Encode touch only fresh
// per-file slices (no shared state).
func computeEmitJobs(jobs []emitJob, opt emitter.Options, withPrelude bool) {
	parallelDo(len(jobs), func(i int) {
		jb := &jobs[i]
		fileOpt := opt
		if jb.sf.File != nil {
			fileOpt.FileName = jb.sf.File.Name
		}
		out, mappings := emitter.EmitFileWithMap(jb.sf.File, fileOpt)
		if withPrelude {
			out, mappings = PrependPrelude(out, mappings)
		}
		for k := range mappings {
			mappings[k].SrcFile = jb.srcRel
		}
		jb.mappings = mappings
		if jb.wantMap {
			raw, err := sourcemap.Encode(filepath.Base(jb.dst), []string{jb.srcRel}, []string{jb.sf.Content}, nil, mappings)
			if err != nil {
				jb.mapErr = err
				return
			}
			jb.mapRaw = raw
		}
		jb.out = out
	})
}

// writeEmitJobs performs the ordered write phase: mkdir, encode-error
// check, map write, lua write per file (same fault order as the original
// sequential emitter). Content-compared writes keep mtimes stable;
// skipped paths still count as emitted. Fills emittedSet with every
// destination (needed by the native-asset collision guard).
func writeEmitJobs(jobs []emitJob, emitted []string, emittedSet map[string]bool) ([]string, error) {
	for i := range jobs {
		jb := &jobs[i]
		// Handle `.d.tl` already skipped; `foo.tl` -> `foo.lua`.
		if err := os.MkdirAll(filepath.Dir(jb.dst), 0o755); err != nil {
			return emitted, err
		}
		// Same fault order as before: mkdir, encode, map write, lua write.
		if jb.wantMap && jb.mapErr != nil {
			return emitted, jb.mapErr
		}
		if jb.wantMap {
			if _, err := writeFileIfChanged(jb.mapName, jb.mapRaw); err != nil {
				return emitted, err
			}
			jb.out += jb.mapLabel
		}
		if _, err := writeFileIfChanged(jb.dst, []byte(jb.out)); err != nil {
			return emitted, err
		}
		emitted = append(emitted, jb.dst)
		emittedSet[filepath.Clean(jb.dst)] = true
	}
	return emitted, nil
}

// writeFileIfChanged writes data to path, skipping the write (and the mtime
// bump) when current content is byte-identical. Reports whether it wrote.
// Skipped paths still count as emitted so watch stale-tracking stays exact.
func writeFileIfChanged(path string, data []byte) (bool, error) {
	if cur, err := os.ReadFile(path); err == nil && string(cur) == string(data) {
		return false, nil
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return false, err
	}
	return true, nil
}

// RenderedFile is one in-memory emitted unit with its source mappings.
// Unlike Emit, Render does not touch the filesystem; tooling (the DAP
// debugger) uses it to place emitted Lua and map runtime lines back.
type RenderedFile struct {
	Path       string // output .lua path (outDir layout)
	SourcePath string // originating .tl path
	Lua        string
	Mappings   []sourcemap.Mapping
}

// Render emits every file in memory, with mappings attached.
func (p *Program) Render() []RenderedFile {
	withPrelude := NeedsModPath(p)
	opt := emitter.Options{
		Target:          p.Cfg.Target(),
		TreeShake:       p.Cfg.CompilerOptions.TreeShake,
		RelativeModules: newRelativeResolver(p.Files, p.Cfg.Dir).moduleMap(),
	}
	jobs := collectEmitJobs(p.Cfg, p.Files)
	computeEmitJobs(jobs, opt, withPrelude)
	out := make([]RenderedFile, 0, len(jobs))
	for i := range jobs {
		jb := &jobs[i]
		out = append(out, RenderedFile{
			Path:       jb.dst,
			SourcePath: jb.sf.Path,
			Lua:        jb.out,
			Mappings:   jb.mappings,
		})
	}
	return out
}

// EmitToString is for tests.
func (p *Program) EmitToString() map[string]string {
	opt := emitter.Options{
		Target:          p.Cfg.Target(),
		TreeShake:       p.Cfg.CompilerOptions.TreeShake,
		RelativeModules: newRelativeResolver(p.Files, p.Cfg.Dir).moduleMap(),
	}
	out := map[string]string{}
	for _, sf := range p.Files {
		if sf.IsDecl {
			continue
		}
		fileOpt := opt
		if sf.File != nil {
			fileOpt.FileName = sf.File.Name
		}
		out[sf.Path] = emitter.EmitFile(sf.File, fileOpt)
	}
	return out
}

// FormatDiags renders diagnostics with file:line:col + message, sorted.
func (p *Program) FormatDiags() string {
	ds := append([]diag.Diagnostic{}, p.Diags...)
	sort.Slice(ds, func(i, j int) bool {
		if ds[i].Span.File != ds[j].Span.File {
			return ds[i].Span.File < ds[j].Span.File
		}
		if ds[i].Span.Start.Line != ds[j].Span.Start.Line {
			return ds[i].Span.Start.Line < ds[j].Span.Start.Line
		}
		return ds[i].Span.Start.Column < ds[j].Span.Start.Column
	})
	var sb strings.Builder
	for _, d := range ds {
		sb.WriteString(d.String())
		sb.WriteString("\n")
	}
	return sb.String()
}

// FormatDiagsJSON renders the same set as a JSON array for LLM agents
// and editor integrations (stable keys, sorted order, exit code unchanged).
func (p *Program) FormatDiagsJSON() string {
	return FormatDiagsJSON(p.Diags)
}

// DiagJSON is the machine-readable form of one diagnostic.
type DiagJSON struct {
	File     string `json:"file"`
	Line     int    `json:"line"`
	Column   int    `json:"column"`
	EndLine  int    `json:"endLine"`
	EndCol   int    `json:"endColumn"`
	Severity string `json:"severity"`
	Code     string `json:"code"`
	Category string `json:"category"`
	Message  string `json:"message"`
}

// FormatDiagsJSON renders any diagnostic slice as a sorted JSON array.
func FormatDiagsJSON(diags []diag.Diagnostic) string {
	ds := append([]diag.Diagnostic{}, diags...)
	sort.Slice(ds, func(i, j int) bool {
		if ds[i].Span.File != ds[j].Span.File {
			return ds[i].Span.File < ds[j].Span.File
		}
		if ds[i].Span.Start.Line != ds[j].Span.Start.Line {
			return ds[i].Span.Start.Line < ds[j].Span.Start.Line
		}
		return ds[i].Span.Start.Column < ds[j].Span.Start.Column
	})
	out := make([]DiagJSON, 0, len(ds))
	for _, d := range ds {
		out = append(out, DiagJSON{
			File:     d.Span.File,
			Line:     d.Span.Start.Line,
			Column:   d.Span.Start.Column,
			EndLine:  d.Span.End.Line,
			EndCol:   d.Span.End.Column,
			Severity: d.Severity.String(),
			Code:     string(d.Code),
			Category: d.Code.Category().String(),
			Message:  d.Message,
		})
	}
	raw, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return "[]\n"
	}
	return string(raw) + "\n"
}
