package checker

import (
	"github.com/typelua/typelua/internal/ast"
	"github.com/typelua/typelua/internal/diag"
	"github.com/typelua/typelua/internal/expand"
	"github.com/typelua/typelua/internal/token"
	"github.com/typelua/typelua/internal/types"
)

// Core checker state: options, symbols, scopes, entries, and the CheckFile drivers.

// Options controls strictness (mirrors tsconfig strict flags).
type Options struct {
	Strict          bool
	StrictNilChecks bool
	NoImplicitAny   bool
	// Target selects version-gated builtins ("5.4" default, "5.1" hides
	// 5.4-only APIs like coroutine.close). Empty means latest.
	Target string
	// RequireTypes maps dotted require paths ("a.b.c") to module types
	// resolved by the driver (nil = every require is any, classic mode).
	RequireTypes map[string]types.Type
	// RelativeImport validates an `import ... from "./path"` against the
	// project layout and returns the runtime module name the emitter should
	// require. A non-empty message reports the spec (compiler fills this in;
	// nil means single-file mode, where relative imports are left unchecked).
	RelativeImport func(file, spec string) (string, string)
	// Ambient is an optional parent scope holding builtins + global .d.tl
	// declarations (like tsconfig types/typeRoots). When nil, the checker
	// defines builtins in a fresh top scope (classic single-file mode).
	// When set, the file is checked in a child scope so locals shadow
	// ambient without DuplicateType errors and rename stays detached.
	Ambient *Scope
}

func DefaultOptions() Options {
	return Options{Strict: true, StrictNilChecks: true, NoImplicitAny: false}
}

func (o Options) toTypes() types.Options {
	strictNil := o.StrictNilChecks
	if o.Strict {
		strictNil = true
	}
	return types.Options{StrictNilChecks: strictNil}
}

// Symbol kinds.
type symKind int

const (
	symVar symKind = iota
	symFunc
	symParam
)

type Symbol struct {
	Name      string
	Kind      symKind
	Type      types.Type
	Span      token.Span
	IsConst   bool
	IsBuiltin bool
	// SameAs links narrowing shadows (and similar ephemeral copies) back
	// to the definition symbol for references/rename.
	SameAs *Symbol
	// For functions with generics.
	GenericNames []string
	FuncDecl     *ast.FuncLit
}

type typeEntry struct {
	name         string
	span         token.Span // declaration name span (for goto-definition)
	params       []*ast.GenericParam
	alias        *ast.TypeAliasDecl
	iface        *ast.InterfaceDecl
	enumDecl     *ast.EnumDecl
	enumType     *types.Enum
	classDecl    *ast.ClassDecl
	classDef     *types.ClassDef
	classBuild   *classBuild
	classShell   *types.Class
	classPatches []func()
	resolving    bool
	cached       types.Type // for non-generic
	cacheInst    map[string]types.Type
	// inflight tracks in-progress builds per instantiation key for
	// recursive declarations: a cycle re-entry shares a shell object
	// (nil = building, non-nil = shell handed out), filled on completion.
	inflight map[string]*types.Object
}

// TypeEntry is the exported alias of typeEntry for language servers:
// occurrences compare by pointer without needing construction.
type TypeEntry = typeEntry

// classBuild is the checked definition form of a class (generic placeholders
// intact so call-site inference keeps working).
type classBuild struct {
	inst *types.Class
	val  *types.ClassValue
	sigs map[*ast.ClassMember]*classMemberSig
}

// classCtx tracks the enclosing class while checking method bodies.
type classCtx struct {
	def           *types.ClassDef
	name          string
	inst          *types.Class // definition-form instance (placeholders)
	base          *types.Class
	baseVal       *types.ClassValue
	inStatic      bool
	inConstructor bool
	retDepth      int // len(retStack) at method entry; super only applies at this depth
}

type Scope struct {
	parent *Scope
	vars   map[string]*Symbol
	types  map[string]*typeEntry
}

func newScope(parent *Scope) *Scope {
	return &Scope{parent: parent, vars: map[string]*Symbol{}, types: map[string]*typeEntry{}}
}

func (s *Scope) lookupVar(name string) *Symbol {
	for sc := s; sc != nil; sc = sc.parent {
		if v, ok := sc.vars[name]; ok {
			return v
		}
	}
	return nil
}

func (s *Scope) lookupType(name string) *typeEntry {
	for sc := s; sc != nil; sc = sc.parent {
		if e, ok := sc.types[name]; ok {
			return e
		}
	}
	return nil
}

// Checker holds per-program state (single file for now; multi-file via Program).
type Checker struct {
	opt    Options
	diags  []diag.Diagnostic
	scopes []*Scope
	file   string
	// Narrowing overrides per scope depth? We implement via shadow symbols: narrowedScopes stack.
	// Return checking.
	retStack [][]types.Type // expected returns per function (nil = infer/any)
	// Labels per function.
	labelStack []map[string]token.Span
	gotoList   [][]gotoRef
	// Generic params in scope (type-level).
	tparamStack []map[string]*types.GenericParam
	// Vararg type in current function.
	varargStack []types.Type
	// Inferred returns for unannotated functions (collect).
	inferStack [][]types.Type
	loopDepth  int
	// Instantiation depth for generic alias/interface builds. Self-feeding
	// expansions (`L<L<T>>`, `L<Wrap<T>>`) grow the key forever, so cap it.
	// (Non-generic builds terminate via inflight shells.)
	typeDepth int
	// depthReported suppresses repeat overflow errors per entry.
	depthReported map[*typeEntry]bool
	// Enclosing classes for self/super/visibility.
	classStack []*classCtx
	// asyncStack tracks `async` function nesting (FuncLit/ClassMember
	// IsAsync): `await`/`select` are valid when the stack is empty (the
	// top-level chunk = main task) or its top is true.
	asyncStack []bool
	// spawnDepth counts enclosing `spawn` calls: spawning an async
	// function from sync code is fine (the body becomes a new task), so
	// the TL3805 async-call check is suspended inside.
	spawnDepth int
	// >0 while resolving an extends clause (where `infer` may appear).
	extendsDepth int
	// Index collects position information for LSP when non-nil.
	Index *SymIndex
}

// SymUse is one identifier occurrence: a use, or (IsDef) its definition.
type SymUse struct {
	Span  token.Span
	Sym   *Symbol
	Root  *Symbol
	IsDef bool
	// Entry is set for type-name references (aliases, interfaces...).
	Entry *TypeEntry
	// Member is set for member references (class/interface/enum members).
	Member *MemberRef
}

// MemberRef identifies one member occurrence of a nominal owner
// (class, interface, or enum). Owner compares by pointer within one
// index run; rename-safe since renaming never replaces the entry.
// StructOwner covers anonymous object literals: the root variable holding
// the literal (same-variable direct flows only; aliases untracked).
// Exactly one of Owner/StructOwner is set.
type MemberRef struct {
	Owner       *TypeEntry
	OwnerName   string // e.g. "Dog", or the variable name for structural
	Name        string // member name
	Kind        string // "method", "field", or "member" (kind is "" at use sites)
	StructOwner *Symbol
}

// ScopeSpan pairs a live scope with the block span that owns it.
type ScopeSpan struct {
	Sc   *Scope
	Span token.Span
}

// SymIndex is the checker's position database for language servers.
// Recording is best-effort and never affects diagnostics.
type SymIndex struct {
	Uses   []SymUse
	Scopes []ScopeSpan
}

// symRoot follows SameAs links to the definition symbol.
func symRoot(s *Symbol) *Symbol {
	for s != nil && s.SameAs != nil {
		s = s.SameAs
	}
	return s
}

type gotoRef struct {
	name string
	span token.Span
}

func New(opt Options) *Checker {
	return &Checker{opt: opt}
}

func (c *Checker) Diags() []diag.Diagnostic { return c.diags }

func (c *Checker) errorf(code diag.Code, sp token.Span, format string, args ...any) {
	c.diags = append(c.diags, diag.Errorf(code, sp, format, args...))
}

func (c *Checker) cur() *Scope { return c.scopes[len(c.scopes)-1] }
func (c *Checker) push()       { c.scopes = append(c.scopes, newScope(c.cur())) }
func (c *Checker) pop()        { c.scopes = c.scopes[:len(c.scopes)-1] }

// bindVar installs a binding, linking redefinitions so reference queries
// (references/rename) unify the whole chain. Builtin shadows stay detached.
func (c *Checker) bindVar(name string, sym *Symbol) *Symbol {
	if old, ok := c.cur().vars[name]; ok && !old.IsBuiltin {
		sym.SameAs = old
	}
	c.cur().vars[name] = sym
	return sym
}

// CheckFile type-checks a parsed file. Returns diagnostics.
func CheckFile(f *ast.File, opt Options) []diag.Diagnostic {
	diags, _ := CheckFileIndexed(f, opt)
	return diags
}

// CheckFileIndexed type-checks and additionally returns the position index
// used by language servers. The index is always populated.
func CheckFileIndexed(f *ast.File, opt Options) ([]diag.Diagnostic, *SymIndex) {
	diags, idx, _ := checkFileModule(f, opt)
	return diags, idx
}

// CheckFileModule checks like CheckFileIndexed and additionally reports
// the module's own return type (a top-level `return M` identifier looked
// up in the surviving top scope, else Any).
func CheckFileModule(f *ast.File, opt Options) ([]diag.Diagnostic, *SymIndex, types.Type) {
	return checkFileModule(f, opt)
}

func checkFileModule(f *ast.File, opt Options) ([]diag.Diagnostic, *SymIndex, types.Type) {
	c := New(opt)
	c.file = f.Name
	// Macros expand before checking (definitions erase; idempotent).
	c.diags = append(c.diags, expand.ExpandFile(f)...)
	if opt.Ambient != nil {
		c.scopes = []*Scope{newScope(opt.Ambient)}
	} else {
		c.scopes = []*Scope{newScope(nil)}
		c.defineBuiltins(c.cur())
	}
	c.Index = &SymIndex{}
	if f.Block != nil {
		c.recordScope(c.cur(), f.Block.Span())
	}
	c.checkBlock(f.Block)
	return c.diags, c.Index, c.moduleType(f)
}

// moduleType reports the type of the file's top-level `return M`
// identifier (looked up in the surviving top scope), else Any.
func (c *Checker) moduleType(f *ast.File) types.Type {
	if f.Block == nil || f.Block.Ret == nil || len(f.Block.Ret.Exprs) == 0 {
		return types.Any
	}
	if id, ok := f.Block.Ret.Exprs[0].(*ast.Ident); ok {
		if s := c.cur().lookupVar(id.Name); s != nil && s.Type != nil {
			return s.Type
		}
	}
	return types.Any
}
