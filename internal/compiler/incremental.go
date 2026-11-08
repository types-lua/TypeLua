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
	"github.com/typelua/typelua/internal/types"
	"github.com/typelua/typelua/internal/watch"
)

// Incremental watch sessions: steady-state cycles reuse parsed ASTs and
// cached check results, reprocessing only what the watcher reported.
//
// Correctness anchors (deviations here become wrong builds, so they are
// deliberately conservative):
//   - Membership is authoritative per cycle (fresh ListFiles), changes are
//     only a hint for what to reparse. Watcher blind spots cannot desync.
//   - A file is reparsed only when its bytes differ (Modified with identical
//     content is a no-op); expansion runs exactly once per parse, same as
//     one-shot builds. Ambient decls rebuild from fresh reparses, never
//     from already-expanded ASTs.
//   - Recheck uses the same memoized DFS driver as full builds, seeded with
//     cached results for unaffected files and expanded to full SCCs:
//     cyclic requires keep deterministic any-degradation.
//   - Ambient affects checking only, never emission: decl-only changes
//     recheck but do not re-emit.
//   - Emission is a pure function of (AST, options, prelude): unchanged
//     bytes with an unchanged prelude decision are skipped; write dedup
//     still guards every actual write.
//   - The checker stays single-threaded (ambient scopes carry warm mutable
//     caches by design); parallelism lives in parse and emit-compute only.
//   - Raw diagnostic order is unspecified; FormatDiags is the stable view.
//
// Session methods assume single-goroutine use (the watch loop); internal
// worker pools join before returning.

// fileState is one tracked source file with cached pipeline results.
type fileState struct {
	sf         *SourceFile
	parseDiags []diag.Diagnostic
	checkDiags []diag.Diagnostic
	modTy      types.Type
	reqs       []string
	targets    map[string]string
	emitDirty  bool
	isDecl     bool
}

// Session is an incremental build session. Use NewSession + Sync; Reset on
// config changes (config identity covers manifest short-names, ambient
// allowlist, targets and output layout).
type Session struct {
	cfg            *config.Config
	files          map[string]*fileState
	order          []string
	shorts         map[string]string
	ambient        *checker.Scope
	ambientDiags   []diag.Diagnostic
	ambientContent map[string]string
	unread         map[string][]diag.Diagnostic
	prelude        bool
	initialized    bool
	lastChecked    []string
	diags          []diag.Diagnostic
}

// NewSession creates an empty session for cfg.
func NewSession(cfg *config.Config) *Session {
	return &Session{cfg: cfg, files: map[string]*fileState{}, unread: map[string][]diag.Diagnostic{}}
}

// Reset drops all cached state (config changes, including manifest,
// ambient allowlist, target and outDir, all require a fresh baseline).
func (s *Session) Reset(cfg *config.Config) {
	*s = Session{cfg: cfg, files: map[string]*fileState{}, unread: map[string][]diag.Diagnostic{}}
}

// normKey canonicalizes a path for session maps (watch reports abs-clean).
func normKey(path string) string {
	if a, err := filepath.Abs(path); err == nil {
		return filepath.Clean(a)
	}
	return filepath.Clean(path)
}

// targetKey fingerprints a require edge for change detection: nil targets,
// decl-ness flips and path moves all count as changes. Paths go through
// normKey (not bare Clean) so edge keys always match the session map keys,
// even when the config Dir is relative and listed paths are not absolute.
func targetKey(tgt *SourceFile) string {
	if tgt == nil {
		return "nil"
	}
	if tgt.IsDecl {
		return "D:" + normKey(tgt.Path)
	}
	return "F:" + normKey(tgt.Path)
}

// Sync reconciles with disk and runs check (+emit). full forces a complete
// reload (first cycle, explicit callers); otherwise only watch-reported
// changes are reconsidered. doEmit=false is check mode (no emission).
// Returns all currently up-to-date outputs (like Emit, skipped-identical
// included) plus any fatal error. Diags are available via FormatDiags.
func (s *Session) Sync(full bool, changes []watch.Change, doEmit bool) ([]string, error) {
	if full || !s.initialized {
		return s.fullSync(doEmit)
	}
	for _, c := range changes {
		if c.Config {
			// Defensive: config must come through Reset+full; a stray
			// config diff degrades to a full sync, never a wrong build.
			return s.fullSync(doEmit)
		}
	}
	return s.incrSync(changes, doEmit)
}

// FileCount mirrors len(Program.Files) for status messages.
func (s *Session) FileCount() int { return len(s.files) }

// Files returns tracked units in program order (for stale-output tracking).
func (s *Session) Files() []*SourceFile {
	out := make([]*SourceFile, 0, len(s.order))
	for _, k := range s.order {
		out = append(out, s.files[k].sf)
	}
	return out
}

// HasErrors reports whether any error-severity diagnostic exists.
func (s *Session) HasErrors() bool {
	for _, d := range s.diags {
		if d.Severity == diag.Error {
			return true
		}
	}
	return false
}

// Diags returns a copy of the current diagnostics.
func (s *Session) Diags() []diag.Diagnostic {
	return append([]diag.Diagnostic(nil), s.diags...)
}

// FormatDiags renders diagnostics sorted (stable across sync styles).
func (s *Session) FormatDiags() string {
	ds := append([]diag.Diagnostic{}, s.diags...)
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

// FormatDiagsJSON renders the current session diagnostics as a sorted
// JSON array (same shape as Program.FormatDiagsJSON).
func (s *Session) FormatDiagsJSON() string {
	return FormatDiagsJSON(s.diags)
}

// LastChecked returns the sorted paths rechecked in the last Sync (subset
// proof for tests and watch logging).
func (s *Session) LastChecked() []string {
	return append([]string(nil), s.lastChecked...)
}

func (s *Session) assembleDiags() {
	var out []diag.Diagnostic
	keys := make([]string, 0, len(s.unread))
	for k := range s.unread {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		out = append(out, s.unread[k]...)
	}
	for _, k := range s.order {
		out = append(out, s.files[k].parseDiags...)
	}
	out = append(out, s.ambientDiags...)
	for _, k := range s.order {
		out = append(out, s.files[k].checkDiags...)
	}
	s.diags = out
}

// parseOne reads + parses a single listed path (pure per path).
func parseOne(cfg *config.Config, fp string) (content []byte, file *ast.File, pdiags []diag.Diagnostic, readErr error) {
	data, err := os.ReadFile(fp)
	if err != nil {
		return nil, nil, nil, err
	}
	rel := fp
	if r, err := filepath.Rel(cfg.Dir, fp); err == nil {
		rel = r
	}
	f, pds := parser.ParseFile(rel, string(data))
	return data, f, pds, nil
}

func readDiag(path string, err error) []diag.Diagnostic {
	return []diag.Diagnostic{{Code: diag.CannotReadFile, Message: fmt.Sprintf("cannot read file: %v", err)}}
}

// fullSync drops everything and runs the whole pipeline (same results as a
// fresh Program: identical parse order, ambient order, check visit order,
// emit order and fault behavior).
func (s *Session) fullSync(doEmit bool) ([]string, error) {
	s.files = map[string]*fileState{}
	s.unread = map[string][]diag.Diagnostic{}
	s.order = nil
	// A failed listing proceeds empty (mirrors Load ignoring the error;
	// Build reports no-files below, Check reports zero files).
	if list, err := s.cfg.ListFiles(); err == nil {
		sort.Strings(list)
		s.parseInto(list)
	}
	s.order = s.sortedKeys()
	s.shorts = s.shortModules()
	s.rebuildAmbientFresh()
	// Seed require-target fingerprints so the first incremental cycle has
	// a baseline to diff against (otherwise everything looks changed).
	for _, k := range s.order {
		st := s.files[k]
		if st.isDecl {
			continue
		}
		st.targets = s.resolveTargets(st.sf)
	}
	s.recheckAll()
	s.assembleDiags()
	if !doEmit {
		s.initialized = true
		return nil, nil
	}
	if len(s.files) == 0 {
		s.initialized = true
		return nil, fmt.Errorf("no .tl files found")
	}
	emitted, err := s.emitChanged()
	s.initialized = true
	return emitted, err
}

func (s *Session) sortedKeys() []string {
	keys := make([]string, 0, len(s.files))
	for k := range s.files {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func (s *Session) shortModules() map[string]string {
	p := &Program{Cfg: s.cfg}
	return p.shortModules()
}

// rebuildAmbientFresh rechecks included decls from fresh reparses (their
// stored ASTs may already be expanded; expansion must run exactly once per
// parse) and swaps in the new scope, diagnostics and input fingerprint.
func (s *Session) rebuildAmbientFresh() {
	s.ambientContent = map[string]string{}
	allow := s.cfg.AmbientTypes()
	var decls []*ast.File
	for _, k := range s.order {
		st := s.files[k]
		if !st.isDecl || st.sf.File == nil {
			continue
		}
		if !config.IsAmbientIncluded(st.sf.Path, allow) {
			continue
		}
		data, f, pds, rerr := parseOne(s.cfg, st.sf.Path)
		if rerr != nil {
			continue // unreadable decl: keep old state; unread covers it
		}
		st.sf = &SourceFile{Path: st.sf.Path, Content: string(data), File: f, IsDecl: true}
		st.parseDiags = pds
		st.reqs = checker.RequiredModules(f)
		st.targets = map[string]string{}
		st.emitDirty = false // decls never emit
		s.ambientContent[k] = st.sf.Content
		decls = append(decls, f)
	}
	opt := s.checkerOpt()
	if len(decls) == 0 {
		s.ambient = nil
		s.ambientDiags = nil
		return
	}
	sort.Slice(decls, func(i, j int) bool { return decls[i].Name < decls[j].Name })
	base, diags := checker.BuildAmbient(decls, opt)
	s.ambient = base
	s.ambientDiags = diags
}

func (s *Session) checkerOpt() checker.Options {
	return checker.Options{
		Strict:          s.cfg.Strict(),
		StrictNilChecks: s.cfg.StrictNilChecks(),
		NoImplicitAny:   s.cfg.NoImplicitAny(),
		Target:          s.cfg.Target(),
	}
}

// fileList returns tracked units in program order.
func (s *Session) fileList() []*SourceFile {
	out := make([]*SourceFile, 0, len(s.order))
	for _, k := range s.order {
		out = append(out, s.files[k].sf)
	}
	return out
}

// cachedRequires feeds the driver parse-time require lists (identical to
// what a live walk sees on unexpanded ASTs, stable across rechecks).
func (s *Session) cachedRequires(sf *SourceFile) []string {
	if st, ok := s.files[normKey(sf.Path)]; ok {
		return st.reqs
	}
	return liveRequires(sf)
}

// recheckAll runs the shared DFS driver over every non-decl file.
func (s *Session) recheckAll() {
	files := s.fileList()
	s.lastChecked = nil
	d := &checkDriver{
		files:    files,
		shorts:   s.shorts,
		cfgDir:   s.cfg.Dir,
		ambient:  s.ambient,
		opt:      s.checkerOpt(),
		requires: s.cachedRequires,
		state:    map[*SourceFile]int{},
		modTy:    map[*SourceFile]types.Type{},
		onCheck: func(sf *SourceFile, ds []diag.Diagnostic, mt types.Type) {
			if st, ok := s.files[normKey(sf.Path)]; ok {
				st.checkDiags = ds
				st.modTy = mt
				s.lastChecked = append(s.lastChecked, normKey(sf.Path))
			}
		},
	}
	for _, sf := range files {
		if sf.IsDecl {
			d.seedDone(sf, types.Any)
		}
	}
	for _, sf := range files {
		d.checkOne(sf)
	}
	sort.Strings(s.lastChecked)
}

// incrSync reconciles membership authoritatively (fresh ListFiles; changes
// only hint what to reparse), then rechecks the dirty closure and emits
// what changed. Unchanged files keep cached parses, diags, types and
// outputs bit-for-bit.
func (s *Session) incrSync(changes []watch.Change, doEmit bool) ([]string, error) {
	list, err := s.cfg.ListFiles()
	if err != nil {
		// Membership unknown: touch nothing, let the caller warn and keep
		// the last good state (never build from a half-known set).
		return nil, err
	}
	listed := map[string]bool{}
	for _, fp := range list {
		listed[normKey(fp)] = true
	}
	for k := range s.files {
		if !listed[k] {
			delete(s.files, k)
		}
	}
	for k := range s.unread {
		if !listed[k] {
			delete(s.unread, k)
		}
	}
	changed := map[string]watch.ChangeKind{}
	for _, c := range changes {
		if c.Config {
			continue // unreachable (Sync degrades); ignored defensively
		}
		changed[normKey(c.Path)] = c.Kind
	}
	var toParse []string
	for _, fp := range list {
		k := normKey(fp)
		if _, tracked := s.files[k]; !tracked {
			toParse = append(toParse, fp) // adds + unreadable retries
			continue
		}
		if kind, flagged := changed[k]; flagged && kind != watch.Removed {
			toParse = append(toParse, fp)
		}
	}
	contentChanged := s.parseInto(toParse)
	s.order = s.sortedKeys()
	// Ambient rebuild only when included-decl membership/content changed;
	// included decls reparse fresh (stored ASTs were already expanded).
	// Decl files are never standalone-checked (mirrors checkAll seeding),
	// so their checkDiags buckets stay empty by construction.
	dirty := map[string]bool{}
	if s.ambientInputsChanged() {
		s.rebuildAmbientFresh()
		for _, k := range s.order {
			if !s.files[k].isDecl {
				dirty[k] = true
			}
		}
	}
	// Resolution diff over the current set (cheap path math, no AST walks).
	for _, k := range s.order {
		st := s.files[k]
		if st.isDecl {
			continue
		}
		fresh := s.resolveTargets(st.sf)
		if !equalTargets(fresh, st.targets) {
			dirty[k] = true
		}
		st.targets = fresh
	}
	// Content-changed non-decls join (parseInto reports them; the emit
	// flag stays reserved for emission decisions).
	for _, k := range contentChanged {
		if st, ok := s.files[k]; ok && !st.isDecl {
			dirty[k] = true
		}
	}
	dirty = s.expandDirty(dirty)
	s.recheckDirty(dirty)
	s.assembleDiags()
	if !doEmit {
		s.initialized = true
		return nil, nil
	}
	if len(s.files) == 0 {
		s.initialized = true
		return nil, fmt.Errorf("no .tl files found")
	}
	emitted, err := s.emitChanged()
	s.initialized = true
	return emitted, err
}

// parseInto reads + parses fps (parallel), refreshing states. Tracked files
// whose bytes are identical keep everything (parse/expand run zero times).
// Unreadable paths get TL4001 entries (same shape as Load) and no state.
// Returns keys whose bytes are new or changed (check-dirty signal; the emit
// flag is managed separately so failed-emit retries don't force rechecks).
func (s *Session) parseInto(fps []string) (changed []string) {
	type parsed struct {
		data   []byte
		file   *ast.File
		pdiags []diag.Diagnostic
		rerr   error
	}
	results := make([]parsed, len(fps))
	parallelDo(len(fps), func(i int) {
		data, f, pds, rerr := parseOne(s.cfg, fps[i])
		results[i] = parsed{data: data, file: f, pdiags: pds, rerr: rerr}
	})
	for i, fp := range fps {
		r := results[i]
		key := normKey(fp)
		if r.rerr != nil {
			delete(s.files, key)
			s.unread[key] = readDiag(fp, r.rerr)
			continue
		}
		if st, ok := s.files[key]; ok && string(r.data) == st.sf.Content {
			delete(s.unread, key)
			continue // touch-noop: keep parse, check, emit state
		}
		delete(s.unread, key)
		sf := &SourceFile{Path: fp, Content: string(r.data), File: r.file, IsDecl: strings.HasSuffix(fp, ".d.tl")}
		s.files[key] = &fileState{
			sf:         sf,
			parseDiags: r.pdiags,
			targets:    map[string]string{},
			emitDirty:  true,
			isDecl:     sf.IsDecl,
		}
		s.files[key].reqs = checker.RequiredModules(sf.File)
		changed = append(changed, key)
	}
	return changed
}

// ambientInputsChanged reports whether the included ambient decl set
// changed (membership or bytes) since the last snapshot.
func (s *Session) ambientInputsChanged() bool {
	allow := s.cfg.AmbientTypes()
	count := 0
	for _, k := range s.order {
		st := s.files[k]
		if !st.isDecl || st.sf.File == nil {
			continue
		}
		if !config.IsAmbientIncluded(st.sf.Path, allow) {
			continue
		}
		count++
		if s.ambientContent[k] != st.sf.Content {
			return true
		}
	}
	return count != len(s.ambientContent)
}

// resolveTargets maps a file's dotted requires to target keys, using the
// parse-time cached require lists (never a walk over expanded ASTs).
func (s *Session) resolveTargets(sf *SourceFile) map[string]string {
	files := s.fileList()
	out := map[string]string{}
	for _, dotted := range s.cachedRequires(sf) {
		out[dotted] = targetKey(resolveRequireIn(files, s.shorts, s.cfg.Dir, sf, dotted))
	}
	return out
}

func equalTargets(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

// uniqueStrings compacts a sorted slice in place, returning the deduped
// length (same semantics as slices.Compact, stdlib-free).
func uniqueStrings(s []string) int {
	if len(s) < 2 {
		return len(s)
	}
	w := 1
	for r := 1; r < len(s); r++ {
		if s[r] != s[w-1] {
			s[w] = s[r]
			w++
		}
	}
	return w
}

// expandDirty grows the dirty set to full SCCs (deterministic
// any-degradation inside cycles) plus the reverse require closure.
func (s *Session) expandDirty(dirty map[string]bool) map[string]bool {
	// Forward edges among non-decl files (decl targets carry no type flow:
	// require edges to .d.tl always degrade to any).
	var nodes []string
	edges := map[string][]string{}
	for _, k := range s.order {
		st := s.files[k]
		if st.isDecl {
			continue
		}
		nodes = append(nodes, k)
		for _, tk := range st.targets {
			if len(tk) < 2 || tk[0] != 'F' {
				continue
			}
			// targetKey format "F:"+clean(path); keys are already clean.
			if _, ok := s.files[tk[2:]]; ok {
				edges[k] = append(edges[k], tk[2:])
			}
		}
	}
	// Sort + dedupe adjacency once so tarjanSCC visits deterministically
	// without per-node copies.
	for n, tos := range edges {
		sort.Strings(tos)
		edges[n] = tos[:uniqueStrings(tos)]
	}
	for _, scc := range tarjanSCC(nodes, edges) {
		hit := false
		for _, m := range scc {
			if dirty[m] {
				hit = true
				break
			}
		}
		if hit {
			for _, m := range scc {
				dirty[m] = true
			}
		}
	}
	// Reverse closure (index-based queue: no reslicing).
	rev := map[string][]string{}
	for from, tos := range edges {
		for _, to := range tos {
			rev[to] = append(rev[to], from)
		}
	}
	queue := make([]string, 0, len(dirty))
	for m := range dirty {
		queue = append(queue, m)
	}
	for head := 0; head < len(queue); head++ {
		cur := queue[head]
		for _, dep := range rev[cur] {
			if !dirty[dep] {
				dirty[dep] = true
				queue = append(queue, dep)
			}
		}
	}
	return dirty
}

// tarjanSCC returns strongly connected components (each sorted) ordered by
// minimum member, over nodes with adjacency edges. Recursive like the
// checker's own DFS (same depth class as existing full-run traversal).
// Callers sort + dedupe adjacency once (see expandDirty); this function
// never copies edge lists, only reads them.
func tarjanSCC(nodes []string, edges map[string][]string) [][]string {
	const (
		white = 0
		gray  = 1
		black = 2
	)
	color := map[string]int{}
	disc := map[string]int{}
	low := map[string]int{}
	onStack := map[string]bool{}
	var stack []string
	var out [][]string
	clock := 0
	sorted := append([]string(nil), nodes...)
	sort.Strings(sorted)
	var visit func(v string)
	visit = func(v string) {
		color[v] = gray
		disc[v] = clock
		low[v] = clock
		clock++
		stack = append(stack, v)
		onStack[v] = true
		// Edges arrive sorted + deduped from expandDirty; index the map
		// directly with no per-node copy.
		for _, to := range edges[v] {
			if color[to] == white {
				visit(to)
				if low[to] < low[v] {
					low[v] = low[to]
				}
			} else if onStack[to] && disc[to] < low[v] {
				low[v] = disc[to]
			}
		}
		if low[v] == disc[v] {
			var comp []string
			for {
				w := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				onStack[w] = false
				comp = append(comp, w)
				if w == v {
					break
				}
			}
			sort.Strings(comp)
			out = append(out, comp)
		}
		color[v] = black
	}
	for _, v := range sorted {
		if color[v] == white {
			visit(v)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i][0] < out[j][0] })
	return out
}

// recheckDirty runs the shared DFS driver over dirty non-decl files,
// seeding cached results for everything else (identical visit semantics to
// full runs restricted to the dirty subgraph: complete SCCs + DAG order).
func (s *Session) recheckDirty(dirty map[string]bool) {
	files := s.fileList()
	byPtr := map[*SourceFile]string{}
	for _, k := range s.order {
		byPtr[s.files[k].sf] = k
	}
	d := &checkDriver{
		files:    files,
		shorts:   s.shorts,
		cfgDir:   s.cfg.Dir,
		ambient:  s.ambient,
		opt:      s.checkerOpt(),
		requires: s.cachedRequires,
		state:    map[*SourceFile]int{},
		modTy:    map[*SourceFile]types.Type{},
		onCheck: func(sf *SourceFile, ds []diag.Diagnostic, mt types.Type) {
			if k, ok := byPtr[sf]; ok {
				if st, ok := s.files[k]; ok {
					st.checkDiags = ds
					st.modTy = mt
				}
				s.lastChecked = append(s.lastChecked, k)
			}
		},
	}
	var roots []*SourceFile
	for _, k := range s.order {
		st := s.files[k]
		if st.isDecl {
			d.seedDone(st.sf, types.Any)
			continue
		}
		if dirty[k] {
			roots = append(roots, st.sf)
			continue
		}
		d.seedDone(st.sf, st.modTy)
	}
	s.lastChecked = nil
	for _, sf := range roots {
		d.checkOne(sf)
	}
	sort.Strings(s.lastChecked)
}

// emitChanged renders outputs whose bytes changed (or prelude flipped) and
// always refreshes the asset passthrough (dedup keeps it cheap). Returns
// the full up-to-date output list (like Emit) for stable status messages.
func (s *Session) emitChanged() ([]string, error) {
	files := s.fileList()
	prog := &Program{Cfg: s.cfg, Files: files}
	prelude := NeedsModPath(prog)
	if prelude != s.prelude {
		for _, k := range s.order {
			if !s.files[k].isDecl {
				s.files[k].emitDirty = true
			}
		}
	}
	opt := emitter.Options{Target: s.cfg.Target(), TreeShake: s.cfg.CompilerOptions.TreeShake}
	jobs := collectEmitJobs(s.cfg, files)
	emittedSet := map[string]bool{}
	for _, jb := range jobs {
		emittedSet[filepath.Clean(jb.dst)] = true
	}
	byDst := map[string]string{}
	// OutputForFile is pure in (cfg, path): compute once per file instead
	// of twice (mapping + dirty-clearing passes below).
	dstOf := map[string]string{}
	for _, k := range s.order {
		st := s.files[k]
		if st.isDecl {
			continue
		}
		if dst, ok := OutputForFile(s.cfg, st.sf.Path); ok {
			byDst[filepath.Clean(dst)] = k
			dstOf[k] = dst
		}
	}
	var todo []emitJob
	for _, jb := range jobs {
		// byDst covers exactly the non-decl states; the guard below is
		// defensive (a long-running watcher must never panic on mapping
		// skew between the two pure passes).
		k, ok := byDst[filepath.Clean(jb.dst)]
		if !ok {
			continue
		}
		if st, ok := s.files[k]; ok && st.emitDirty {
			todo = append(todo, jb)
		}
	}
	computeEmitJobs(todo, opt, prelude)
	emitted, err := writeEmitJobs(todo, nil, emittedSet)
	if err != nil {
		return emitted, err
	}
	// Clearing per successful write is approximated by clearing what the
	// ordered writer reports (prefix of todo on error, all on success).
	written := map[string]bool{}
	for _, dst := range emitted {
		written[filepath.Clean(dst)] = true
	}
	for _, k := range s.order {
		st := s.files[k]
		if st.isDecl {
			continue
		}
		if dst, ok := dstOf[k]; ok && written[filepath.Clean(dst)] {
			st.emitDirty = false
		}
	}
	// Commit the .tl-side state before assets: an asset failure retries
	// only assets next cycle, not the whole render.
	s.prelude = prelude
	assets, err := prog.emitNativeAssets(emittedSet)
	if err != nil {
		return emitted, err
	}
	// Report every up-to-date output (like Emit), not just this cycle's
	// writes, so status counts stay stable across incremental cycles.
	all := make([]string, 0, len(jobs)+len(assets))
	for _, jb := range jobs {
		all = append(all, jb.dst)
	}
	return append(all, assets...), nil
}
