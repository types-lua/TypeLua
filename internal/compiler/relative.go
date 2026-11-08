package compiler

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/typelua/typelua/internal/checker"
)

// Relative imports (`import X from "./mod"`) resolve against the requiring
// file and emit a runtime `require` name that the normal package search
// already understands:
//
//   - inside the repository:        "lib.leveldb.codec"
//   - inside tlua_modules/<pkg>:    "example.com.leveldb.leveldb_codec"
//
// The name is the target's path relative to the project root (or to the
// vendor root) with slashes turned into dots. Domain directories keep their
// dots, which the vendor searcher expands back correctly, so a package works
// no matter which dependency key it was installed under.
//
// Names are canonical (`pkg/init.tl` -> `pkg`), which keeps module identity
// stable with a plain `require("pkg")` elsewhere in the program.

// relativeResolver answers both the checker (validation + type wiring) and
// the emitter (runtime module name) for one program snapshot.
type relativeResolver struct {
	files  []*SourceFile
	cfgDir string
	// names maps candidate runtime module names to canonical file paths, so
	// collisions (`a.b.tl` vs `a/b.tl`) are reported instead of silently
	// loading the wrong module.
	names map[string]string
	// ambiguous marks names claimed by more than one file.
	ambiguous map[string]bool
}

func newRelativeResolver(files []*SourceFile, cfgDir string) *relativeResolver {
	r := &relativeResolver{
		files:     files,
		cfgDir:    cfgDir,
		names:     map[string]string{},
		ambiguous: map[string]bool{},
	}
	for _, sf := range files {
		if sf.IsDecl || sf.File == nil {
			continue
		}
		name, ok := r.moduleName(sf.Path)
		if !ok {
			continue
		}
		if prev, exists := r.names[name]; exists && prev != sf.Path {
			r.ambiguous[name] = true
			continue
		}
		r.names[name] = sf.Path
	}
	return r
}

// moduleName derives the canonical runtime module name for an emitted file.
func (r *relativeResolver) moduleName(path string) (string, bool) {
	rel, ok := r.relativeToRoot(path)
	if !ok {
		return "", false
	}
	rel = strings.TrimSuffix(rel, ".tl")
	if rel == "" {
		return "", false
	}
	// Canonical package root: pkg/init.tl behaves exactly like pkg.tl.
	rel = strings.TrimSuffix(rel, "/init")
	if rel == "" {
		return "init", true
	}
	return strings.ReplaceAll(rel, "/", "."), true
}

// relativeToRoot returns the path relative to tlua_modules/ when the file is
// vendored, otherwise relative to the project root.
func (r *relativeResolver) relativeToRoot(path string) (string, bool) {
	vendor := filepath.Join(r.cfgDir, "tlua_modules")
	if rel, err := filepath.Rel(vendor, path); err == nil && !escapes(rel) {
		return filepath.ToSlash(rel), true
	}
	rel, err := filepath.Rel(r.cfgDir, path)
	if err != nil || escapes(rel) {
		return "", false
	}
	return filepath.ToSlash(rel), true
}

func escapes(rel string) bool {
	return rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// target resolves a relative spec against the requiring AST file. It returns
// the module name, or a user-facing message describing why it cannot.
func (r *relativeResolver) target(fromFile string, spec string) (string, string) {
	from := r.fileByASTName(fromFile)
	if from == nil {
		return "", fmt.Sprintf("cannot resolve %q: unknown requiring file %s", spec, fromFile)
	}
	base := filepath.Join(filepath.Dir(from.Path), filepath.FromSlash(spec))
	for _, candidate := range []string{base + ".tl", filepath.Join(base, "init.tl")} {
		tgt := fileByPathIn(r.files, candidate)
		if tgt == nil {
			continue
		}
		if tgt.IsDecl {
			return "", fmt.Sprintf("%q resolves to a declaration file, which has no runtime value", spec)
		}
		name, ok := r.moduleName(tgt.Path)
		if !ok {
			return "", fmt.Sprintf("%q resolves outside the project root", spec)
		}
		if r.ambiguous[name] {
			return "", fmt.Sprintf("%q is ambiguous: module name %q is claimed by multiple files", spec, name)
		}
		if other, exists := r.names[name]; exists && other != tgt.Path {
			return "", fmt.Sprintf("%q is ambiguous: module name %q is claimed by %s", spec, name, other)
		}
		return name, ""
	}
	return "", fmt.Sprintf("module %q not found relative to %s", spec, fromFile)
}

func (r *relativeResolver) fileByASTName(name string) *SourceFile {
	for _, sf := range r.files {
		if sf.File != nil && sf.File.Name == name {
			return sf
		}
	}
	return nil
}

// moduleMap keys every relative spec by (AST file, spec) for the emitter.
// Emit failures are already reported by the checker; unresolved entries are
// simply absent and the emitter keeps the original require as a fallback.
func (r *relativeResolver) moduleMap() map[string]string {
	out := map[string]string{}
	for _, sf := range r.files {
		if sf.File == nil || sf.IsDecl {
			continue
		}
		for _, spec := range checker.RequiredModules(sf.File) {
			if !isRelativeSpec(spec) {
				continue
			}
			name, msg := r.target(sf.File.Name, spec)
			if msg != "" {
				continue
			}
			out[sf.File.Name+"\x00"+spec] = name
		}
	}
	return out
}

// isRelativeSpec reports the package-local import form ("./x", "../x").
func isRelativeSpec(spec string) bool {
	return strings.HasPrefix(spec, "./") || strings.HasPrefix(spec, "../")
}
