package compiler

import (
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
func (p *Program) Load() error {
	list, err := p.Cfg.ListFiles()
	if err != nil {
		return err
	}
	sort.Strings(list)
	for _, fp := range list {
		data, err := os.ReadFile(fp)
		if err != nil {
			p.Diags = append(p.Diags, diag.Diagnostic{Code: diag.CannotReadFile, Message: fmt.Sprintf("cannot read file: %v", err)})
			continue
		}
		rel := fp
		if r, err := filepath.Rel(p.Cfg.Dir, fp); err == nil {
			rel = r
		}
		f, pdiags := parser.ParseFile(rel, string(data))
		p.Diags = append(p.Diags, pdiags...)
		p.Files = append(p.Files, &SourceFile{Path: fp, Content: string(data), File: f, IsDecl: strings.HasSuffix(fp, ".d.tl")})
	}
	return nil
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
// Native assets (*.lua shims, *.so/*.dylib/*.dll) are then copied preserving
// layout so vendored cross-language bindings run without manual steps.
func (p *Program) Emit() ([]string, error) {
	var emitted []string
	withPrelude := NeedsModPath(p)
	opt := emitter.Options{Target: p.Cfg.Target(), TreeShake: p.Cfg.CompilerOptions.TreeShake}
	emittedSet := map[string]bool{}
	for _, sf := range p.Files {
		if sf.IsDecl {
			continue
		}
		dst, ok := OutputForFile(p.Cfg, sf.Path)
		if !ok {
			continue
		}
		out, mappings := emitter.EmitFileWithMap(sf.File, opt)
		if withPrelude {
			out, mappings = PrependPrelude(out, mappings)
		}
		rel, err := filepath.Rel(filepath.Join(p.Cfg.Dir, p.Cfg.RootDir()), sf.Path)
		if err != nil {
			rel = filepath.Base(sf.Path)
		}
		srcRel := filepath.ToSlash(rel)
		// Handle `.d.tl` already skipped; `foo.tl` -> `foo.lua`.
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return emitted, err
		}
		if p.Cfg.CompilerOptions.SourceMap {
			base := filepath.Base(dst)
			for i := range mappings {
				mappings[i].SrcFile = srcRel
			}
			raw, err := sourcemap.Encode(base, []string{srcRel}, []string{sf.Content}, nil, mappings)
			if err != nil {
				return emitted, err
			}
			if err := os.WriteFile(dst+".map", raw, 0o644); err != nil {
				return emitted, err
			}
			out += "--# sourceMappingURL=" + base + ".map\n"
		}
		if err := os.WriteFile(dst, []byte(out), 0o644); err != nil {
			return emitted, err
		}
		emitted = append(emitted, dst)
		emittedSet[filepath.Clean(dst)] = true
	}
	assets, err := p.emitNativeAssets(emittedSet)
	if err != nil {
		return emitted, err
	}
	return append(emitted, assets...), nil
}

// EmitToString is for tests.
func (p *Program) EmitToString() map[string]string {
	opt := emitter.Options{Target: p.Cfg.Target(), TreeShake: p.Cfg.CompilerOptions.TreeShake}
	out := map[string]string{}
	for _, sf := range p.Files {
		if sf.IsDecl {
			continue
		}
		out[sf.Path] = emitter.EmitFile(sf.File, opt)
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
