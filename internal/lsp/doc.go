package lsp

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
	"github.com/typelua/typelua/internal/parser"
)

// Open documents: content, parse, check, and the position index.

// ---------- documents ----------

type document struct {
	uri     string
	version int
	content string
	lines   []int // byte offset of each 0-based line start
	file    *ast.File
	diags   []diag.Diagnostic
	index   *checker.SymIndex
}

func uriToName(uri string) string {
	if strings.HasPrefix(uri, "file://") {
		return strings.TrimPrefix(uri, "file://")
	}
	return uri
}

func splitLines(content string) []int {
	lines := []int{0}
	for i := 0; i < len(content); i++ {
		if content[i] == '\n' {
			lines = append(lines, i+1)
		}
	}
	return lines
}

// ambientEntry caches the ambient scope for one tlua.json project. The LSP is
// still single-file for cross-file symbol resolution, but global `.d.tl`
// declarations (tfconfig types/typeRoots) must be visible or every annotation
// such as `T` from an ambient file reports TL3103 in the editor even though
// `tlua check` succeeds.
type ambientEntry struct {
	scope       *checker.Scope
	opt         checker.Options
	configStamp string
	declPaths   []string
	stamp       string
}

// analyze checks one document. If it belongs to a project, ambient
// declarations are loaded from that project's `types/` roots.
func (s *Server) analyze(uri, content string, version int) *document {
	name := uriToName(uri)
	f, pdiags := parser.ParseFile(name, content)
	opt, ambient := s.ambientFor(name)
	if ambient != nil {
		opt.Ambient = ambient
	}
	diags, idx := checker.CheckFileIndexed(f, opt)
	diags = append(append([]diag.Diagnostic{}, pdiags...), diags...)
	return &document{uri: uri, version: version, content: content, lines: splitLines(content), file: f, diags: diags, index: idx}
}

// ambientFor locates tlua.json from the document directory, loads the configured
// strictness and builds/caches the project's ambient declaration scope.
func (s *Server) ambientFor(name string) (checker.Options, *checker.Scope) {
	opt := checker.DefaultOptions()
	// Untitled buffers and other non-file URIs have no project location.
	if !filepath.IsAbs(name) {
		return opt, nil
	}
	cfgPath := config.FindUp(filepath.Dir(name))
	if cfgPath == "" {
		return opt, nil
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return opt, nil
	}
	opt.Strict = cfg.Strict()
	opt.StrictNilChecks = cfg.StrictNilChecks()
	opt.NoImplicitAny = cfg.NoImplicitAny()
	opt.Target = cfg.Target()

	// A declaration file checks standalone; adding itself as ambient would
	// duplicate its own declarations.
	if strings.HasSuffix(name, ".d.tl") {
		return opt, nil
	}

	stamp := s.projectStamp(cfgPath, cfg)
	entry := s.ambients[cfgPath]
	if entry == nil || entry.configStamp != stamp {
		paths := s.ambientPaths(cfg)
		entry = &ambientEntry{configStamp: stamp, declPaths: paths}
		s.ambients[cfgPath] = entry
	}
	if len(entry.declPaths) == 0 {
		return opt, nil
	}
	entry.opt = opt
	fileStamp := stampPaths(entry.declPaths)
	if entry.scope == nil || entry.stamp != fileStamp {
		decls := make([]*ast.File, 0, len(entry.declPaths))
		for _, path := range entry.declPaths {
			data, err := os.ReadFile(path)
			if err != nil {
				continue
			}
			decl, pdiags := parser.ParseFile(path, string(data))
			if len(pdiags) != 0 {
				continue
			}
			decls = append(decls, decl)
		}
		if len(decls) == 0 {
			return opt, nil
		}
		// Ambient diagnostics are not attributed to the open document; the
		// authoritative project check (`tlua check`) reports them on the .d.tl.
		base, _ := checker.BuildAmbient(decls, opt)
		entry.scope = base
		entry.stamp = fileStamp
	}
	return opt, entry.scope
}

// ambientPaths lists the .d.tl files selected by the project's type roots.
func (s *Server) ambientPaths(cfg *config.Config) []string {
	files, err := cfg.ListFiles()
	if err != nil {
		return nil
	}
	var out []string
	for _, path := range files {
		if strings.HasSuffix(path, ".d.tl") && config.IsAmbientIncluded(path, cfg.AmbientTypes()) {
			out = append(out, path)
		}
	}
	sort.Strings(out)
	return out
}

// projectStamp changes when the config or a type-root directory changes, so
// adding/removing a declaration file triggers a fresh ListFiles.
func (s *Server) projectStamp(cfgPath string, cfg *config.Config) string {
	paths := []string{cfgPath}
	paths = append(paths, cfg.TypeRoots()...)
	return stampPaths(paths)
}

func stampPaths(paths []string) string {
	var b strings.Builder
	for _, path := range paths {
		info, err := os.Stat(path)
		if err != nil {
			fmt.Fprintf(&b, "%s|missing;", path)
			continue
		}
		fmt.Fprintf(&b, "%s|%d|%d|%t;", path, info.ModTime().UnixNano(), info.Size(), info.IsDir())
	}
	return b.String()
}
