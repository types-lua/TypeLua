package checker

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/typelua/typelua/internal/ast"
	"github.com/typelua/typelua/internal/diag"
	"github.com/typelua/typelua/internal/expand"
	"github.com/typelua/typelua/internal/token"
	"github.com/typelua/typelua/internal/types"
)

// Options controls strictness (mirrors tsconfig strict flags).
type Options struct {
	Strict          bool
	StrictNilChecks bool
	NoImplicitAny   bool
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
	// Enclosing classes for self/super/visibility.
	classStack []*classCtx
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
type MemberRef struct {
	Owner     *TypeEntry
	OwnerName string // e.g. "Dog"
	Name      string // member name
	Kind      string // "method", "field", or "member" (kind is "" at use sites)
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

func (c *Checker) recordUse(span token.Span, s *Symbol) {
	if c.Index == nil || s == nil {
		return
	}
	c.Index.Uses = append(c.Index.Uses, SymUse{Span: span, Sym: s, Root: symRoot(s)})
}

func (c *Checker) recordDef(span token.Span, s *Symbol) {
	if c.Index == nil || s == nil {
		return
	}
	c.Index.Uses = append(c.Index.Uses, SymUse{Span: span, Sym: s, Root: symRoot(s), IsDef: true})
}

func (c *Checker) recordTypeUse(span token.Span, e *typeEntry) {
	if c.Index == nil || e == nil {
		return
	}
	c.Index.Uses = append(c.Index.Uses, SymUse{Span: span, Entry: e})
}

// recordDefEntry records a value definition that twins a type declaration
// (class/enum names), so rename can cover both namespaces at once.
func (c *Checker) recordDefEntry(span token.Span, s *Symbol, e *typeEntry) {
	if c.Index == nil || s == nil {
		return
	}
	c.Index.Uses = append(c.Index.Uses, SymUse{Span: span, Sym: s, Root: symRoot(s), IsDef: true, Entry: e})
}

// recordMemberDef records a member definition of a nominal owner.
func (c *Checker) recordMemberDef(span token.Span, e *typeEntry, name, kind string) {
	if c.Index == nil || e == nil {
		return
	}
	c.Index.Uses = append(c.Index.Uses, SymUse{Span: span, IsDef: true,
		Member: &MemberRef{Owner: e, OwnerName: e.name, Name: name, Kind: kind}})
}

// recordMemberUse records a member access on a resolved owner type.
// Unknown owners (structural objects, maps, any) are skipped.
func (c *Checker) recordMemberUse(span token.Span, objT types.Type, name string) {
	if c.Index == nil {
		return
	}
	// `new` on a class value is synthetic constructor sugar (no source
	// definition) unless the class actually declares a static `new`.
	if name == "new" {
		if cv, ok := objT.(*types.ClassValue); ok {
			if _, has := cv.Statics["new"]; !has {
				return
			}
		}
	}
	e := c.ownerEntry(objT)
	if e == nil {
		return
	}
	c.Index.Uses = append(c.Index.Uses, SymUse{Span: span,
		Member: &MemberRef{Owner: e, OwnerName: e.name, Name: name}})
}

// ownerEntry resolves a value type back to its nominal type declaration,
// if any (class instance/value, named object such as an interface or enum
// table, enum). Scope-aware: the name resolves at the use site.
func (c *Checker) ownerEntry(t types.Type) *typeEntry {
	var name string
	switch x := t.(type) {
	case *types.Class:
		name = x.Name
	case *types.ClassValue:
		if x.Class != nil {
			name = x.Class.Name
		}
	case *types.Object:
		name = x.Name
	case *types.Enum:
		name = x.Name
	default:
		return nil
	}
	if name == "" {
		return nil
	}
	return c.cur().lookupType(name)
}

func (c *Checker) recordScope(sc *Scope, span token.Span) {
	if c.Index == nil {
		return
	}
	c.Index.Scopes = append(c.Index.Scopes, ScopeSpan{Sc: sc, Span: span})
}

type gotoRef struct {
	name string
	span token.Span
}

func New(opt Options) *Checker {
	return &Checker{opt: opt}
}

func (c *Checker) Diags() []diag.Diagnostic { return c.diags }

func (c *Checker) errorf(code string, sp token.Span, format string, args ...any) {
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
	c := New(opt)
	c.file = f.Name
	// Macros expand before checking (definitions erase; idempotent).
	c.diags = append(c.diags, expand.ExpandFile(f)...)
	c.scopes = []*Scope{newScope(nil)}
	c.defineBuiltins(c.cur())
	c.Index = &SymIndex{}
	if f.Block != nil {
		c.recordScope(c.cur(), f.Block.Span())
	}
	c.checkBlock(f.Block)
	return c.diags, c.Index
}

// ---------- builtins (permissive any-based to avoid false positives) ----------

func (c *Checker) defineBuiltins(sc *Scope) {
	anyT := types.Any
	mkFunc := func(params ...types.Type) types.Type {
		var ps []types.FuncParam
		for i, p := range params {
			ps = append(ps, types.FuncParam{Name: fmt.Sprintf("arg%d", i), Type: p})
		}
		return &types.Func{Params: ps, HasVarArg: true, VarArg: anyT, Returns: []types.Type{anyT}}
	}
	_ = mkFunc
	// Core functions.
	sc.vars["print"] = &Symbol{Name: "print", Kind: symFunc, Type: &types.Func{HasVarArg: true, VarArg: anyT, Returns: nil}}
	sc.vars["pairs"] = &Symbol{Name: "pairs", Kind: symFunc, Type: &types.Func{Params: []types.FuncParam{{Name: "t", Type: anyT}}, HasVarArg: false, Returns: []types.Type{anyT}}}
	sc.vars["ipairs"] = &Symbol{Name: "ipairs", Kind: symFunc, Type: &types.Func{Params: []types.FuncParam{{Name: "t", Type: anyT}}, Returns: []types.Type{anyT}}}
	sc.vars["tostring"] = &Symbol{Name: "tostring", Kind: symFunc, Type: &types.Func{Params: []types.FuncParam{{Name: "v", Type: anyT}}, Returns: []types.Type{types.String}}}
	sc.vars["tonumber"] = &Symbol{Name: "tonumber", Kind: symFunc, Type: &types.Func{Params: []types.FuncParam{{Name: "v", Type: anyT}}, HasVarArg: true, VarArg: anyT, Returns: []types.Type{types.NewUnion([]types.Type{types.Number, types.Nil})}}}
	sc.vars["type"] = &Symbol{Name: "type", Kind: symFunc, Type: &types.Func{Params: []types.FuncParam{{Name: "v", Type: anyT}}, Returns: []types.Type{types.String}}}
	sc.vars["require"] = &Symbol{Name: "require", Kind: symFunc, Type: &types.Func{Params: []types.FuncParam{{Name: "m", Type: types.String}}, Returns: []types.Type{anyT}}}
	sc.vars["assert"] = &Symbol{Name: "assert", Kind: symFunc, Type: &types.Func{Params: []types.FuncParam{{Name: "v", Type: anyT}}, HasVarArg: true, VarArg: anyT, Returns: []types.Type{anyT}}}
	sc.vars["error"] = &Symbol{Name: "error", Kind: symFunc, Type: &types.Func{Params: []types.FuncParam{{Name: "msg", Type: anyT}}, HasVarArg: true, VarArg: anyT, Returns: []types.Type{types.Never}}}
	sc.vars["pcall"] = &Symbol{Name: "pcall", Kind: symFunc, Type: &types.Func{HasVarArg: true, VarArg: anyT, Returns: []types.Type{anyT}}}
	sc.vars["xpcall"] = &Symbol{Name: "xpcall", Kind: symFunc, Type: &types.Func{HasVarArg: true, VarArg: anyT, Returns: []types.Type{anyT}}}
	sc.vars["select"] = &Symbol{Name: "select", Kind: symFunc, Type: &types.Func{HasVarArg: true, VarArg: anyT, Returns: []types.Type{anyT}}}
	sc.vars["next"] = &Symbol{Name: "next", Kind: symFunc, Type: &types.Func{HasVarArg: true, VarArg: anyT, Returns: []types.Type{anyT}}}
	sc.vars["setmetatable"] = &Symbol{Name: "setmetatable", Kind: symFunc, Type: &types.Func{Params: []types.FuncParam{{Name: "t", Type: anyT}, {Name: "m", Type: anyT}}, Returns: []types.Type{anyT}}}
	sc.vars["getmetatable"] = &Symbol{Name: "getmetatable", Kind: symFunc, Type: &types.Func{Params: []types.FuncParam{{Name: "v", Type: anyT}}, Returns: []types.Type{anyT}}}
	sc.vars["rawget"] = &Symbol{Name: "rawget", Kind: symFunc, Type: &types.Func{Params: []types.FuncParam{{Name: "t", Type: anyT}, {Name: "k", Type: anyT}}, Returns: []types.Type{anyT}}}
	sc.vars["rawset"] = &Symbol{Name: "rawset", Kind: symFunc, Type: &types.Func{HasVarArg: true, VarArg: anyT, Returns: []types.Type{anyT}}}
	sc.vars["rawequal"] = &Symbol{Name: "rawequal", Kind: symFunc, Type: &types.Func{Params: []types.FuncParam{{Name: "a", Type: anyT}, {Name: "b", Type: anyT}}, Returns: []types.Type{types.Boolean}}}
	sc.vars["rawlen"] = &Symbol{Name: "rawlen", Kind: symFunc, Type: &types.Func{Params: []types.FuncParam{{Name: "v", Type: anyT}}, Returns: []types.Type{types.Integer}}}
	sc.vars["collectgarbage"] = &Symbol{Name: "collectgarbage", Kind: symFunc, Type: &types.Func{HasVarArg: true, VarArg: anyT, Returns: []types.Type{anyT}}}
	sc.vars["dofile"] = &Symbol{Name: "dofile", Kind: symFunc, Type: &types.Func{HasVarArg: true, VarArg: anyT, Returns: []types.Type{anyT}}}
	sc.vars["load"] = &Symbol{Name: "load", Kind: symFunc, Type: &types.Func{HasVarArg: true, VarArg: anyT, Returns: []types.Type{anyT}}}
	sc.vars["loadfile"] = &Symbol{Name: "loadfile", Kind: symFunc, Type: &types.Func{HasVarArg: true, VarArg: anyT, Returns: []types.Type{anyT}}}
	sc.vars["self"] = nil // placeholder; defined per-method.
	delete(sc.vars, "self")
	// Modules as any.
	for _, m := range []string{"table", "string", "math", "io", "os", "coroutine", "debug", "package", "utf8", "bit32"} {
		sc.vars[m] = &Symbol{Name: m, Kind: symVar, Type: types.Any}
	}
	// unpack compat.
	sc.vars["unpack"] = &Symbol{Name: "unpack", Kind: symFunc, Type: &types.Func{HasVarArg: true, VarArg: anyT, Returns: []types.Type{anyT}}}
	for _, s := range sc.vars {
		if s != nil {
			s.IsBuiltin = true
		}
	}
}

// ---------- blocks ----------

// recordTypeDef records a type declaration name.
func (c *Checker) recordTypeDef(span token.Span, e *typeEntry) {
	if c.Index == nil || e == nil {
		return
	}
	c.Index.Uses = append(c.Index.Uses, SymUse{Span: span, IsDef: true, Entry: e})
}

func (c *Checker) collectTypes(b *ast.Block, sc *Scope) {
	if b == nil {
		return
	}
	for _, st := range b.Stmts {
		switch n := st.(type) {
		case *ast.TypeAliasDecl:
			if _, ok := sc.types[n.Name.Name]; !ok {
				e := &typeEntry{name: n.Name.Name, params: n.Generics, alias: n, span: n.Name.Span(), cacheInst: map[string]types.Type{}}
				sc.types[n.Name.Name] = e
				c.recordTypeDef(n.Name.Span(), e)
			} else {
				c.errorf("TL3001", n.Span(), "duplicate type '%s'", n.Name.Name)
			}
		case *ast.InterfaceDecl:
			if _, ok := sc.types[n.Name.Name]; !ok {
				e := &typeEntry{name: n.Name.Name, params: n.Generics, iface: n, span: n.Name.Span(), cacheInst: map[string]types.Type{}}
				sc.types[n.Name.Name] = e
				c.recordTypeDef(n.Name.Span(), e)
			} else {
				c.errorf("TL3001", n.Span(), "duplicate type '%s'", n.Name.Name)
			}
		case *ast.EnumDecl:
			if _, ok := sc.types[n.Name.Name]; !ok {
				e := &typeEntry{name: n.Name.Name, enumDecl: n, span: n.Name.Span(), cacheInst: map[string]types.Type{}}
				sc.types[n.Name.Name] = e
				c.recordTypeDef(n.Name.Span(), e)
			} else {
				c.errorf("TL3001", n.Span(), "duplicate type '%s'", n.Name.Name)
			}
		case *ast.ClassDecl:
			if _, ok := sc.types[n.Name.Name]; !ok {
				e := &typeEntry{name: n.Name.Name, params: n.Generics, classDecl: n, span: n.Name.Span(), cacheInst: map[string]types.Type{}}
				sc.types[n.Name.Name] = e
				c.recordTypeDef(n.Name.Span(), e)
			} else {
				c.errorf("TL3001", n.Span(), "duplicate type '%s'", n.Name.Name)
			}
		}
	}
}

func (c *Checker) checkBlock(b *ast.Block) {
	if b == nil {
		return
	}
	c.collectTypes(b, c.cur())
	c.recordScope(c.cur(), b.Span())
	for _, st := range b.Stmts {
		c.checkStmt(st)
	}
	if b.Ret != nil {
		c.checkReturn(b.Ret)
	}
}

// ---------- statements ----------

func (c *Checker) checkStmt(st ast.Stmt) {
	if st == nil {
		// Recovery artifact (parser already reported); never crash.
		return
	}
	switch n := st.(type) {
	case *ast.EmptyStmt:
	case *ast.ExprStmt:
		c.checkExpr(n.E)
	case *ast.LocalAssign:
		c.checkLocalAssign(n)
	case *ast.LocalFuncDef:
		c.checkLocalFunc(n)
	case *ast.FuncDefStmt:
		c.checkGlobalFunc(n)
	case *ast.AssignStmt:
		c.checkAssign(n)
	case *ast.DoBlock:
		c.push()
		c.checkBlock(n.Block)
		c.pop()
	case *ast.IfStmt:
		c.checkIf(n)
	case *ast.WhileStmt:
		condT := c.checkExpr(n.Cond)
		_ = condT
		c.push()
		c.applyNarrowing(n.Cond, true)
		c.checkBlock(n.Block)
		c.pop()
		c.loopDepth++
		_ = c.loopDepth
		c.loopDepth--
	case *ast.RepeatStmt:
		c.push()
		c.checkBlock(n.Block)
		condT := c.checkExpr(n.Cond)
		_ = condT
		c.pop()
	case *ast.NumericFor:
		c.checkNumericFor(n)
	case *ast.GenericFor:
		c.checkGenericFor(n)
	case *ast.BreakStmt:
		// TODO: verify inside loop; we track loosely.
	case *ast.GotoStmt:
		if len(c.gotoList) > 0 {
			c.gotoList[len(c.gotoList)-1] = append(c.gotoList[len(c.gotoList)-1], gotoRef{name: n.Label.Name, span: n.Span()})
		}
	case *ast.LabelStmt:
		if len(c.labelStack) > 0 {
			top := c.labelStack[len(c.labelStack)-1]
			if _, ok := top[n.Name.Name]; ok {
				c.errorf("TL3002", n.Span(), "duplicate label '%s'", n.Name.Name)
			} else {
				top[n.Name.Name] = n.Span()
			}
		}
	case *ast.TypeAliasDecl:
		c.checkTypeAlias(n)
	case *ast.InterfaceDecl:
		c.checkInterface(n)
	case *ast.EnumDecl:
		c.checkEnum(n)
	case *ast.ClassDecl:
		c.checkClass(n)
	case *ast.MacroDef:
		// Unreachable via CheckFile (expansion runs first); defensive.
		c.errorf("TL3700", st.Span(), "internal error: unexpanded macro '%s'", n.Name.Name)
	default:
		c.errorf("TL3000", st.Span(), "unsupported statement %T", st)
	}
}

func (c *Checker) checkLocalAssign(n *ast.LocalAssign) {
	var declTypes []types.Type
	for _, tn := range n.Types {
		if tn == nil {
			declTypes = append(declTypes, nil)
		} else {
			declTypes = append(declTypes, c.resolveType(tn))
		}
	}
	var valTypes []types.Type
	if len(n.Values) > 0 {
		valTypes = c.checkExprListMulti(n.Values)
	}
	for i, name := range n.Names {
		var want types.Type
		if i < len(declTypes) {
			want = declTypes[i]
		}
		var got types.Type
		if i < len(valTypes) {
			got = valTypes[i]
		} else if len(n.Values) > 0 {
			got = types.Nil // missing trailing values are nil
		}
		var final types.Type
		if want != nil && got != nil {
			if !types.IsAssignable(got, want, c.opt.toTypes()) {
				c.errorf("TL3010", name.Span(), "type '%s' is not assignable to type '%s'", got.String(), want.String())
			}
			final = want
		} else if want != nil {
			final = want
		} else if got != nil {
			// Unannotated inference: list literals become growable arrays (not fixed tuples),
			// and literal members are widened to avoid over-narrowing.
			if i < len(n.Values) {
				if lit, ok := n.Values[i].(*ast.TableLit); ok && isPureList(lit) {
					if tup, ok := got.(*types.Tuple); ok {
						final = &types.Array{Elem: widenInferred(types.NewUnion(tup.Elems))}
					} else {
						final = widenInferred(got)
					}
				} else {
					final = widenInferred(got)
				}
			} else {
				// Multi-expanded value beyond single node (e.g. from call): widen.
				final = widenInferred(got)
			}
		} else {
			final = types.Nil
		}
		isConst := i < len(n.Attrs) && n.Attrs[i] == "const"
		if existing := c.cur().vars[name.Name]; existing != nil && !existing.IsBuiltin {
			// Shadowing in same scope: error (Lua allows, but likely bug).
			// Builtins (print/next/...) may be localized, so exempt them.
			c.errorf("TL3011", name.Span(), "redefinition of local '%s'", name.Name)
		}
		c.recordDef(name.Span(), c.bindVar(name.Name, &Symbol{Name: name.Name, Kind: symVar, Type: final, Span: name.Span(), IsConst: isConst}))
	}
	if len(valTypes) > len(n.Names) && len(n.Names) > 0 {
		// Extra values dropped: allowed in Lua, no error.
	}
	// Check const/close misuse: <close> must have a value? Lua requires? Just warn if no value.
	for i, a := range n.Attrs {
		if a == "close" && len(n.Values) == 0 && i < len(n.Names) {
			c.errorf("TL3012", n.Names[i].Span(), "to-be-closed variable '%s' must be initialized", n.Names[i].Name)
		}
	}
}

func widen(t types.Type) types.Type {
	if lit, ok := t.(*types.Literal); ok {
		return lit.Base
	}
	return t
}

// widenInferred deep-widens literals for unannotated inference to avoid over-narrowing.
func widenInferred(t types.Type) types.Type {
	switch x := t.(type) {
	case *types.Literal:
		return x.Base
	case *types.Union:
		var ms []types.Type
		for _, m := range x.Types {
			ms = append(ms, widenInferred(m))
		}
		return types.NewUnion(ms)
	case *types.Array:
		return &types.Array{Elem: widenInferred(x.Elem)}
	case *types.Tuple:
		es := make([]types.Type, len(x.Elems))
		for i, e := range x.Elems {
			es[i] = widenInferred(e)
		}
		return &types.Tuple{Elems: es}
	case *types.Map:
		return &types.Map{Key: widenInferred(x.Key), Value: widenInferred(x.Value)}
	case *types.Object:
		nf := map[string]types.Field{}
		for n, f := range x.Fields {
			nf[n] = types.Field{Type: widenInferred(f.Type), Optional: f.Optional, Readonly: f.Readonly, Access: f.Access, Owner: f.Owner}
		}
		var nix []types.Indexer
		for _, ix := range x.Indexers {
			nix = append(nix, types.Indexer{Key: widenInferred(ix.Key), Value: widenInferred(ix.Value)})
		}
		return &types.Object{Name: x.Name, Fields: nf, Indexers: nix}
	case *types.Class:
		na := make([]types.Type, len(x.Args))
		for i, a := range x.Args {
			na[i] = widenInferred(a)
		}
		return &types.Class{Def: x.Def, Name: x.Name, Args: na, Params: x.Params, Fields: x.Fields, Base: x.Base, IsAbstract: x.IsAbstract}
	case *types.ClassValue:
		return x
	default:
		return widen(t)
	}
}

func isPureList(lit *ast.TableLit) bool {
	for _, f := range lit.Fields {
		if f.Kind != ast.ExprField {
			return false
		}
	}
	return true
}

func (c *Checker) checkLocalFunc(n *ast.LocalFuncDef) {
	ft := c.funcTypeFromLit(n.Func, n.Generics)
	if existing := c.cur().vars[n.Name.Name]; existing != nil && !existing.IsBuiltin {
		c.errorf("TL3011", n.Name.Span(), "redefinition of local '%s'", n.Name.Name)
	}
	sym := c.bindVar(n.Name.Name, &Symbol{Name: n.Name.Name, Kind: symFunc, Type: ft, Span: n.Span(), GenericNames: genericNames(n.Generics), FuncDecl: n.Func})
	c.recordDef(n.Name.Span(), sym)
	c.checkFuncBody(n.Func, n.Generics, ft)
}

func genericNames(gs []*ast.GenericParam) []string {
	var out []string
	for _, g := range gs {
		out = append(out, g.Name.Name)
	}
	return out
}

func (c *Checker) checkGlobalFunc(n *ast.FuncDefStmt) {
	ft := c.funcTypeFromLit(n.Func, n.Generics)
	if n.Name == nil {
		return
	}
	if len(n.Name.Prefix) == 1 && n.Name.Method == nil {
		name := n.Name.Prefix[0].Name
		if g := c.cur().lookupVar(name); g != nil {
			// Redefinition of function: allow (Lua pattern), but update type.
			g.Type = ft
			c.recordDef(n.Name.Prefix[0].Span(), g)
		} else {
			// Define as global in current scope (modules use locals+return, globals rare).
			sym := c.bindVar(name, &Symbol{Name: name, Kind: symFunc, Type: ft, Span: n.Span(), GenericNames: genericNames(n.Generics), FuncDecl: n.Func})
			c.recordDef(n.Name.Prefix[0].Span(), sym)
		}
	} else {
		// a.b.c or a:b — verify base exists.
		base := n.Name.Prefix[0].Name
		sym := c.cur().lookupVar(base)
		if sym == nil {
			c.errorf("TL3013", n.Name.Span(), "undefined table '%s' in function definition", base)
		} else {
			// If base is an object, record method field for later lookups.
			if obj, ok := sym.Type.(*types.Object); ok {
				fieldName := n.Name.Prefix[len(n.Name.Prefix)-1].Name
				if n.Name.Method != nil {
					fieldName = n.Name.Method.Name
				} else if len(n.Name.Prefix) > 1 {
					// a.b.c defines c in... we only track one level; keep simple.
				}
				if obj.Fields == nil {
					obj.Fields = map[string]types.Field{}
				}
				obj.Fields[fieldName] = types.Field{Type: ft}
			}
		}
	}
	c.checkFuncBody(n.Func, n.Generics, ft)
}

func (c *Checker) checkAssign(n *ast.AssignStmt) {
	valTypes := []types.Type{}
	if len(n.Values) > 0 {
		valTypes = c.checkExprListMulti(n.Values)
	}
	for i, tgt := range n.Targets {
		var want types.Type
		var sym *Symbol
		var readonly bool
		switch t := tgt.(type) {
		case *ast.Ident:
			sym = c.cur().lookupVar(t.Name)
			if sym == nil {
				c.errorf("TL3014", t.Span(), "undefined variable '%s'", t.Name)
				want = types.Any
			} else {
				if sym.IsConst {
					c.errorf("TL3015", t.Span(), "cannot assign to const variable '%s'", t.Name)
				}
				c.recordUse(t.Span(), sym)
				want = sym.Type
			}
		case *ast.FieldExpr:
			ot := c.checkExpr(t.Obj)
			f, ok := lookupField(ot, t.Field.Name)
			if !ok {
				if !isAnyLike(ot) {
					c.errorf("TL3016", t.Span(), "type '%s' has no field '%s'", ot.String(), t.Field.Name)
				}
				want = types.Any
			} else {
				if !c.checkAccess(f.Access, f.Owner, t.Span(), "field '"+t.Field.Name+"'") {
					want = types.Any
				} else {
					c.recordMemberUse(t.Field.Span(), ot, t.Field.Name)
					if f.Readonly {
						readonly = true
					}
					want = f.Type
				}
			}
		case *ast.IndexExpr:
			ot := c.checkExpr(t.Obj)
			kt := c.checkExpr(t.Key)
			want = indexResult(ot, kt)
			if want == nil {
				if !isAnyLike(ot) {
					c.errorf("TL3016", t.Span(), "cannot index type '%s'", ot.String())
				}
				want = types.Any
			}
		default:
			c.errorf("TL3017", tgt.Span(), "invalid assignment target")
			want = types.Any
		}
		if readonly {
			c.errorf("TL3018", tgt.Span(), "cannot assign to readonly field")
		}
		var got types.Type
		if i < len(valTypes) {
			got = valTypes[i]
		} else if len(n.Values) > 0 {
			got = types.Nil
		} else {
			got = types.Any
		}
		if got != nil && want != nil {
			if !types.IsAssignable(got, want, c.opt.toTypes()) {
				c.errorf("TL3010", tgt.Span(), "type '%s' is not assignable to type '%s'", got.String(), want.String())
			}
		}
		_ = sym
	}
}

func (c *Checker) checkIf(n *ast.IfStmt) {
	c.checkExpr(n.Cond)
	c.push()
	c.applyNarrowing(n.Cond, true)
	c.checkBlock(n.Then)
	c.pop()
	thenDiv := blockDiverges(n.Then)
	for _, ei := range n.Elseifs {
		c.checkExpr(ei.Cond)
		c.push()
		c.applyNarrowing(ei.Cond, true)
		c.checkBlock(ei.Block)
		c.pop()
	}
	elseDiv := false
	if n.Else != nil {
		c.push()
		c.applyNarrowing(n.Cond, false)
		c.checkBlock(n.Else)
		c.pop()
		elseDiv = blockDiverges(n.Else)
	}
	// Flow narrowing after if: if one branch diverges (return), the other condition holds after.
	// `if x == nil then return end; use(x as non-nil)`
	if thenDiv && n.Else == nil && len(n.Elseifs) == 0 {
		c.applyNarrowing(n.Cond, false)
	} else if n.Else != nil && elseDiv && !thenDiv && len(n.Elseifs) == 0 {
		c.applyNarrowing(n.Cond, true)
	}
}

func blockDiverges(b *ast.Block) bool {
	if b == nil {
		return false
	}
	if b.Ret != nil {
		return true
	}
	if len(b.Stmts) == 0 {
		return false
	}
	// Last statement determines divergence for our purposes.
	switch n := b.Stmts[len(b.Stmts)-1].(type) {
	case *ast.IfStmt:
		// Both branches must diverge.
		if n.Else == nil {
			return false
		}
		if !blockDiverges(n.Then) {
			return false
		}
		if !blockDiverges(n.Else) {
			return false
		}
		for _, ei := range n.Elseifs {
			if !blockDiverges(ei.Block) {
				return false
			}
		}
		return true
	case *ast.DoBlock:
		return blockDiverges(n.Block)
	case *ast.WhileStmt:
		// `while true do ... end` with no break? Conservative false.
		return false
	}
	return false
}

func (c *Checker) checkNumericFor(n *ast.NumericFor) {
	s := c.checkExpr(n.Start)
	e := c.checkExpr(n.End)
	if n.Step != nil {
		c.checkExpr(n.Step)
	}
	// Bounds should be numbers.
	for _, t := range []types.Type{s, e} {
		if !isAnyLike(t) && !types.IsAssignable(t, types.Number, c.opt.toTypes()) {
			c.errorf("TL3020", n.Span(), "numeric for bounds must be numbers, got '%s'", t.String())
			break
		}
	}
	c.push()
	var vt types.Type = types.Number
	if n.VarTy != nil {
		vt = c.resolveType(n.VarTy)
	}
	c.recordDef(n.Var.Span(), c.bindVar(n.Var.Name, &Symbol{Name: n.Var.Name, Kind: symVar, Type: vt, Span: n.Var.Span()}))
	c.checkBlock(n.Block)
	c.pop()
}

func (c *Checker) checkGenericFor(n *ast.GenericFor) {
	var exprTypes []types.Type
	for _, e := range n.Exprs {
		exprTypes = append(exprTypes, c.checkExpr(e))
	}
	_ = exprTypes
	c.push()
	for i, name := range n.Names {
		var t types.Type = types.Any
		if i < len(n.Types) && n.Types[i] != nil {
			t = c.resolveType(n.Types[i])
		}
		c.recordDef(name.Span(), c.bindVar(name.Name, &Symbol{Name: name.Name, Kind: symVar, Type: t, Span: name.Span()}))
	}
	c.checkBlock(n.Block)
	c.pop()
}

func (c *Checker) checkReturn(r *ast.ReturnStmt) {
	vals := []types.Type{}
	if len(r.Exprs) > 0 {
		vals = c.checkExprListMulti(r.Exprs)
	}
	if len(c.retStack) == 0 {
		// Top-level return (module): just check exprs, no signature.
		return
	}
	want := c.retStack[len(c.retStack)-1]
	if want == nil {
		// Unannotated: collect for potential inference (currently any, so no check).
		if len(c.inferStack) > 0 {
			c.inferStack[len(c.inferStack)-1] = append(c.inferStack[len(c.inferStack)-1], vals...)
		}
		return
	}
	if len(want) == 0 && len(vals) == 0 {
		return
	}
	if len(want) == 0 && len(vals) > 0 {
		// Returning values from void function.
		// Allow `return` bare only.
		c.errorf("TL3030", r.Span(), "function returns no values, but %d value(s) returned", len(vals))
		return
	}
	for i, w := range want {
		var g types.Type = types.Nil
		if i < len(vals) {
			g = vals[i]
		}
		if !types.IsAssignable(g, w, c.opt.toTypes()) {
			c.errorf("TL3031", r.Span(), "return type '%s' is not assignable to '%s'", g.String(), w.String())
		}
	}
}

// ---------- type declarations ----------

func (c *Checker) checkTypeAlias(n *ast.TypeAliasDecl) {
	e := c.cur().lookupType(n.Name.Name)
	if e == nil {
		return
	}
	// Resolve to surface errors + cycles.
	_ = c.resolveTypeEntry(e, nil)
}

func (c *Checker) checkInterface(n *ast.InterfaceDecl) {
	e := c.cur().lookupType(n.Name.Name)
	if e == nil {
		return
	}
	for _, m := range n.Fields {
		if m.Name == nil {
			continue
		}
		kind := "field"
		if _, ok := m.Type.(*ast.FuncType); ok {
			kind = "method"
		}
		c.recordMemberDef(m.Name.Span(), e, m.Name.Name, kind)
	}
	_ = c.resolveTypeEntry(e, nil)
}

func (c *Checker) checkEnum(n *ast.EnumDecl) {
	e := c.cur().lookupType(n.Name.Name)
	if e == nil {
		return
	}
	et := c.resolveTypeEntry(e, nil)
	// Define runtime value: enum table.
	if _, ok := et.(*types.Enum); ok {
		fields := map[string]types.Field{}
		for name, m := range et.(*types.Enum).Members {
			fields[name] = types.Field{Type: m}
		}
		obj := &types.Object{Name: n.Name.Name, Fields: fields}
		c.recordDefEntry(n.Name.Span(), c.bindVar(n.Name.Name, &Symbol{Name: n.Name.Name, Kind: symVar, Type: obj, Span: n.Span()}), e)
		for _, m := range n.Members {
			if m.Name == nil {
				continue
			}
			c.recordMemberDef(m.Name.Span(), e, m.Name.Name, "member")
		}
	}
}

// ---------- function types & bodies ----------

func (c *Checker) funcTypeFromLit(fn *ast.FuncLit, generics []*ast.GenericParam) *types.Func {
	// Push tparams for param/return resolution.
	c.pushTParams(generics)
	var params []types.FuncParam
	for _, p := range fn.Params {
		var t types.Type = types.Any
		if p.Type != nil {
			t = c.resolveType(p.Type)
		} else if c.opt.NoImplicitAny {
			c.errorf("TL3040", p.Span(), "parameter '%s' implicitly has 'any' type", p.Name.Name)
		}
		params = append(params, types.FuncParam{Name: p.Name.Name, Type: t})
	}
	var va types.Type
	if fn.HasVarArg {
		if fn.VarArgType != nil {
			va = c.resolveType(fn.VarArgType)
			// `...: T[]`? If annotated as array, keep elem? Convention: `...rest: string[]` means each vararg is string? Or whole is array?
			// Our parser: `...rest: string[]` type is array. For checking `...` uses, treat vararg element as elem of array if array.
			if arr, ok := va.(*types.Array); ok {
				va = arr.Elem
			}
		} else {
			va = types.Any
		}
	}
	var rets []types.Type
	if fn.ReturnType != nil {
		rt := c.resolveType(fn.ReturnType)
		rets = flattenReturns(rt)
		if rets == nil {
			rets = []types.Type{}
		}
	} else {
		// Unannotated: permissive `any` single return for assignability;
		// body checking still skips (see checkFuncBody retStack nil).
		rets = []types.Type{types.Any}
	}
	tps := make([]*types.GenericParam, len(generics))
	for i, g := range generics {
		tp := &types.GenericParam{Name: g.Name.Name}
		if g.Constraint != nil {
			tp.Constraint = c.resolveType(g.Constraint)
		}
		if g.Default != nil {
			tp.Default = c.resolveType(g.Default)
		}
		tps[i] = tp
	}
	c.popTParams()
	return &types.Func{Params: params, HasVarArg: fn.HasVarArg, VarArg: va, Returns: rets, TypeParams: tps}
}

func flattenReturns(t types.Type) []types.Type {
	if t == nil {
		return nil
	}
	if tup, ok := t.(*types.Tuple); ok {
		return tup.Elems
	}
	if p, ok := t.(*types.Primitive); ok && p.K == types.KVoid {
		return nil
	}
	return []types.Type{t}
}

func (c *Checker) checkFuncBody(fn *ast.FuncLit, generics []*ast.GenericParam, ft *types.Func) {
	c.pushTParams(generics)
	c.push() // value scope
	// Labels for goto validation.
	c.labelStack = append(c.labelStack, map[string]token.Span{})
	c.gotoList = append(c.gotoList, nil)
	// Params.
	for i, p := range fn.Params {
		var t types.Type = types.Any
		if i < len(ft.Params) {
			t = ft.Params[i].Type
		}
		c.recordDef(p.Name.Span(), c.bindVar(p.Name.Name, &Symbol{Name: p.Name.Name, Kind: symParam, Type: t, Span: p.Name.Span()}))
	}
	if fn.IsMethod {
		if _, ok := c.cur().vars["self"]; !ok {
			c.cur().vars["self"] = &Symbol{Name: "self", Kind: symParam, Type: types.Any, Span: fn.Span()}
		}
	}
	if fn.HasVarArg {
		// `...` name binding if present.
		if fn.VarArgName != nil {
			// Named rest collects array.
			var restT types.Type = types.Any
			if ft.VarArg != nil {
				restT = &types.Array{Elem: ft.VarArg}
			} else {
				restT = &types.Array{Elem: types.Any}
			}
			c.recordDef(fn.VarArgName.Span(), c.bindVar(fn.VarArgName.Name, &Symbol{Name: fn.VarArgName.Name, Kind: symParam, Type: restT, Span: fn.VarArgName.Span()}))
		}
		c.varargStack = append(c.varargStack, ft.VarArg)
		if ft.VarArg == nil {
			c.varargStack[len(c.varargStack)-1] = types.Any
		}
	} else {
		c.varargStack = append(c.varargStack, nil) // marker: no vararg
	}
	c.retStack = append(c.retStack, ft.Returns)
	// If unannotated (ft.Returns == nil because no annotation vs void?), distinguish: fn.ReturnType == nil => infer mode (nil sentinel).
	// Our funcTypeFromLit returns nil slice when no annotation; void annotation returns empty non-nil? Actually returns nil too for void (len 0). Need to distinguish.
	// Convention: annotated void => empty slice non-nil? Go nil vs empty: both len 0. Use fn.ReturnType to decide.
	isAnnotated := fn.ReturnType != nil
	if !isAnnotated {
		c.retStack[len(c.retStack)-1] = nil
	}
	c.inferStack = append(c.inferStack, nil)
	c.checkBlock(fn.Body)
	// Validate gotos.
	gotos := c.gotoList[len(c.gotoList)-1]
	labels := c.labelStack[len(c.labelStack)-1]
	for _, g := range gotos {
		if _, ok := labels[g.name]; !ok {
			c.errorf("TL3050", g.span, "undefined label '%s'", g.name)
		}
	}
	c.gotoList = c.gotoList[:len(c.gotoList)-1]
	c.labelStack = c.labelStack[:len(c.labelStack)-1]
	c.inferStack = c.inferStack[:len(c.inferStack)-1]
	c.retStack = c.retStack[:len(c.retStack)-1]
	c.varargStack = c.varargStack[:len(c.varargStack)-1]
	c.pop()
	c.popTParams()
	_ = isAnnotated
}

func (c *Checker) pushTParams(gs []*ast.GenericParam) {
	m := map[string]*types.GenericParam{}
	for _, g := range gs {
		var con types.Type
		var def types.Type
		if g.Constraint != nil {
			con = c.resolveType(g.Constraint)
		}
		if g.Default != nil {
			def = c.resolveType(g.Default)
		}
		m[g.Name.Name] = &types.GenericParam{Name: g.Name.Name, Constraint: con, Default: def}
	}
	c.tparamStack = append(c.tparamStack, m)
}

func (c *Checker) popTParams() {
	c.tparamStack = c.tparamStack[:len(c.tparamStack)-1]
}

func (c *Checker) lookupTParam(name string) *types.GenericParam {
	for i := len(c.tparamStack) - 1; i >= 0; i-- {
		if v, ok := c.tparamStack[i][name]; ok {
			return v
		}
	}
	return nil
}

// ---------- type resolution ----------

func (c *Checker) resolveType(n ast.TypeNode) types.Type {
	return c.resolveTypeWithSub(n, map[string]types.Type{})
}

func (c *Checker) resolveTypeWithSub(n ast.TypeNode, sub map[string]types.Type) types.Type {
	if n == nil {
		// Recovery artifact (parser already reported); never crash.
		return types.Any
	}
	switch t := n.(type) {
	case *ast.NamedType:
		return c.resolveNamed(t, sub)
	case *ast.LiteralType:
		return c.resolveLiteral(t)
	case *ast.ArrayType:
		return &types.Array{Elem: c.resolveTypeWithSub(t.Elem, sub)}
	case *ast.UnionType:
		var ms []types.Type
		for _, m := range t.Types {
			ms = append(ms, c.resolveTypeWithSub(m, sub))
		}
		return types.NewUnion(ms)
	case *ast.IntersectionType:
		var ms []types.Type
		for _, m := range t.Types {
			ms = append(ms, c.resolveTypeWithSub(m, sub))
		}
		return types.NewIntersection(ms)
	case *ast.OptionalType:
		e := c.resolveTypeWithSub(t.Elem, sub)
		return types.NewUnion([]types.Type{e, types.Nil})
	case *ast.ParenType:
		return c.resolveTypeWithSub(t.Elem, sub)
	case *ast.TupleType:
		var es []types.Type
		for _, e := range t.Elems {
			es = append(es, c.resolveTypeWithSub(e, sub))
		}
		return &types.Tuple{Elems: es}
	case *ast.TableType:
		return c.resolveTableType(t, sub)
	case *ast.FuncType:
		return c.resolveFuncType(t, sub)
	case *ast.TypeofType:
		return c.resolveTypeof(t)
	case *ast.KeyofType:
		e := c.resolveTypeWithSub(t.Elem, sub)
		return c.resolveKeyof(e, t.Span())
	case *ast.MappedType:
		return c.resolveMapped(t, sub)
	case *ast.IndexedAccessType:
		return c.resolveIndexedAccess(t, sub)
	case *ast.InferType:
		if c.extendsDepth == 0 {
			c.errorf("TL3601", t.Span(), "'infer' is only allowed in the extends clause of a conditional type")
			return types.Any
		}
		return &types.InferParam{Name: t.Name.Name}
	case *ast.ConditionalType:
		return c.resolveConditional(t, sub)
	default:
		c.errorf("TL3100", n.Span(), "unsupported type node %T", n)
		return types.Any
	}
}

func (c *Checker) resolveNamed(t *ast.NamedType, sub map[string]types.Type) types.Type {
	// Primitive lookup (unqualified only).
	if len(t.Args) == 0 {
		switch t.Name {
		case "any":
			return types.Any
		case "unknown":
			return types.Unknown
		case "never":
			return types.Never
		case "void":
			return types.Void
		case "nil":
			return types.Nil
		case "boolean":
			return types.Boolean
		case "number":
			return types.Number
		case "integer":
			return types.Integer
		case "float":
			return types.Float
		case "string":
			return types.String
		case "table":
			return types.Table
		case "thread":
			return types.Thread
		case "userdata":
			return types.Userdata
		case "function":
			return types.Function
		}
	}
	// Qualified names: not supported in v1 (modules).
	if strings.Contains(t.Name, ".") && t.Name != "..." {
		c.errorf("TL3101", t.Span(), "qualified type '%s' is not supported", t.Name)
		return types.Any
	}
	// `...T` rest marker (tuple patterns, vararg positions).
	if t.Name == "..." {
		if len(t.Args) != 1 {
			c.errorf("TL3102", t.Span(), "'...' expects 1 type argument, got %d", len(t.Args))
			return types.Any
		}
		return &types.Ref{Name: "...", Args: []types.Type{c.resolveTypeWithSub(t.Args[0], sub)}}
	}
	// Generic param substitution.
	if s, ok := sub[t.Name]; ok && len(t.Args) == 0 {
		return s
	}
	if gp := c.lookupTParam(t.Name); gp != nil && len(t.Args) == 0 {
		if s, ok := sub[t.Name]; ok {
			return s
		}
		// Return a copy to avoid aliasing.
		return &types.GenericParam{Name: gp.Name, Constraint: gp.Constraint, Default: gp.Default}
	}
	// Special generics: map<K,V>, Array<T>, table<K,V>?
	if t.Name == "map" || t.Name == "table" {
		if len(t.Args) == 0 {
			return types.Table
		}
		if len(t.Args) != 2 {
			c.errorf("TL3102", t.Span(), "'%s' expects 2 type arguments, got %d", t.Name, len(t.Args))
			return types.Table
		}
		return &types.Map{Key: c.resolveTypeWithSub(t.Args[0], sub), Value: c.resolveTypeWithSub(t.Args[1], sub)}
	}
	if t.Name == "Array" || t.Name == "array" {
		if len(t.Args) != 1 {
			c.errorf("TL3102", t.Span(), "'Array' expects 1 type argument, got %d", len(t.Args))
			return types.Any
		}
		return &types.Array{Elem: c.resolveTypeWithSub(t.Args[0], sub)}
	}
	// User type.
	e := c.cur().lookupType(t.Name)
	if e == nil {
		c.errorf("TL3103", t.Span(), "undefined type '%s'", t.Name)
		return types.Any
	}
	c.recordTypeUse(t.Span(), e)
	// Self-reference while a class definition is being built: bind to the
	// shell so recursive types (e.g. `next: Box<T> | nil`) keep working.
	// The shell is filled in place, so later reads see complete members.
	if e.classDecl != nil && e.resolving && e.classShell != nil {
		var sargs []types.Type
		for _, a := range t.Args {
			sargs = append(sargs, c.resolveTypeWithSub(a, sub))
		}
		if isIdentityClassArgs(e, sargs) {
			return e.classShell
		}
		inst := &types.Class{Def: e.classDef, Name: e.name, Args: sargs, Params: classParamNames(e.params), Fields: map[string]types.ClassField{}}
		sub2 := c.classSubFromTypes(e, sargs, t.Span())
		e.classPatches = append(e.classPatches, func() {
			if full, ok := types.Substitute(e.classShell, sub2).(*types.Class); ok {
				inst.Fields = full.Fields
				inst.Base = full.Base
			}
		})
		return inst
	}
	var args []types.Type
	for _, a := range t.Args {
		args = append(args, c.resolveTypeWithSub(a, sub))
	}
	return c.resolveTypeEntryWithArgs(e, args, t.Span())
}

func (c *Checker) resolveLiteral(t *ast.LiteralType) types.Type {
	v := strings.TrimSpace(t.Value)
	if v == "true" || v == "false" {
		return &types.Literal{Value: v, Base: types.Boolean}
	}
	if v == "nil" {
		return types.Nil
	}
	if strings.HasPrefix(v, "\"") || strings.HasPrefix(v, "'") || strings.HasPrefix(v, "[") {
		return &types.Literal{Value: v, Base: types.String}
	}
	// Number literal.
	return &types.Literal{Value: v, Base: types.Number}
}

func (c *Checker) resolveTableType(t *ast.TableType, sub map[string]types.Type) types.Type {
	// Push dummy tparams? Table types don't introduce params.
	obj := &types.Object{Fields: map[string]types.Field{}}
	for _, f := range t.Fields {
		if f.IsIndexer {
			obj.Indexers = append(obj.Indexers, types.Indexer{Key: c.resolveTypeWithSub(f.IndexKey, sub), Value: c.resolveTypeWithSub(f.IndexVal, sub)})
			continue
		}
		var ft types.Type
		if f.IsMethod && f.MethodSig != nil {
			ft = c.resolveTypeWithSub(f.MethodSig, sub)
		} else {
			ft = c.resolveTypeWithSub(f.Type, sub)
		}
		obj.Fields[f.Name] = types.Field{Type: ft, Optional: f.Optional, Readonly: f.Readonly}
	}
	return obj
}

func (c *Checker) resolveFuncType(t *ast.FuncType, sub map[string]types.Type) types.Type {
	c.pushTParams(t.Generics)
	defer c.popTParams()
	var ps []types.FuncParam
	for _, p := range t.Params {
		var pt types.Type = types.Any
		if p.Type != nil {
			pt = c.resolveTypeWithSub(p.Type, sub)
		}
		name := ""
		if p.Name != nil {
			name = p.Name.Name
		}
		if p.IsVarArg {
			// Vararg param in func type: treat as vararg, not regular param.
			continue
		}
		ps = append(ps, types.FuncParam{Name: name, Type: pt, Optional: p.Optional})
	}
	var va types.Type
	hasVA := t.HasVarArg
	if t.VarArgType != nil {
		va = c.resolveTypeWithSub(t.VarArgType, sub)
		hasVA = true
		if arr, ok := va.(*types.Array); ok {
			va = arr.Elem
		}
	}
	// Also check Params for IsVarArg (from tryParse).
	for _, p := range t.Params {
		if p.IsVarArg {
			hasVA = true
			if p.Type != nil {
				va = c.resolveTypeWithSub(p.Type, sub)
				if arr, ok := va.(*types.Array); ok {
					va = arr.Elem
				}
			} else if va == nil {
				va = types.Any
			}
		}
	}
	var rs []types.Type
	for _, r := range t.Returns {
		rs = append(rs, c.resolveTypeWithSub(r, sub))
	}
	tps := make([]*types.GenericParam, len(t.Generics))
	for i, g := range t.Generics {
		tp := &types.GenericParam{Name: g.Name.Name}
		if g.Constraint != nil {
			tp.Constraint = c.resolveTypeWithSub(g.Constraint, sub)
		}
		if g.Default != nil {
			tp.Default = c.resolveTypeWithSub(g.Default, sub)
		}
		tps[i] = tp
	}
	return &types.Func{Params: ps, HasVarArg: hasVA, VarArg: va, Returns: rs, TypeParams: tps}
}

// resolveConditional resolves `Check extends Extends ? True : False`.
// Distributive evaluation mirrors TS: a naked type-parameter check
// distributes over unions once instantiated; anything still generic
// stays deferred (permissive in assignability until then).
func (c *Checker) resolveConditional(n *ast.ConditionalType, sub map[string]types.Type) types.Type {
	check := c.resolveTypeWithSub(n.Check, sub)
	c.extendsDepth++
	ext := c.resolveTypeWithSub(n.Extends, sub)
	c.extendsDepth--
	// `infer U` names bind in the true branch only (like TS).
	sub2 := make(map[string]types.Type, len(sub)+2)
	for k, v := range sub {
		sub2[k] = v
	}
	for name := range collectInferNames(n.Extends, nil) {
		sub2[name] = &types.InferParam{Name: name}
	}
	trueB := c.resolveTypeWithSub(n.True, sub2)
	falseB := c.resolveTypeWithSub(n.False, sub)
	distributive := isNakedTypeParam(n.Check, c, sub)
	cond := &types.Conditional{Check: check, Extends: ext, TrueBranch: trueB, FalseBranch: falseB, Distributive: distributive}
	if r, ok := types.TryEvalConditional(cond, c.opt.toTypes()); ok {
		return r
	}
	return cond
}

// isNakedTypeParam reports `T` (bare parameter reference, no args).
func isNakedTypeParam(n ast.TypeNode, c *Checker, sub map[string]types.Type) bool {
	nt, ok := n.(*ast.NamedType)
	if !ok || len(nt.Args) > 0 || strings.Contains(nt.Name, ".") {
		return false
	}
	if _, ok := sub[nt.Name]; ok {
		return true
	}
	return c.lookupTParam(nt.Name) != nil
}

// resolveMapped resolves `{ [K in Keys]?: V }`, expanding concrete
// domains eagerly and deferring the rest.
func (c *Checker) resolveMapped(n *ast.MappedType, sub map[string]types.Type) types.Type {
	nd := len(c.diags)
	constraint := c.resolveTypeWithSub(n.Constraint, sub)
	// The key variable is visible inside the value type (shadows outer).
	c.pushTParams([]*ast.GenericParam{{Name: n.Param}})
	value := c.resolveTypeWithSub(n.Value, sub)
	c.popTParams()
	m := &types.Mapped{Param: n.Param.Name, Constraint: constraint, Optional: n.Optional, OptMinus: n.OptMinus, Readonly: n.Readonly, RoMinus: n.RoMinus, Value: value}
	// Homomorphic base: `{[K in keyof T]...}` inherits T's field modifiers
	// unless an explicit `?`/`-?`/`readonly`/`-readonly` overrides them.
	// Only when nothing errored so far, so the re-resolution stays silent.
	if kn, ok := n.Constraint.(*ast.KeyofType); ok && len(c.diags) == nd {
		if base := c.resolveTypeWithSub(kn.Elem, sub); base != nil {
			if o, ok := base.(*types.Object); ok {
				m.Homomorphic = o
			}
		}
	}
	if r, ok := types.TryEvalMapped(m, c.opt.toTypes()); ok {
		return r
	}
	// Only report unmappable concrete domains; Any/Unknown arise from
	// unfilled generic parameters (definition check) or dynamic code,
	// both of which stay deferred.
	if types.IsConcreteType(constraint) && !isAnyLike(constraint) {
		c.errorf("TL3602", n.Span(), "cannot map over type '%s'", constraint.String())
		return types.Any
	}
	return m
}

// resolveIndexedAccess resolves `T[K]`.
func (c *Checker) resolveIndexedAccess(n *ast.IndexedAccessType, sub map[string]types.Type) types.Type {
	obj := c.resolveTypeWithSub(n.Obj, sub)
	index := c.resolveTypeWithSub(n.Index, sub)
	if r, ok, errStr := types.TryEvalIndexed(obj, index, c.opt.toTypes()); ok {
		if errStr != "" {
			c.errorf("TL3603", n.Span(), "%s", errStr)
			return types.Any
		}
		return r
	}
	return &types.IndexedAccess{Obj: obj, Index: index}
}

func (c *Checker) resolveTypeof(t *ast.TypeofType) types.Type {
	if id, ok := t.Expr.(*ast.Ident); ok {
		if s := c.cur().lookupVar(id.Name); s != nil {
			return s.Type
		}
		c.errorf("TL3104", t.Span(), "typeof: undefined variable '%s'", id.Name)
		return types.Any
	}
	// General expr: check it.
	return c.checkExpr(t.Expr)
}

func (c *Checker) resolveKeyof(e types.Type, sp token.Span) types.Type {
	switch x := e.(type) {
	case *types.Object:
		var ms []types.Type
		for name := range x.Fields {
			ms = append(ms, &types.Literal{Value: strconv.Quote(name), Base: types.String})
		}
		for _, ix := range x.Indexers {
			// keyof of indexer: key type itself.
			ms = append(ms, ix.Key)
		}
		if len(ms) == 0 {
			return types.Never
		}
		return types.NewUnion(ms)
	case *types.Tuple:
		var ms []types.Type
		for i := range x.Elems {
			ms = append(ms, &types.Literal{Value: strconv.Itoa(i + 1), Base: types.Number})
		}
		return types.NewUnion(ms)
	case *types.Array:
		return types.Number
	case *types.Map:
		return x.Key
	case *types.Class:
		var ms []types.Type
		for name, f := range x.Fields {
			if f.Access != types.Public {
				continue
			}
			ms = append(ms, &types.Literal{Value: strconv.Quote(name), Base: types.String})
		}
		if len(ms) == 0 {
			return types.Never
		}
		return types.NewUnion(ms)
	default:
		if isAnyLike(e) {
			return types.Any
		}
		c.errorf("TL3105", sp, "keyof not supported for '%s'", e.String())
		return types.Any
	}
}

func (c *Checker) resolveTypeEntry(e *typeEntry, sub map[string]types.Type) types.Type {
	return c.resolveTypeEntryWithArgs(e, nil, token.Span{})
}

func (c *Checker) resolveTypeEntryWithArgs(e *typeEntry, args []types.Type, useSpan token.Span) types.Type {
	// Enum: no params.
	if e.enumDecl != nil {
		if e.enumType != nil {
			return e.enumType
		}
		if e.resolving {
			c.errorf("TL3106", useSpan, "cyclic enum '%s'", e.name)
			return types.Any
		}
		e.resolving = true
		et := c.buildEnum(e.enumDecl)
		e.enumType = et
		e.resolving = false
		return et
	}
	// Classes: definition is built once (generic placeholders intact);
	// every reference substitutes arguments (defaults/unknown filled).
	if e.classDecl != nil {
		def := c.ensureClassDef(e)
		if def == nil {
			return types.Any
		}
		if len(e.params) == 0 {
			if len(args) > 0 {
				c.errorf("TL3107", useSpan, "type '%s' takes no type arguments, got %d", e.name, len(args))
			}
			return def.inst
		}
		sub := c.classSubFromTypes(e, args, useSpan)
		if inst, ok := types.Substitute(def.inst, sub).(*types.Class); ok {
			return inst
		}
		return def.inst
	}
	// Build substitution for generic params.
	sub := map[string]types.Type{}
	if len(e.params) > 0 {
		if len(args) > len(e.params) {
			c.errorf("TL3107", useSpan, "type '%s' expects %d argument(s), got %d", e.name, len(e.params), len(args))
		}
		for i, p := range e.params {
			var a types.Type
			if i < len(args) {
				a = args[i]
			} else {
				// Default or unknown.
				if p.Default != nil {
					a = c.resolveType(p.Default)
				} else {
					a = types.Unknown
				}
			}
			// Check constraint.
			if p.Constraint != nil {
				con := c.resolveType(p.Constraint)
				if !types.IsAssignable(a, con, c.opt.toTypes()) {
					c.errorf("TL3108", useSpan, "type argument '%s' does not satisfy constraint '%s'", a.String(), con.String())
				}
			}
			sub[p.Name.Name] = a
		}
		// Memoize.
		key := typesKey(args)
		if cached, ok := e.cacheInst[key]; ok {
			return cached
		}
		if e.resolving {
			// Recursive generic: return any to break cycle (error reported at alias level if direct).
			return types.Any
		}
		e.resolving = true
		var res types.Type
		if e.alias != nil {
			res = c.resolveTypeWithSub(e.alias.Type, sub)
		} else if e.iface != nil {
			res = c.buildInterface(e.iface, sub)
		} else {
			res = types.Any
		}
		e.resolving = false
		e.cacheInst[key] = res
		return res
	}
	// Non-generic: cache.
	if e.cached != nil {
		return e.cached
	}
	if e.resolving {
		c.errorf("TL3106", useSpan, "cyclic type alias '%s'", e.name)
		return types.Any
	}
	e.resolving = true
	var res types.Type
	if e.alias != nil {
		res = c.resolveTypeWithSub(e.alias.Type, sub)
	} else if e.iface != nil {
		res = c.buildInterface(e.iface, sub)
	} else {
		res = types.Any
	}
	e.resolving = false
	e.cached = res
	return res
}

func typesKey(args []types.Type) string {
	ps := make([]string, len(args))
	for i, a := range args {
		ps[i] = a.String()
	}
	return strings.Join(ps, "|")
}

func (c *Checker) buildInterface(n *ast.InterfaceDecl, sub map[string]types.Type) types.Type {
	obj := &types.Object{Name: n.Name.Name, Fields: map[string]types.Field{}}
	// Extends: merge base fields.
	for _, b := range n.Extends {
		bt := c.resolveTypeWithSub(b, sub)
		if bo, ok := bt.(*types.Object); ok {
			for k, v := range bo.Fields {
				obj.Fields[k] = v
			}
			obj.Indexers = append(obj.Indexers, bo.Indexers...)
		} else if isAnyLike(bt) {
			continue
		} else {
			c.errorf("TL3109", b.Span(), "interface can only extend object types, got '%s'", bt.String())
		}
	}
	for _, f := range n.Fields {
		var ft types.Type
		// Method shorthand fields store FuncType in Type already.
		ft = c.resolveTypeWithSub(f.Type, sub)
		if _, exists := obj.Fields[f.Name.Name]; exists {
			c.errorf("TL3110", f.Span(), "duplicate field '%s'", f.Name.Name)
		}
		obj.Fields[f.Name.Name] = types.Field{Type: ft, Optional: f.Optional, Readonly: f.Readonly}
	}
	return obj
}

func (c *Checker) buildEnum(n *ast.EnumDecl) *types.Enum {
	members := map[string]types.Type{}
	var underlying types.Type
	autoNum := 0
	autoAllowed := true
	seenVals := map[string]string{} // value string -> member name
	for _, m := range n.Members {
		var mt types.Type
		if m.Value == nil {
			if !autoAllowed {
				c.errorf("TL3120", m.Span(), "enum member '%s' must have an explicit value (mixed string enum)", m.Name.Name)
				mt = types.Any
			} else {
				mt = &types.Literal{Value: strconv.Itoa(autoNum), Base: types.Integer}
				autoNum++
			}
		} else {
			switch v := m.Value.(type) {
			case *ast.StringLit:
				mt = &types.Literal{Value: v.Literal, Base: types.String}
				autoAllowed = false
			case *ast.NumberLit:
				// Determine integer vs float.
				base := types.Type(types.Integer)
				if strings.ContainsAny(v.Literal, ".eEpP") {
					base = types.Float
				}
				mt = &types.Literal{Value: v.Literal, Base: base}
				// Update auto counter if integer.
				if iv, err := strconv.Atoi(strings.ReplaceAll(v.Literal, "_", "")); err == nil {
					autoNum = iv + 1
				} else {
					autoAllowed = false
				}
			case *ast.BoolLit:
				mt = &types.Literal{Value: strconv.FormatBool(v.Value), Base: types.Boolean}
				autoAllowed = false
			case *ast.UnOpExpr:
				// Negative number: -N
				if num, ok := v.X.(*ast.NumberLit); ok {
					base := types.Type(types.Integer)
					if strings.ContainsAny(num.Literal, ".eEpP") {
						base = types.Float
					}
					mt = &types.Literal{Value: "-" + num.Literal, Base: base}
				} else {
					c.errorf("TL3121", m.Span(), "enum member '%s' must be a literal", m.Name.Name)
					mt = types.Any
				}
			default:
				c.errorf("TL3121", m.Span(), "enum member '%s' must be a literal", m.Name.Name)
				mt = types.Any
			}
		}
		if prev, ok := seenVals[mt.String()]; ok {
			c.errorf("TL3122", m.Span(), "duplicate enum value for '%s' (already used by '%s')", m.Name.Name, prev)
		} else {
			seenVals[mt.String()] = m.Name.Name
		}
		if _, ok := members[m.Name.Name]; ok {
			c.errorf("TL3123", m.Span(), "duplicate enum member '%s'", m.Name.Name)
		}
		members[m.Name.Name] = mt
		if lit, ok := mt.(*types.Literal); ok {
			if underlying == nil {
				underlying = lit.Base
			} else if underlying.String() != lit.Base.String() {
				// Mixed: underlying becomes union.
				underlying = types.NewUnion([]types.Type{underlying, lit.Base})
			}
		}
	}
	if underlying == nil {
		underlying = types.Integer
	}
	return &types.Enum{Name: n.Name.Name, Underlying: underlying, Members: members}
}

// ---------- expressions ----------

func (c *Checker) checkExprListMulti(exprs []ast.Expr) []types.Type {
	if len(exprs) == 0 {
		return nil
	}
	var out []types.Type
	for i, e := range exprs {
		if i == len(exprs)-1 {
			out = append(out, c.checkExprMulti(e)...)
		} else {
			out = append(out, c.checkExprSingle(e))
		}
	}
	return out
}

// checkExprSingle returns the first value of an expression.
func (c *Checker) checkExprSingle(e ast.Expr) types.Type {
	ms := c.checkExprMulti(e)
	if len(ms) == 0 {
		return types.Nil
	}
	return ms[0]
}

func (c *Checker) checkExpr(e ast.Expr) types.Type {
	return c.checkExprSingle(e)
}

// checkExprMulti expands multi-return calls on the last position.
func (c *Checker) checkExprMulti(e ast.Expr) []types.Type {
	switch n := e.(type) {
	case *ast.CallExpr:
		// `Point.new(...)` gets instantiation checks (abstract/private).
		if fe, ok := n.Func.(*ast.FieldExpr); ok && fe.Field.Name == "new" {
			if id, ok := fe.Obj.(*ast.Ident); ok {
				if s := c.cur().lookupVar(id.Name); s != nil {
					if cv, ok := s.Type.(*types.ClassValue); ok {
						c.recordUse(id.Span(), s)
						return c.checkClassInstantiate(cv, n.Args, n.Span(), c.resolveTypeArgs(n.TypeArgs))
					}
				}
			}
		}
		if id, ok := n.Func.(*ast.Ident); ok && id.Name == "super" && c.cur().lookupVar("super") == nil {
			return c.checkSuperCall(n)
		}
		return c.checkCall(n.Func.Span(), c.checkExpr(n.Func), n.Args, n.Span(), c.resolveTypeArgs(n.TypeArgs))
	case *ast.MethodCallExpr:
		if id, ok := n.Obj.(*ast.Ident); ok && id.Name == "super" && c.cur().lookupVar("super") == nil {
			return c.checkSuperMethod(n)
		}
		// `Point:new(...)` routes to instantiation checks.
		if id, ok := n.Obj.(*ast.Ident); ok && n.Method.Name == "new" {
			if s := c.cur().lookupVar(id.Name); s != nil {
				if cv, ok := s.Type.(*types.ClassValue); ok {
					c.recordUse(id.Span(), s)
					return c.checkClassInstantiate(cv, n.Args, n.Span(), c.resolveTypeArgs(n.TypeArgs))
				}
			}
		}
		ot := c.checkExpr(n.Obj)
		mf := c.lookupMethodField(ot, n.Method.Name, n.Span())
		if !c.checkAccess(mf.Access, mf.Owner, n.Span(), "method '"+n.Method.Name+"'") {
			return []types.Type{types.Any}
		}
		if _, ok := lookupField(ot, n.Method.Name); ok {
			c.recordMemberUse(n.Method.Span(), ot, n.Method.Name)
		}
		return c.checkCall(n.Span(), mf.Type, n.Args, n.Span(), c.resolveTypeArgs(n.TypeArgs))
	case *ast.VarArg:
		vt := c.currentVarArg()
		if vt == nil {
			c.errorf("TL3200", n.Span(), "'...' used outside vararg function")
			return []types.Type{types.Any}
		}
		// `...` expands to multiple values of vt. Represent as single for non-last? For multi, return [vt] (one-or-more modeled as one).
		// To keep sound, return single vt; callers in multi context treat as one. (Full variadic multi is unbounded; model as one.)
		return []types.Type{vt}
	default:
		return []types.Type{c.checkExprOne(e)}
	}
}

func (c *Checker) currentVarArg() types.Type {
	if len(c.varargStack) == 0 {
		return nil
	}
	return c.varargStack[len(c.varargStack)-1]
}

func (c *Checker) checkExprOne(e ast.Expr) types.Type {
	if e == nil {
		// Recovery artifact (parser already reported); never crash.
		return types.Any
	}
	switch n := e.(type) {
	case *ast.Ident:
		if s := c.cur().lookupVar(n.Name); s != nil {
			c.recordUse(n.Span(), s)
			return s.Type
		}
		if n.Name == "self" {
			c.errorf("TL3201", n.Span(), "'self' is only available inside methods")
			return types.Any
		}
		if n.Name == "super" {
			c.errorf("TL3301", n.Span(), "'super' can only be used inside methods of a derived class")
			return types.Any
		}
		c.errorf("TL3201", n.Span(), "undefined variable '%s'", n.Name)
		return types.Any
	case *ast.NumberLit:
		base := types.Type(types.Integer)
		if strings.ContainsAny(n.Literal, ".eEpP") {
			// Hex integers with p exponent are floats? Keep float.
			base = types.Float
		}
		return &types.Literal{Value: n.Literal, Base: base}
	case *ast.StringLit:
		return &types.Literal{Value: n.Literal, Base: types.String}
	case *ast.BoolLit:
		return &types.Literal{Value: strconv.FormatBool(n.Value), Base: types.Boolean}
	case *ast.NilLit:
		return types.Nil
	case *ast.VarArg:
		vt := c.currentVarArg()
		if vt == nil {
			c.errorf("TL3200", n.Span(), "'...' used outside vararg function")
			return types.Any
		}
		return vt
	case *ast.ParenExpr:
		return c.checkExpr(n.E)
	case *ast.TableLit:
		return c.checkTable(n)
	case *ast.FuncLit:
		ft := c.funcTypeFromLit(n, n.Generics)
		c.checkFuncBody(n, n.Generics, ft)
		return ft
	case *ast.IndexExpr:
		ot := c.checkExpr(n.Obj)
		kt := c.checkExpr(n.Key)
		if r := indexResult(ot, kt); r != nil {
			return r
		}
		if isAnyLike(ot) {
			return types.Any
		}
		c.errorf("TL3202", n.Span(), "cannot index type '%s'", ot.String())
		return types.Any
	case *ast.FieldExpr:
		ot := c.checkExpr(n.Obj)
		if f, ok := lookupField(ot, n.Field.Name); ok {
			if !c.checkAccess(f.Access, f.Owner, n.Span(), "field '"+n.Field.Name+"'") {
				return types.Any
			}
			c.recordMemberUse(n.Field.Span(), ot, n.Field.Name)
			if f.Optional {
				return types.NewUnion([]types.Type{f.Type, types.Nil})
			}
			return f.Type
		}
		if isAnyLike(ot) {
			return types.Any
		}
		// Enum member access: Color.Red
		if en, ok := ot.(*types.Enum); ok {
			_ = en
		}
		// If obj is an enum value object (runtime table), fields were defined.
		c.errorf("TL3203", n.Span(), "type '%s' has no field '%s'", ot.String(), n.Field.Name)
		return types.Any
	case *ast.CallExpr:
		return c.checkExprSingle(n) // unreachable (handled in multi) but keep.
	case *ast.MethodCallExpr:
		return c.checkExprSingle(n)
	case *ast.BinOpExpr:
		return c.checkBinOp(n)
	case *ast.UnOpExpr:
		return c.checkUnOp(n)
	case *ast.CastExpr:
		srcT := c.checkExpr(n.E)
		dstT := c.resolveType(n.T)
		if !types.IsAssignable(srcT, dstT, c.opt.toTypes()) && !types.IsAssignable(dstT, srcT, c.opt.toTypes()) {
			c.errorf("TL3204", n.Span(), "conversion from '%s' to '%s' may be a mistake", srcT.String(), dstT.String())
		}
		return dstT
	case *ast.NonNullExpr:
		t := c.checkExpr(n.E)
		stripped, hadNil := types.RemoveNil(t)
		if !hadNil && !isAnyLike(t) {
			// Warn? `!` on non-nullable is unnecessary but not an error. Keep silent to avoid noise? Emit warning as error for now? Skip.
		}
		if hadNil {
			// stripped could be Never if it was just nil: `nil!` => never (likely bug).
			if stripped.Kind() == types.KNever {
				c.errorf("TL3205", n.Span(), "non-null assertion on 'nil'")
				return types.Never
			}
			return stripped
		}
		return t
	}
	c.errorf("TL3200", e.Span(), "unsupported expression %T", e)
	return types.Any
}

func (c *Checker) checkTable(n *ast.TableLit) types.Type {
	// Collect.
	var listElems []types.Type
	fields := map[string]types.Field{}
	var indexers []types.Indexer
	hasNamed := false
	for _, f := range n.Fields {
		switch f.Kind {
		case ast.ExprField:
			listElems = append(listElems, c.checkExpr(f.Val))
		case ast.NamedField:
			hasNamed = true
			fields[f.Key.Name] = types.Field{Type: c.checkExpr(f.Val)}
		case ast.IndexedField:
			// If key is string/number literal, treat as named.
			if lit := literalKey(f.KeyX); lit != nil {
				fields[*lit] = types.Field{Type: c.checkExpr(f.Val)}
				hasNamed = true
			} else {
				kt := c.checkExpr(f.KeyX)
				vt := c.checkExpr(f.Val)
				indexers = append(indexers, types.Indexer{Key: kt, Value: vt})
				hasNamed = true
			}
		}
	}
	if !hasNamed {
		if len(listElems) == 0 {
			return &types.Object{Fields: map[string]types.Field{}}
		}
		// Pure list: keep positional info as Tuple so `{"a", 1}` fits `[string, number]`.
		// Unannotated inference converts to Array (see checkLocalAssign).
		return &types.Tuple{Elems: listElems}
	}
	// Mixed or pure named: object with optional array indexer for list part.
	obj := &types.Object{Fields: fields, Indexers: indexers}
	if len(listElems) > 0 {
		elem := types.NewUnion(listElems)
		obj.Indexers = append(obj.Indexers, types.Indexer{Key: types.Integer, Value: elem})
	}
	return obj
}

func literalKey(e ast.Expr) *string {
	switch n := e.(type) {
	case *ast.StringLit:
		s, err := parseStringLiteral(n.Literal)
		if err == nil {
			return &s
		}
		// Fallback: raw.
		raw := strings.Trim(n.Literal, `"'`)
		return &raw
	case *ast.NumberLit:
		s := n.Literal
		return &s
	}
	return nil
}

func parseStringLiteral(lit string) (string, error) {
	if len(lit) >= 2 && (lit[0] == '"' || lit[0] == '\'') {
		// Use strconv-ish unquoting for simple cases; Lua escapes differ, so best-effort.
		inner := lit[1 : len(lit)-1]
		return inner, nil
	}
	if strings.HasPrefix(lit, "[") {
		// Long string: strip brackets.
		start := strings.Index(lit, "[")
		_ = start
		return lit, nil
	}
	return lit, nil
}

func (c *Checker) checkBinOp(n *ast.BinOpExpr) types.Type {
	l := c.checkExpr(n.L)
	r := c.checkExpr(n.R)
	anyLike := isAnyLike(l) || isAnyLike(r)
	switch n.Op {
	case token.Plus, token.Minus, token.Star, token.Slash, token.DoubleSlash, token.Percent, token.Caret:
		if !anyLike {
			if !isNumberLike(l) || !isNumberLike(r) {
				c.errorf("TL3210", n.Span(), "arithmetic on non-numbers ('%s', '%s')", l.String(), r.String())
			}
		}
		// Integer preservation: if both integer and op is not / ^, result integer.
		if isIntegerLike(l) && isIntegerLike(r) && n.Op != token.Slash && n.Op != token.Caret {
			return types.Integer
		}
		return types.Number
	case token.Amp, token.Pipe, token.Tilde, token.ShiftL, token.ShiftR:
		if !anyLike && (!isIntegerLike(l) || !isIntegerLike(r)) {
			c.errorf("TL3211", n.Span(), "bitwise operation requires integers ('%s', '%s')", l.String(), r.String())
		}
		return types.Integer
	case token.DotDot:
		if !anyLike && (!isConcatable(l) || !isConcatable(r)) {
			c.errorf("TL3212", n.Span(), "concatenation requires strings/numbers ('%s', '%s')", l.String(), r.String())
		}
		return types.String
	case token.Eq, token.Ne:
		return types.Boolean
	case token.Lt, token.Le, token.Gt, token.Ge:
		if !anyLike && !isComparable(l, r) {
			c.errorf("TL3213", n.Span(), "comparison of incompatible types ('%s', '%s')", l.String(), r.String())
		}
		return types.Boolean
	case token.And, token.Or:
		// `and`/`or`: result is union (simplified).
		if n.Op == token.Or {
			// a or b: if a is falsy, b. Type = NonFalsy(a) | b. Simplify to union minus false/nil? Keep union.
			return types.NewUnion([]types.Type{l, r})
		}
		return types.NewUnion([]types.Type{l, r})
	}
	c.errorf("TL3214", n.Span(), "unsupported operator '%s'", n.Op)
	return types.Any
}

func (c *Checker) checkUnOp(n *ast.UnOpExpr) types.Type {
	x := c.checkExpr(n.X)
	switch n.Op {
	case token.Not:
		return types.Boolean
	case token.Hash:
		if !isAnyLike(x) && !isLengthable(x) {
			c.errorf("TL3220", n.Span(), "length operator on '%s'", x.String())
		}
		return types.Integer
	case token.Minus:
		if !isAnyLike(x) && !isNumberLike(x) {
			c.errorf("TL3221", n.Span(), "unary minus on '%s'", x.String())
		}
		if isIntegerLike(x) {
			return types.Integer
		}
		return types.Number
	case token.Tilde:
		if !isAnyLike(x) && !isIntegerLike(x) {
			c.errorf("TL3222", n.Span(), "bitwise not on '%s'", x.String())
		}
		return types.Integer
	}
	return types.Any
}

// ---------- calls & generics ----------

func (c *Checker) lookupMethod(ot types.Type, name string, sp token.Span) types.Type {
	if f, ok := lookupField(ot, name); ok {
		return f.Type
	}
	if isAnyLike(ot) {
		return types.Any
	}
	c.errorf("TL3230", sp, "type '%s' has no method '%s'", ot.String(), name)
	return types.Any
}

// lookupMethodField is lookupMethod with visibility metadata for access checks.
func (c *Checker) lookupMethodField(ot types.Type, name string, sp token.Span) types.Field {
	if f, ok := lookupField(ot, name); ok {
		return f
	}
	if isAnyLike(ot) {
		return types.Field{Type: types.Any}
	}
	c.errorf("TL3230", sp, "type '%s' has no method '%s'", ot.String(), name)
	return types.Field{Type: types.Any}
}

// resolveTypeArgs resolves explicit generic argument nodes (`f<T>(x)`).
func (c *Checker) resolveTypeArgs(nodes []ast.TypeNode) []types.Type {
	if len(nodes) == 0 {
		return nil
	}
	out := make([]types.Type, len(nodes))
	for i, n := range nodes {
		out[i] = c.resolveType(n)
	}
	return out
}

// bindExplicitArgs binds explicit type arguments positionally into sub and
// returns the function with those positions pre-substituted, so later
// inference only fills the remainder. Unfilled params use defaults.
func (c *Checker) bindExplicitArgs(fn *types.Func, explicit []types.Type, callSpan token.Span, sub map[string]types.Type) *types.Func {
	if len(explicit) == 0 {
		return fn
	}
	if len(fn.TypeParams) == 0 {
		c.errorf("TL3234", callSpan, "explicit type arguments given to non-generic function")
		return fn
	}
	if len(explicit) > len(fn.TypeParams) {
		c.errorf("TL3234", callSpan, "expected %d type argument(s), got %d", len(fn.TypeParams), len(explicit))
		explicit = explicit[:len(fn.TypeParams)]
	}
	exSub := map[string]types.Type{}
	for i, gp := range fn.TypeParams {
		if i >= len(explicit) {
			break
		}
		a := explicit[i]
		if gp.Constraint != nil && !types.IsAssignable(a, gp.Constraint, c.opt.toTypes()) {
			c.errorf("TL3234", callSpan, "type argument '%s' does not satisfy constraint '%s'", a.String(), gp.Constraint.String())
		}
		exSub[gp.Name] = a
		sub[gp.Name] = a
	}
	// Defaults for params with neither explicit nor (yet) inferred values.
	for _, gp := range fn.TypeParams {
		if _, ok := sub[gp.Name]; !ok && gp.Default != nil {
			sub[gp.Name] = types.Substitute(gp.Default, sub)
		}
	}
	if out, ok := types.Substitute(fn, exSub).(*types.Func); ok {
		return out
	}
	return fn
}

// checkCall checks a call and returns multi return types.
// explicit holds resolved explicit generic arguments (`f<T>(x)`);
// remaining parameters still infer from arguments.
func (c *Checker) checkCall(opSpan token.Span, fnT types.Type, args []ast.Expr, callSpan token.Span, explicit []types.Type) []types.Type {
	if isAnyLike(fnT) {
		for _, a := range args {
			c.checkExpr(a)
		}
		return []types.Type{types.Any}
	}
	// Calling the class itself instantiates it (`Point(...)` via __call).
	if cv, ok := fnT.(*types.ClassValue); ok {
		return c.checkClassInstantiate(cv, args, callSpan, explicit)
	}
	fn, ok := fnT.(*types.Func)
	if !ok {
		// Callable objects with __call? For v1, only functions callable.
		for _, a := range args {
			c.checkExpr(a)
		}
		c.errorf("TL3231", callSpan, "cannot call type '%s'", fnT.String())
		return []types.Type{types.Any}
	}
	// Generic inference: collect type params from fn? Our Func doesn't store generic names after substitution? For generic functions, funcTypeFromLit resolved params with tparams as GenericParam placeholders.
	// Infer: map param name -> inferred type from args.
	sub := map[string]types.Type{}
	// Explicit type arguments bind first (positional); the rest infer.
	// Missing ones fall back to defaults, like TS partial application.
	fn = c.bindExplicitArgs(fn, explicit, callSpan, sub)
	// Check args (expand? call args don't expand multi except last? In Lua, `f(g())` where g returns multi expands! Support: expand last arg if multi.
	argTypes := c.checkCallArgs(args)
	// Arity checks (Lua allows any count at runtime; statically enforce required).
	req := 0
	for _, p := range fn.Params {
		if !p.Optional {
			req++
		}
	}
	if len(argTypes) < req && !fn.HasVarArg {
		// If source requires more than supplied and missing can't be nil-optional, error.
		// Check if missing params are optional or nilable.
		missingOK := true
		for i := len(argTypes); i < len(fn.Params); i++ {
			if !fn.Params[i].Optional && !types.ContainsNil(fn.Params[i].Type) {
				missingOK = false
				break
			}
		}
		if !missingOK {
			c.errorf("TL3232", callSpan, "expected %d argument(s), got %d", req, len(argTypes))
		}
	}
	// Check each arg.
	for i, at := range argTypes {
		var pt types.Type
		if i < len(fn.Params) {
			pt = fn.Params[i].Type
		} else if fn.HasVarArg {
			if fn.VarArg != nil {
				pt = fn.VarArg
			} else {
				pt = types.Any
			}
		} else {
			// Extra args: allowed in Lua (dropped). No error to stay compatible? TS would error. For Lua compat, allow silently.
			continue
		}
		// Infer generics occurring in pt.
		inferInto(pt, at, sub)
		if !types.IsAssignable(at, applySub(pt, sub), c.opt.toTypes()) {
			// Retry with full sub after inference? For better messages, check after collecting all? We check incrementally; may false-positive when later args refine.
			// To avoid, defer errors to second pass below.
		}
	}
	// Second pass for errors with final sub.
	for i, at := range argTypes {
		var pt types.Type
		if i < len(fn.Params) {
			pt = applySub(fn.Params[i].Type, sub)
		} else if fn.HasVarArg {
			if fn.VarArg != nil {
				pt = applySub(fn.VarArg, sub)
			} else {
				continue
			}
		} else {
			continue
		}
		if !types.IsAssignable(at, pt, c.opt.toTypes()) {
			c.errorf("TL3233", args[i].Span(), "argument %d: type '%s' is not assignable to '%s'", i+1, at.String(), pt.String())
			break // one error per call to avoid cascades.
		}
	}
	// Substitute returns.
	var rets []types.Type
	for _, r := range fn.Returns {
		rets = append(rets, applySub(r, sub))
	}
	// Validate generic constraints with inferred args? Constraints checked at definition; inferred any satisfies? If inferred Unknown, keep.
	if len(rets) == 0 {
		return nil
	}
	return rets
}

func (c *Checker) checkCallArgs(args []ast.Expr) []types.Type {
	if len(args) == 0 {
		return nil
	}
	var out []types.Type
	for i, a := range args {
		if i == len(args)-1 {
			out = append(out, c.checkExprMulti(a)...)
		} else {
			out = append(out, c.checkExprSingle(a))
		}
	}
	return out
}

func inferInto(param, arg types.Type, sub map[string]types.Type) {
	switch p := param.(type) {
	case *types.GenericParam:
		if existing, ok := sub[p.Name]; ok {
			// Join: union? Keep first (simple). If differing, widen to union.
			if existing.String() != arg.String() {
				sub[p.Name] = types.NewUnion([]types.Type{existing, arg})
			}
			return
		}
		sub[p.Name] = arg
	case *types.InferParam:
		// Inference positions (e.g. conditional checks) bind like params.
		if existing, ok := sub[p.Name]; ok {
			if existing.String() != arg.String() {
				sub[p.Name] = types.NewUnion([]types.Type{existing, arg})
			}
			return
		}
		sub[p.Name] = arg
	case *types.Conditional:
		// Infer through the check pattern (`Elem<T>` from `[1, 2]`).
		inferInto(p.Check, arg, sub)
	case *types.Array:
		switch a := arg.(type) {
		case *types.Array:
			inferInto(p.Elem, a.Elem, sub)
		case *types.Tuple:
			// `{1,2,3}` infers as Tuple positionally; `T[]` accepts each element.
			for _, e := range a.Elems {
				inferInto(p.Elem, e, sub)
			}
		}
	case *types.Map:
		if a, ok := arg.(*types.Map); ok {
			inferInto(p.Key, a.Key, sub)
			inferInto(p.Value, a.Value, sub)
		}
	case *types.Tuple:
		if a, ok := arg.(*types.Tuple); ok && len(a.Elems) == len(p.Elems) {
			for i := range p.Elems {
				inferInto(p.Elems[i], a.Elems[i], sub)
			}
		}
	case *types.Func:
		// Skip deep inference for funcs (could unify returns/params; keep simple).
	case *types.Object:
		if a, ok := arg.(*types.Object); ok {
			for name, f := range p.Fields {
				if af, ok := a.Fields[name]; ok {
					inferInto(f.Type, af.Type, sub)
				}
			}
		}
	case *types.Union:
		// If arg matches one member, infer into that member? Try each.
		for _, m := range p.Types {
			if types.IsAssignable(arg, m, types.Options{StrictNilChecks: true}) {
				inferInto(m, arg, sub)
				break
			}
		}
	}
}

func applySub(t types.Type, sub map[string]types.Type) types.Type {
	if len(sub) == 0 {
		return t
	}
	return types.Substitute(t, sub)
}

// ---------- narrowing ----------

// applyNarrowing installs shadowed (narrowed) variable types in the current scope for a branch.
func (c *Checker) applyNarrowing(cond ast.Expr, truthy bool) {
	over := c.narrowCond(cond, truthy)
	for name, t := range over {
		if sym := c.cur().lookupVar(name); sym != nil {
			// Shadow in current scope.
			c.cur().vars[name] = &Symbol{Name: name, Kind: sym.Kind, Type: t, Span: sym.Span, IsConst: sym.IsConst, IsBuiltin: sym.IsBuiltin, SameAs: symRoot(sym)}
		}
	}
}

func (c *Checker) narrowCond(cond ast.Expr, truthy bool) map[string]types.Type {
	// Strip parens and `not`.
	if p, ok := cond.(*ast.ParenExpr); ok {
		return c.narrowCond(p.E, truthy)
	}
	if u, ok := cond.(*ast.UnOpExpr); ok && u.Op == token.Not {
		return c.narrowCond(u.X, !truthy)
	}
	if b, ok := cond.(*ast.BinOpExpr); ok {
		// `x == nil` / `x ~= nil`
		if b.Op == token.Eq || b.Op == token.Ne {
			isEq := b.Op == token.Eq
			wantTruthy := isEq == truthy // e.g. `x == nil` true => x is nil.
			if id, ok := asIdent(b.L); ok && isNilLit(b.R) {
				return c.narrowNil(id, wantTruthy)
			}
			if id, ok := asIdent(b.R); ok && isNilLit(b.L) {
				return c.narrowNil(id, wantTruthy)
			}
			// Member paths (`node.next == nil`): refine the root.
			if root, fields, ok := narrowPath(b.L); ok && len(fields) > 0 && isNilLit(b.R) {
				return c.narrowNilPath(root, fields, wantTruthy)
			}
			if root, fields, ok := narrowPath(b.R); ok && len(fields) > 0 && isNilLit(b.L) {
				return c.narrowNilPath(root, fields, wantTruthy)
			}
			// `type(x) == "s"` / `~=`.
			if tn, lit, ok := asTypeofCompare(b); ok {
				isEqualOp := b.Op == token.Eq
				narrowTo := (isEqualOp == truthy)
				return c.narrowTypeof(tn, lit, narrowTo)
			}
			if root, fields, lit, ok := asTypeofPathCompare(b); ok {
				isEqualOp := b.Op == token.Eq
				narrowTo := (isEqualOp == truthy)
				return c.narrowTypeofPath(root, fields, lit, narrowTo)
			}
		}
		// `a and b`: in true branch both true; in false unknown.
		if b.Op == token.And && truthy {
			m := c.narrowCond(b.L, true)
			for k, v := range c.narrowCond(b.R, true) {
				m[k] = v
			}
			return m
		}
		if b.Op == token.Or && !truthy {
			m := c.narrowCond(b.L, false)
			for k, v := range c.narrowCond(b.R, false) {
				m[k] = v
			}
			return m
		}
	}
	return map[string]types.Type{}
}

// narrowPath decomposes a narrowable lvalue (`x`, `x.y`, `x["y"]`) into
// its root variable and field chain. Anything else reports ok=false.
func narrowPath(e ast.Expr) (root string, fields []string, ok bool) {
	switch n := e.(type) {
	case *ast.Ident:
		return n.Name, nil, true
	case *ast.ParenExpr:
		return narrowPath(n.E)
	case *ast.FieldExpr:
		r, fs, ok := narrowPath(n.Obj)
		if !ok {
			return "", nil, false
		}
		return r, append(fs, n.Field.Name), true
	case *ast.IndexExpr:
		sl, isStr := n.Key.(*ast.StringLit)
		if !isStr {
			return "", nil, false
		}
		s, _ := parseStringLiteral(sl.Literal)
		s = strings.Trim(s, `"'`)
		r, fs, ok := narrowPath(n.Obj)
		if !ok {
			return "", nil, false
		}
		return r, append(fs, s), true
	}
	return "", nil, false
}

// refineRootType rebuilds t with the member at fields narrowed by refine
// (which maps the effective member type to its narrowed form, as field
// access would observe it). Only plain objects and class instances are
// rebuilt; maps, enums, primitives, or a missing field report ok=false
// and the condition simply does not narrow.
func refineRootType(t types.Type, fields []string, refine func(types.Type) (types.Type, bool)) (types.Type, bool) {
	if len(fields) == 0 {
		return refine(t)
	}
	switch x := t.(type) {
	case *types.Union:
		var rs []types.Type
		changed := false
		for _, m := range x.Types {
			r, ok := refineRootType(m, fields, refine)
			if !ok {
				rs = append(rs, m)
				continue
			}
			changed = true
			rs = append(rs, r)
		}
		if !changed {
			return t, false
		}
		return types.NewUnion(rs), true
	case *types.Object:
		f, ok := x.Fields[fields[0]]
		if !ok {
			return t, false
		}
		ft, opt, ok := refineMemberField(f.Type, f.Optional, fields[1:], refine)
		if !ok {
			return t, false
		}
		cp := *x
		cp.Fields = make(map[string]types.Field, len(x.Fields))
		for k, v := range x.Fields {
			cp.Fields[k] = v
		}
		nf := f
		nf.Type, nf.Optional = ft, opt
		cp.Fields[fields[0]] = nf
		return &cp, true
	case *types.Class:
		cf, ok := x.Fields[fields[0]]
		if !ok {
			return t, false
		}
		ft, opt, ok := refineMemberField(cf.Type, cf.Optional, fields[1:], refine)
		if !ok {
			return t, false
		}
		cp := *x
		cp.Fields = make(map[string]types.ClassField, len(x.Fields))
		for k, v := range x.Fields {
			cp.Fields[k] = v
		}
		ncf := cf
		ncf.Type, ncf.Optional = ft, opt
		cp.Fields[fields[0]] = ncf
		return &cp, true
	}
	return t, false
}

// refineMemberField narrows one field's effective access type (adding `nil`
// for optional fields, as field access observes), folding the result back
// into (type, optional) storage form.
func refineMemberField(ft types.Type, optional bool, rest []string, refine func(types.Type) (types.Type, bool)) (types.Type, bool, bool) {
	eff := ft
	if optional {
		eff = types.NewUnion([]types.Type{ft, types.Nil})
	}
	sub, ok := refineRootType(eff, rest, refine)
	if !ok {
		return nil, false, false
	}
	if !optional {
		return sub, false, true
	}
	stripped, hadNil := types.RemoveNil(sub)
	if !hadNil {
		return sub, false, true
	}
	return stripped, true, true
}

func asIdent(e ast.Expr) (string, bool) {
	if id, ok := e.(*ast.Ident); ok {
		return id.Name, true
	}
	return "", false
}

func isNilLit(e ast.Expr) bool {
	_, ok := e.(*ast.NilLit)
	return ok
}

// matchTypeofCall matches `type(arg) == "lit"` shapes, returning the
// inspected argument expression and the literal text.
func matchTypeofCall(call, lit ast.Expr) (ast.Expr, string, bool) {
	ce, ok := call.(*ast.CallExpr)
	if !ok {
		return nil, "", false
	}
	fn, ok := ce.Func.(*ast.Ident)
	if !ok || fn.Name != "type" || len(ce.Args) != 1 {
		return nil, "", false
	}
	sl, ok := lit.(*ast.StringLit)
	if !ok {
		return nil, "", false
	}
	s, _ := parseStringLiteral(sl.Literal)
	// Strip quotes if our fallback left them.
	s = strings.Trim(s, `"'`)
	// Long-string edge: ignore.
	return ce.Args[0], s, true
}

// asTypeofCompare matches `type(x) == "lit"` forms, returning (varname, literal, true).
func asTypeofCompare(b *ast.BinOpExpr) (string, string, bool) {
	if arg, s, ok := matchTypeofCall(b.L, b.R); ok {
		if name, isId := asIdent(arg); isId {
			return name, s, true
		}
	}
	if arg, s, ok := matchTypeofCall(b.R, b.L); ok {
		if name, isId := asIdent(arg); isId {
			return name, s, true
		}
	}
	return "", "", false
}

// asTypeofPathCompare matches `type(path.to.member) == "lit"` forms.
func asTypeofPathCompare(b *ast.BinOpExpr) (string, []string, string, bool) {
	if arg, s, ok := matchTypeofCall(b.L, b.R); ok {
		if root, fields, isPath := narrowPath(arg); isPath && len(fields) > 0 {
			return root, fields, s, true
		}
	}
	if arg, s, ok := matchTypeofCall(b.R, b.L); ok {
		if root, fields, isPath := narrowPath(arg); isPath && len(fields) > 0 {
			return root, fields, s, true
		}
	}
	return "", nil, "", false
}

func (c *Checker) narrowNil(name string, isNil bool) map[string]types.Type {
	sym := c.cur().lookupVar(name)
	if sym == nil {
		return map[string]types.Type{}
	}
	t, ok := narrowNilOfType(sym.Type, isNil)
	if !ok {
		return map[string]types.Type{}
	}
	return map[string]types.Type{name: t}
}

// narrowNilOfType narrows a type by nil-ness (shared by variable and
// member narrowing).
func narrowNilOfType(cur types.Type, isNil bool) (types.Type, bool) {
	if isNil {
		return types.Nil, true
	}
	stripped, hadNil := types.RemoveNil(cur)
	if !hadNil {
		return nil, false
	}
	if stripped.Kind() == types.KNever {
		stripped = types.Never
	}
	return stripped, true
}

// narrowNilPath narrows the member at fields of the root variable.
func (c *Checker) narrowNilPath(root string, fields []string, isNil bool) map[string]types.Type {
	sym := c.cur().lookupVar(root)
	if sym == nil {
		return map[string]types.Type{}
	}
	refined, ok := refineRootType(sym.Type, fields, func(cur types.Type) (types.Type, bool) {
		return narrowNilOfType(cur, isNil)
	})
	if !ok {
		return map[string]types.Type{}
	}
	return map[string]types.Type{root: refined}
}

func (c *Checker) narrowTypeof(name, lit string, toType bool) map[string]types.Type {
	sym := c.cur().lookupVar(name)
	if sym == nil {
		return map[string]types.Type{}
	}
	t, ok := narrowTypeofOfType(sym.Type, lit, toType)
	if !ok {
		return map[string]types.Type{}
	}
	return map[string]types.Type{name: t}
}

// narrowTypeofPath narrows the member at fields of the root variable by a
// `type(...)` comparison.
func (c *Checker) narrowTypeofPath(root string, fields []string, lit string, toType bool) map[string]types.Type {
	sym := c.cur().lookupVar(root)
	if sym == nil {
		return map[string]types.Type{}
	}
	refined, ok := refineRootType(sym.Type, fields, func(cur types.Type) (types.Type, bool) {
		return narrowTypeofOfType(cur, lit, toType)
	})
	if !ok {
		return map[string]types.Type{}
	}
	return map[string]types.Type{root: refined}
}

// narrowTypeofOfType narrows a type by a `type(x)` literal comparison
// (shared by variable and member narrowing).
func narrowTypeofOfType(cur types.Type, lit string, toType bool) (types.Type, bool) {
	prim := typeofLiteralToType(lit)
	if prim == nil {
		return nil, false
	}
	if toType {
		// Intersect? If orig is union containing prim, narrow to prim (or member).
		// If orig is any, narrow to prim.
		if isAnyLike(cur) {
			return prim, true
		}
		// If prim assignable to orig, narrow to prim; else if orig assignable to prim? Keep prim? Else unreachable (Never) — still narrow to prim to surface downstream? Use prim.
		return prim, true
	}
	// Exclude: remove prim from union.
	if u, ok := cur.(*types.Union); ok {
		var rest []types.Type
		for _, m := range u.Types {
			if m.String() == prim.String() {
				continue
			}
			// Literal members of that base? e.g. orig `"a" | "b"`, prim string => all removed => Never. Keep logic: if prim is base and member is literal of base, remove.
			if litM, ok := m.(*types.Literal); ok && litM.Base.String() == prim.String() {
				continue
			}
			rest = append(rest, m)
		}
		if len(rest) != len(u.Types) {
			return types.NewUnion(rest), true
		}
		return nil, false
	}
	if cur.String() == prim.String() {
		// `type(x) ~= "string"` but x: string => unreachable else? Narrow to Never.
		return types.Never, true
	}
	return nil, false
}

func typeofLiteralToType(s string) types.Type {
	switch s {
	case "nil":
		return types.Nil
	case "boolean":
		return types.Boolean
	case "number":
		return types.Number
	case "string":
		return types.String
	case "table":
		return types.Table
	case "function":
		return types.Function
	case "thread":
		return types.Thread
	case "userdata":
		return types.Userdata
	}
	return nil
}

// ---------- helpers ----------

func isAnyLike(t types.Type) bool {
	return t.Kind() == types.KAny || t.Kind() == types.KUnknown
}

// unwrapGeneric returns the constraint for `T extends C`, or Any for unconstrained params
// (permissive to avoid false positives on generic code).
func unwrapGeneric(t types.Type) (types.Type, bool) {
	if g, ok := t.(*types.GenericParam); ok {
		if g.Constraint != nil {
			return g.Constraint, true
		}
		return types.Any, true
	}
	return t, false
}

func isNumberLike(t types.Type) bool {
	if u, ok := unwrapGeneric(t); ok {
		return isNumberLike(u)
	}
	switch x := t.(type) {
	case *types.Primitive:
		return x.K == types.KNumber || x.K == types.KInteger || x.K == types.KFloat
	case *types.Literal:
		return x.Base == types.Number || x.Base == types.Integer || x.Base == types.Float
	case *types.Union:
		for _, m := range x.Types {
			if !isNumberLike(m) {
				return false
			}
		}
		return true
	case *types.Enum:
		return isNumberLike(x.Underlying)
	}
	return isAnyLike(t)
}

func isIntegerLike(t types.Type) bool {
	if u, ok := unwrapGeneric(t); ok {
		return isIntegerLike(u)
	}
	switch x := t.(type) {
	case *types.Primitive:
		return x.K == types.KInteger
	case *types.Literal:
		return x.Base == types.Integer
	case *types.Union:
		for _, m := range x.Types {
			if !isIntegerLike(m) {
				return false
			}
		}
		return true
	}
	return isAnyLike(t)
}

func isConcatable(t types.Type) bool {
	if isAnyLike(t) {
		return true
	}
	if u, ok := unwrapGeneric(t); ok {
		return isConcatable(u)
	}
	switch x := t.(type) {
	case *types.Primitive:
		return x.K == types.KString || x.K == types.KNumber || x.K == types.KInteger || x.K == types.KFloat
	case *types.Literal:
		return x.Base == types.String || x.Base == types.Number || x.Base == types.Integer || x.Base == types.Float
	case *types.Union:
		for _, m := range x.Types {
			if !isConcatable(m) {
				return false
			}
		}
		return true
	case *types.Enum:
		return isConcatable(x.Underlying)
	}
	return false
}

func isComparable(l, r types.Type) bool {
	if isAnyLike(l) || isAnyLike(r) {
		return true
	}
	if _, ok := l.(*types.GenericParam); ok {
		return true
	}
	if _, ok := r.(*types.GenericParam); ok {
		return true
	}
	// Numbers compare with numbers, strings with strings.
	if isNumberLike(l) && isNumberLike(r) {
		return true
	}
	if isStringLike(l) && isStringLike(r) {
		return true
	}
	// Literals of same base.
	if l.String() == r.String() {
		return true
	}
	return false
}

func isStringLike(t types.Type) bool {
	if u, ok := unwrapGeneric(t); ok {
		return isStringLike(u)
	}
	switch x := t.(type) {
	case *types.Primitive:
		return x.K == types.KString
	case *types.Literal:
		return x.Base == types.String
	case *types.Union:
		for _, m := range x.Types {
			if !isStringLike(m) {
				return false
			}
		}
		return true
	case *types.Enum:
		return isStringLike(x.Underlying)
	}
	return isAnyLike(t)
}

func isLengthable(t types.Type) bool {
	if isAnyLike(t) {
		return true
	}
	if u, ok := unwrapGeneric(t); ok {
		return isLengthable(u)
	}
	switch x := t.(type) {
	case *types.Primitive:
		return x.K == types.KString || x.K == types.KTable
	case *types.Array, *types.Map, *types.Object, *types.Tuple:
		return true
	case *types.Literal:
		return x.Base == types.String
	case *types.Union:
		for _, m := range x.Types {
			if !isLengthable(m) {
				return false
			}
		}
		return true
	}
	return false
}

func lookupField(ot types.Type, name string) (types.Field, bool) {
	switch x := ot.(type) {
	case *types.Object:
		if f, ok := x.Fields[name]; ok {
			return f, true
		}
		// Indexer with string key?
		for _, ix := range x.Indexers {
			if ix.Key.Kind() == types.KString || ix.Key.Kind() == types.KAny {
				return types.Field{Type: ix.Value}, true
			}
			if lit, ok := ix.Key.(*types.Literal); ok && lit.Base == types.String {
				return types.Field{Type: ix.Value}, true
			}
		}
		// Enum runtime tables are Objects; handled above.
		return types.Field{}, false
	case *types.Enum:
		// Enum value access returns member literal.
		if m, ok := x.Members[name]; ok {
			return types.Field{Type: m}, true
		}
		return types.Field{}, false
	case *types.Map:
		// `m.field` where m: map<K,V>: allowed if K accepts strings.
		if types.IsAssignable(types.String, x.Key, types.Options{StrictNilChecks: true}) || isAnyLike(x.Key) {
			return types.Field{Type: x.Value}, true
		}
		return types.Field{}, false
	case *types.Class:
		// Instance members (flattened to include inherited, minus privates).
		if f, ok := x.Fields[name]; ok {
			return types.Field{Type: f.Type, Optional: f.Optional, Readonly: f.Readonly, Access: f.Access, Owner: f.Owner}, true
		}
		return types.Field{}, false
	case *types.ClassValue:
		if f, ok := x.Statics[name]; ok {
			return types.Field{Type: f.Type, Optional: f.Optional, Readonly: f.Readonly, Access: f.Access, Owner: f.Owner}, true
		}
		if name == "new" {
			if x.Constructor == nil {
				return types.Field{Type: types.Any, Access: x.CtorAccess, Owner: classDefOf(x)}, true
			}
			return types.Field{Type: &types.Func{Params: x.Constructor.Params, HasVarArg: x.Constructor.HasVarArg, VarArg: x.Constructor.VarArg, Returns: []types.Type{x.Class}, TypeParams: x.Class.TypeParams}, Access: x.CtorAccess, Owner: classDefOf(x)}, true
		}
		return types.Field{}, false
	case *types.Primitive:
		if x.K == types.KTable {
			return types.Field{Type: types.Any}, true
		}
	}
	return types.Field{}, false
}

func classDefOf(v *types.ClassValue) *types.ClassDef {
	if v == nil || v.Class == nil {
		return nil
	}
	return v.Class.Def
}

func indexResult(ot, kt types.Type) types.Type {
	if isAnyLike(ot) || isAnyLike(kt) {
		if isAnyLike(ot) {
			return types.Any
		}
		// ot concrete, kt any => best effort.
		switch x := ot.(type) {
		case *types.Array:
			return x.Elem
		case *types.Map:
			return x.Value
		case *types.Object:
			if len(x.Indexers) > 0 {
				return x.Indexers[0].Value
			}
			return types.Any
		case *types.Tuple:
			return types.NewUnion(x.Elems)
		case *types.Primitive:
			if x.K == types.KString {
				// string[i]? Lua 5.4 no, but allow any? Return string? Actually string indexing not valid; return nil.
				return nil
			}
			if x.K == types.KTable {
				return types.Any
			}
		}
		return types.Any
	}
	switch x := ot.(type) {
	case *types.Array:
		if isIntegerLike(kt) || isNumberLike(kt) {
			return x.Elem
		}
		return nil
	case *types.Map:
		return x.Value
	case *types.Tuple:
		if lit, ok := kt.(*types.Literal); ok {
			if idx, err := strconv.Atoi(lit.Value); err == nil {
				if idx >= 1 && idx <= len(x.Elems) {
					return x.Elems[idx-1]
				}
			}
		}
		return nil
	case *types.Object:
		// Literal string/number keys map to fields.
		if lit, ok := kt.(*types.Literal); ok {
			key := strings.Trim(lit.Value, `"'`)
			// Number literal for array-like indexers.
			if lit.Base == types.Number || lit.Base == types.Integer || lit.Base == types.Float {
				for _, ix := range x.Indexers {
					if ix.Key.Kind() == types.KInteger || ix.Key.Kind() == types.KNumber {
						return ix.Value
					}
				}
				if f, ok := x.Fields[key]; ok {
					return f.Type
				}
				return nil
			}
			if f, ok := x.Fields[key]; ok {
				if f.Optional {
					return types.NewUnion([]types.Type{f.Type, types.Nil})
				}
				return f.Type
			}
			for _, ix := range x.Indexers {
				if ix.Key.Kind() == types.KString || ix.Key.Kind() == types.KAny {
					return ix.Value
				}
			}
			return nil
		}
		for _, ix := range x.Indexers {
			return ix.Value
		}
		return nil
	case *types.Primitive:
		if x.K == types.KTable {
			return types.Any
		}
		if x.K == types.KString {
			return nil
		}
	}
	return nil
}

// ---------- classes ----------

func classParamNames(gs []*ast.GenericParam) []string {
	var out []string
	for _, g := range gs {
		out = append(out, g.Name.Name)
	}
	return out
}

// isIdentityClassArgs reports whether resolved args are exactly the entry's
// own type parameters (self-reference to the current instantiation).
func isIdentityClassArgs(e *typeEntry, sargs []types.Type) bool {
	if len(sargs) != len(e.params) {
		return len(sargs) == 0 && len(e.params) == 0
	}
	for i, p := range e.params {
		gp, ok := sargs[i].(*types.GenericParam)
		if !ok || gp.Name != p.Name.Name {
			return false
		}
	}
	return true
}

// classSubFromTypes builds the param->arg substitution with arity,
// default and constraint diagnostics.
func (c *Checker) classSubFromTypes(e *typeEntry, tys []types.Type, span token.Span) map[string]types.Type {
	sub := map[string]types.Type{}
	if len(tys) > len(e.params) {
		c.errorf("TL3107", span, "type '%s' expects %d argument(s), got %d", e.name, len(e.params), len(tys))
	}
	for i, p := range e.params {
		var a types.Type
		if i < len(tys) {
			a = tys[i]
		} else if p.Default != nil {
			a = c.resolveType(p.Default)
		} else {
			a = types.Unknown
		}
		if p.Constraint != nil {
			con := c.resolveType(p.Constraint)
			if !types.IsAssignable(a, con, c.opt.toTypes()) {
				c.errorf("TL3108", span, "type argument '%s' does not satisfy constraint '%s'", a.String(), con.String())
			}
		}
		sub[p.Name.Name] = a
	}
	return sub
}

func (c *Checker) classSubFromNodes(e *typeEntry, nodes []ast.TypeNode, span token.Span) map[string]types.Type {
	var tys []types.Type
	for _, n := range nodes {
		tys = append(tys, c.resolveType(n))
	}
	return c.classSubFromTypes(e, tys, span)
}

func (c *Checker) currentClass() *classCtx {
	if len(c.classStack) == 0 {
		return nil
	}
	return c.classStack[len(c.classStack)-1]
}

// checkAccess enforces private/protected visibility. Public (or non-class)
// members are always accessible.
func (c *Checker) checkAccess(acc types.Access, owner *types.ClassDef, sp token.Span, what string) bool {
	if acc == types.Public || owner == nil {
		return true
	}
	cur := c.currentClass()
	if cur == nil {
		c.errorf("TL3310", sp, "%s is %s and cannot be accessed outside the class body", what, acc)
		return false
	}
	if acc == types.Private {
		if cur.def == owner {
			return true
		}
		c.errorf("TL3310", sp, "%s is private to class '%s'", what, owner.Name)
		return false
	}
	if cur.def == owner || isSubclassOf(cur.inst, owner) {
		return true
	}
	c.errorf("TL3310", sp, "%s is protected and cannot be accessed here", what)
	return false
}

func isSubclassOf(inst *types.Class, owner *types.ClassDef) bool {
	seen := map[*types.Class]bool{}
	for cl := inst; cl != nil; cl = cl.Base {
		if seen[cl] {
			return false
		}
		seen[cl] = true
		if cl.Def == owner {
			return true
		}
	}
	return false
}

// ensureClassDef builds (once) the checked definition form of a class:
// instance shape with generic placeholders intact for call-site inference.
func (c *Checker) ensureClassDef(e *typeEntry) *classBuild {
	if e.classBuild != nil {
		return e.classBuild
	}
	if e.classDecl == nil || e.resolving {
		return nil
	}
	e.resolving = true
	if e.classDef == nil {
		e.classDef = &types.ClassDef{Name: e.name}
	}
	b := c.buildClassDef(e)
	e.resolving = false
	for _, p := range e.classPatches {
		p()
	}
	e.classPatches = nil
	if b != nil {
		e.classBuild = b
	}
	return b
}

// resolveBaseRef resolves an `extends` clause to an instantiated base
// instance plus its (substituted) class value for statics/constructor.
func (c *Checker) resolveBaseRef(node ast.TypeNode, selfName string) (*types.Class, *types.ClassValue) {
	nt, ok := node.(*ast.NamedType)
	if !ok || strings.Contains(nt.Name, ".") {
		c.errorf("TL3131", node.Span(), "extends must name a class (e.g. `extends Animal`)")
		return nil, nil
	}
	e := c.cur().lookupType(nt.Name)
	if e == nil || e.classDecl == nil {
		bt := c.resolveType(node)
		if _, ok := bt.(*types.Class); !ok && !isAnyLike(bt) {
			c.errorf("TL3131", node.Span(), "class can only extend another class, got '%s'", bt.String())
		}
		return nil, nil
	}
	def := c.ensureClassDef(e)
	if def == nil {
		c.errorf("TL3132", node.Span(), "cyclic inheritance involving '%s'", nt.Name)
		return nil, nil
	}
	sub := c.classSubFromNodes(e, nt.Args, node.Span())
	inst, _ := types.Substitute(def.inst, sub).(*types.Class)
	val, _ := types.Substitute(def.val, sub).(*types.ClassValue)
	if inst.Def == e.classDef && e.name == selfName && len(nt.Args) == 0 {
		// Direct self-extension is reported by the caller chain walk;
		// keep the reference so the walk can see it.
	}
	return inst, val
}

// classMemberSig is the resolved signature of one member (pass 1 output).
type classMemberSig struct {
	member    *ast.ClassMember
	fieldType types.Type // fields: declared type (nil => infer in pass 2)
	funcType  *types.Func
}

func astVisibility(v ast.Visibility) types.Access {
	switch v {
	case ast.Private:
		return types.Private
	case ast.Protected:
		return types.Protected
	}
	return types.Public
}

// buildClassDef resolves signatures (no bodies): base chain, flattened
// members, statics, constructor, override and implements checks.
func (c *Checker) buildClassDef(e *typeEntry) *classBuild {
	n := e.classDecl
	def := e.classDef
	c.pushTParams(n.Generics)
	defer c.popTParams()

	shell := &types.Class{Def: def, Name: n.Name.Name, Params: classParamNames(n.Generics), Fields: map[string]types.ClassField{}, IsAbstract: n.Abstract}
	// Definition-form args are the params themselves so substitution
	// (`Box.new(v)` inside `static wrap<U>`) tracks instantiations.
	for _, pn := range shell.Params {
		shell.Args = append(shell.Args, &types.GenericParam{Name: pn})
	}
	// Resolved declarations for explicit-argument checking on `new`.
	for _, g := range n.Generics {
		tp := &types.GenericParam{Name: g.Name.Name}
		if g.Constraint != nil {
			tp.Constraint = c.resolveType(g.Constraint)
		}
		if g.Default != nil {
			tp.Default = c.resolveType(g.Default)
		}
		shell.TypeParams = append(shell.TypeParams, tp)
	}
	e.classShell = shell

	// Duplicate and reserved member names.
	seen := map[string]bool{}
	processed := map[string]bool{}
	var ctorMember *ast.ClassMember
	for _, m := range n.Members {
		if seen[m.Name.Name] {
			c.errorf("TL3320", m.Span(), "duplicate class member '%s'", m.Name.Name)
			continue
		}
		seen[m.Name.Name] = true
		if m.Kind == ast.ClassConstructor {
			if ctorMember != nil {
				c.errorf("TL3321", m.Span(), "duplicate constructor")
				continue
			}
			ctorMember = m
		}
		if m.Name.Name == "new" || m.Name.Name == "__index" {
			c.errorf("TL3322", m.Name.Span(), "'%s' is reserved by the class runtime", m.Name.Name)
			continue
		}
		if m.Kind == ast.ClassField && m.Name.Name == "constructor" {
			c.errorf("TL3322", m.Name.Span(), "'constructor' must be declared as a method")
			continue
		}
		if m.Kind == ast.ClassConstructor && m.IsStatic {
			c.errorf("TL3323", m.Span(), "constructor cannot be 'static'")
		}
		if m.Kind == ast.ClassConstructor && m.IsReadonly {
			c.errorf("TL3323", m.Span(), "constructor cannot be 'readonly'")
		}
		if m.Kind == ast.ClassConstructor && m.IsAbstract {
			c.errorf("TL3323", m.Span(), "constructor cannot be 'abstract'")
		}
		if m.Kind == ast.ClassMethod && m.IsReadonly {
			c.errorf("TL3323", m.Span(), "methods cannot be 'readonly'")
		}
		if m.IsAbstract && m.Kind == ast.ClassField {
			// Parser already reports TL2037; don't cascade.
		} else if m.IsAbstract && !n.Abstract && !n.Declared {
			c.errorf("TL3324", m.Span(), "abstract member '%s' requires an 'abstract class'", m.Name.Name)
		}
		if m.IsAbstract && m.Visibility == ast.Private {
			c.errorf("TL3324", m.Span(), "abstract member '%s' cannot be private", m.Name.Name)
		}
		processed[m.Name.Name] = true
	}

	// Base class.
	var baseInst *types.Class
	var baseVal *types.ClassValue
	if n.Extends != nil {
		baseInst, baseVal = c.resolveBaseRef(n.Extends, n.Name.Name)
		// Indirect cycles: walk with a visited set so a malformed chain
		// can never hang the checker.
		seenChain := map[*types.Class]bool{shell: true}
		for b := baseInst; b != nil; b = b.Base {
			if seenChain[b] {
				c.errorf("TL3130", n.Extends.Span(), "cyclic inheritance involving class '%s'", n.Name.Name)
				baseInst, baseVal = nil, nil
				break
			}
			seenChain[b] = true
			if b.Def == def {
				c.errorf("TL3130", n.Extends.Span(), "class '%s' cannot extend itself", n.Name.Name)
				baseInst, baseVal = nil, nil
				break
			}
		}
	}
	shell.Base = baseInst
	if baseInst != nil {
		for name, f := range baseInst.Fields {
			if f.Access == types.Private {
				continue
			}
			shell.Fields[name] = f
		}
	}
	if baseVal != nil && baseVal.CtorAccess == types.Private {
		c.errorf("TL3325", n.Extends.Span(), "cannot extend class '%s' with a private constructor", baseInst.Name)
	}

	statics := map[string]types.ClassField{}
	if baseVal != nil {
		for name, f := range baseVal.Statics {
			if f.Access == types.Private {
				continue
			}
			statics[name] = f
		}
	}

	sigs := map[*ast.ClassMember]*classMemberSig{}
	ownInstance := map[string]types.ClassField{}
	ownStatics := map[string]types.ClassField{}

	memberFunc := func(m *ast.ClassMember) *types.Func {
		c.pushTParams(m.Generics)
		defer c.popTParams()
		var params []types.FuncParam
		for _, p := range m.Params {
			var t types.Type = types.Any
			if p.Type != nil {
				t = c.resolveType(p.Type)
			} else if c.opt.NoImplicitAny {
				c.errorf("TL3040", p.Span(), "parameter '%s' implicitly has 'any' type", p.Name.Name)
			}
			params = append(params, types.FuncParam{Name: p.Name.Name, Type: t})
		}
		var va types.Type
		if m.HasVarArg {
			if m.VarArgType != nil {
				va = c.resolveType(m.VarArgType)
				if arr, ok := va.(*types.Array); ok {
					va = arr.Elem
				}
			} else {
				va = types.Any
			}
		}
		var rets []types.Type
		if m.ReturnType != nil {
			rets = flattenReturns(c.resolveType(m.ReturnType))
			if rets == nil {
				rets = []types.Type{}
			}
		} else {
			rets = []types.Type{types.Any}
		}
		tps := make([]*types.GenericParam, len(m.Generics))
		for i, g := range m.Generics {
			tp := &types.GenericParam{Name: g.Name.Name}
			if g.Constraint != nil {
				tp.Constraint = c.resolveType(g.Constraint)
			}
			if g.Default != nil {
				tp.Default = c.resolveType(g.Default)
			}
			tps[i] = tp
		}
		return &types.Func{Params: params, HasVarArg: m.HasVarArg, VarArg: va, Returns: rets, TypeParams: tps}
	}

	for _, m := range n.Members {
		if !processed[m.Name.Name] {
			continue
		}
		// Only the first declaration of a name counts (dupes reported above).
		if _, done := sigs[m]; done {
			continue
		}
		// Mark first occurrence: find it by pointer identity against members.
		first := true
		for _, o := range n.Members {
			if o == m {
				break
			}
			if o.Name.Name == m.Name.Name {
				first = false
				break
			}
		}
		if !first {
			continue
		}
		acc := astVisibility(m.Visibility)
		switch m.Kind {
		case ast.ClassField:
			var ft types.Type
			if m.FieldType != nil {
				ft = c.resolveType(m.FieldType)
			}
			cf := types.ClassField{Type: ft, Readonly: m.IsReadonly, Access: acc, Owner: def}
			sigs[m] = &classMemberSig{member: m, fieldType: ft}
			if m.IsStatic {
				statics[m.Name.Name] = cf
				ownStatics[m.Name.Name] = cf
			} else {
				shell.Fields[m.Name.Name] = cf
				ownInstance[m.Name.Name] = cf
			}
		case ast.ClassMethod:
			ft := memberFunc(m)
			cf := types.ClassField{Type: ft, Access: acc, IsMethod: true, Abstract: m.IsAbstract, Owner: def}
			sigs[m] = &classMemberSig{member: m, funcType: ft}
			if m.IsStatic {
				statics[m.Name.Name] = cf
				ownStatics[m.Name.Name] = cf
			} else {
				shell.Fields[m.Name.Name] = cf
				ownInstance[m.Name.Name] = cf
			}
		case ast.ClassConstructor:
			sigs[m] = &classMemberSig{member: m, funcType: memberFunc(m)}
		}
	}

	// Override compatibility against the direct base.
	if baseInst != nil {
		for name, sub := range ownInstance {
			if bf, ok := baseInst.Fields[name]; ok && bf.Access != types.Private {
				checkMemberOverride(c, ownMemberSpan(n, name, false), name, sub, bf)
			}
		}
	}
	if baseVal != nil {
		for name, sub := range ownStatics {
			if bf, ok := baseVal.Statics[name]; ok && bf.Access != types.Private {
				checkMemberOverride(c, ownMemberSpan(n, name, true), name, sub, bf)
			}
		}
	}

	// Abstract members must be implemented by concrete classes.
	// (Present-but-incompatible members are already reported by the
	// override check above, so only missing/still-abstract ones remain.)
	if !n.Abstract && !n.Declared && baseInst != nil {
		for name, bf := range baseInst.Fields {
			if !bf.Abstract || bf.Access == types.Private {
				continue
			}
			sub, ok := ownInstance[name]
			if !ok || sub.Abstract {
				c.errorf("TL3351", n.Name.Span(), "class '%s' must implement abstract member '%s'", n.Name.Name, name)
			}
		}
	}

	// Implements: structural check of public members.
	for _, impl := range n.Implements {
		t := c.resolveType(impl)
		io, ok := t.(*types.Object)
		if !ok {
			if !isAnyLike(t) {
				c.errorf("TL3352", impl.Span(), "class can only implement an interface or object type, got '%s'", t.String())
			}
			continue
		}
		if !types.IsAssignable(shell.ObjectView(), io, c.opt.toTypes()) {
			c.errorf("TL3352", impl.Span(), "class '%s' does not implement '%s'", n.Name.Name, t.String())
		}
	}

	// Constructor value (always present so `Base.constructor` exists).
	var ctorFunc *types.Func
	ctorAccess := types.Public
	if ctorMember != nil {
		ctorFunc = sigs[ctorMember].funcType
		ctorAccess = astVisibility(ctorMember.Visibility)
		// Constructor params are the value; returns are unused.
		ctorFunc = &types.Func{Params: ctorFunc.Params, HasVarArg: ctorFunc.HasVarArg, VarArg: ctorFunc.VarArg}
	} else if n.Declared {
		ctorFunc = &types.Func{HasVarArg: true, VarArg: types.Any}
	} else if baseVal != nil && baseVal.Constructor != nil {
		ctorFunc = baseVal.Constructor
		ctorAccess = baseVal.CtorAccess
	} else {
		ctorFunc = &types.Func{}
	}

	val := &types.ClassValue{Class: shell, Constructor: ctorFunc, CtorAccess: ctorAccess, Statics: statics, Abstract: n.Abstract}
	return &classBuild{inst: shell, val: val, sigs: sigs}
}

func accessRank(a types.Access) int {
	switch a {
	case types.Public:
		return 2
	case types.Protected:
		return 1
	}
	return 0
}

func checkMemberOverride(c *Checker, sp token.Span, name string, sub, base types.ClassField) {
	kind := "field"
	if sub.IsMethod {
		kind = "method"
	}
	if sub.IsMethod != base.IsMethod {
		c.errorf("TL3350", sp, "cannot override %s '%s' with a %s", fieldKind(base), name, kind)
		return
	}
	if sub.Type != nil && base.Type != nil && !types.IsAssignable(sub.Type, base.Type, c.opt.toTypes()) {
		c.errorf("TL3350", sp, "%s '%s' is incompatible with the base declaration", kind, name)
	}
	if accessRank(sub.Access) < accessRank(base.Access) {
		c.errorf("TL3350", sp, "%s '%s' cannot reduce visibility to %s", kind, name, sub.Access)
	}
}

// ownMemberSpan finds the declaration span of an own member for diagnostics.
func ownMemberSpan(n *ast.ClassDecl, name string, isStatic bool) token.Span {
	for _, m := range n.Members {
		if m.Name.Name == name && m.IsStatic == isStatic && m.Kind != ast.ClassConstructor {
			return m.Span()
		}
	}
	return n.Span()
}

func fieldKind(f types.ClassField) string {
	if f.IsMethod {
		return "method"
	}
	return "field"
}

func (c *Checker) checkClass(n *ast.ClassDecl) {
	e := c.cur().lookupType(n.Name.Name)
	if e == nil || e.classDecl != n {
		return // duplicate already reported
	}
	def := c.ensureClassDef(e)
	if def == nil {
		c.recordDefEntry(n.Name.Span(), c.bindVar(n.Name.Name, &Symbol{Name: n.Name.Name, Kind: symVar, Type: types.Any, Span: n.Span()}), e)
		return
	}
	if existing := c.cur().vars[n.Name.Name]; existing != nil && !existing.IsBuiltin {
		c.errorf("TL3011", n.Name.Span(), "redefinition of local '%s'", n.Name.Name)
	}
	c.recordDefEntry(n.Name.Span(), c.bindVar(n.Name.Name, &Symbol{Name: n.Name.Name, Kind: symVar, Type: def.val, Span: n.Span()}), e)
	for _, m := range n.Members {
		if m.Name == nil || m.Kind == ast.ClassConstructor {
			continue
		}
		kind := "field"
		if m.Kind == ast.ClassMethod {
			kind = "method"
		}
		c.recordMemberDef(m.Name.Span(), e, m.Name.Name, kind)
	}
	if !n.Declared {
		c.checkClassBodies(e, n, def)
	}
}

// checkClassBodies is pass 2: field initializers (inferring unannotated
// fields) and method/constructor bodies with self/super in scope.
func (c *Checker) checkClassBodies(e *typeEntry, n *ast.ClassDecl, def *classBuild) {
	c.pushTParams(n.Generics)
	defer c.popTParams()

	baseInst := def.inst.Base
	var baseVal *types.ClassValue
	if n.Extends != nil {
		// Already resolved during build; re-derive cheaply for context.
		if nt, ok := n.Extends.(*ast.NamedType); ok {
			if be := c.cur().lookupType(nt.Name); be != nil && be.classDecl != nil {
				if bd := c.ensureClassDef(be); bd != nil {
					sub := c.classSubFromNodes(be, nt.Args, n.Extends.Span())
					if v, ok := types.Substitute(bd.val, sub).(*types.ClassValue); ok {
						baseVal = v
					}
				}
			}
		}
	}

	// Field initializers (statics have no self; instance fields do).
	for _, m := range n.Members {
		if m.Kind != ast.ClassField || m.Init == nil {
			continue
		}
		sig := def.sigs[m]
		if sig == nil {
			continue
		}
		if m.IsStatic {
			got := c.checkExpr(m.Init)
			if sig.fieldType != nil {
				if !types.IsAssignable(got, sig.fieldType, c.opt.toTypes()) {
					c.errorf("TL3010", m.Init.Span(), "type '%s' is not assignable to type '%s'", got.String(), sig.fieldType.String())
				}
			} else {
				ft := widenInferred(got)
				sig.fieldType = ft
				if entry, ok := def.val.Statics[m.Name.Name]; ok {
					entry.Type = ft
					def.val.Statics[m.Name.Name] = entry
				}
			}
			continue
		}
		c.push()
		c.cur().vars["self"] = &Symbol{Name: "self", Kind: symParam, Type: def.inst, Span: m.Span()}
		c.classStack = append(c.classStack, &classCtx{def: def.inst.Def, name: n.Name.Name, inst: def.inst, base: baseInst, baseVal: baseVal})
		got := c.checkExpr(m.Init)
		want := sig.fieldType
		if want == nil && baseInst != nil {
			// Overriding an inherited field: the base type governs.
			if bf, ok := baseInst.Fields[m.Name.Name]; ok && bf.Access != types.Private && bf.Type != nil {
				want = bf.Type
			}
		}
		if want != nil {
			if !types.IsAssignable(got, want, c.opt.toTypes()) {
				c.errorf("TL3010", m.Init.Span(), "type '%s' is not assignable to type '%s'", got.String(), want.String())
			}
		} else {
			ft := widenInferred(got)
			sig.fieldType = ft
			def.inst.Fields[m.Name.Name] = types.ClassField{Type: ft, Readonly: m.IsReadonly, Access: astVisibility(m.Visibility), Owner: def.inst.Def}
		}
		c.classStack = c.classStack[:len(c.classStack)-1]
		c.pop()
	}

	// Method and constructor bodies.
	for _, m := range n.Members {
		if m.Kind == ast.ClassField || m.Body == nil {
			continue
		}
		sig := def.sigs[m]
		if sig == nil || sig.funcType == nil {
			continue
		}
		c.checkClassMethodBody(n, m, sig.funcType, def, baseInst, baseVal)
	}

	// Constructor rules (explicit constructors only; synthesized ones
	// forward to base by construction).
	var ctor *ast.ClassMember
	for _, m := range n.Members {
		if m.Kind == ast.ClassConstructor && m.Body != nil {
			ctor = m
			break
		}
	}
	required := []string{}
	for _, m := range n.Members {
		if m.Kind != ast.ClassField || m.IsStatic || m.Init != nil {
			continue
		}
		if sig := def.sigs[m]; sig != nil && sig.fieldType == types.Nil {
			continue // `x: nil` is trivially satisfied
		}
		required = append(required, m.Name.Name)
	}
	if ctor == nil {
		for _, name := range required {
			c.errorf("TL3340", n.Name.Span(), "field '%s' needs an initializer or an explicit constructor", name)
		}
	} else {
		assigned := map[string]bool{}
		collectSelfAssigns(ctor.Body, assigned)
		for _, name := range required {
			if !assigned[name] {
				c.errorf("TL3340", ctor.Span(), "constructor does not assign required field '%s'", name)
			}
		}
		if baseInst != nil && baseNeedsConstruction(e, baseInst, baseVal) {
			if !blockHasSuperCall(ctor.Body) {
				c.errorf("TL3342", ctor.Span(), "constructor of derived class '%s' must call 'super(...)'", n.Name.Name)
			} else if idx := topLevelSuperIndex(ctor.Body); idx >= 0 {
				for i, st := range ctor.Body.Stmts {
					if i >= idx {
						break
					}
					if stmtUsesSelf(st) {
						c.errorf("TL3343", st.Span(), "cannot use 'self' before 'super(...)'")
						break
					}
				}
			}
		}
	}
}

// baseNeedsConstruction reports whether a derived constructor must call super().
func baseNeedsConstruction(e *typeEntry, baseInst *types.Class, baseVal *types.ClassValue) bool {
	_ = e
	if baseVal != nil && baseVal.Constructor != nil {
		for _, p := range baseVal.Constructor.Params {
			if !p.Optional && !types.ContainsNil(p.Type) {
				return true
			}
		}
		if baseVal.Constructor.HasVarArg {
			return false
		}
		// Explicit non-trivial constructor still needs the call so its body runs.
		return len(baseVal.Constructor.Params) > 0
	}
	return false
}

// checkClassMethodBody checks one method/constructor body with self/super.
func (c *Checker) checkClassMethodBody(n *ast.ClassDecl, m *ast.ClassMember, ft *types.Func, def *classBuild, baseInst *types.Class, baseVal *types.ClassValue) {
	isCtor := m.Kind == ast.ClassConstructor
	c.pushTParams(n.Generics)
	defer c.popTParams()
	c.pushTParams(m.Generics)
	defer c.popTParams()
	c.push()
	defer c.pop()
	c.labelStack = append(c.labelStack, map[string]token.Span{})
	defer func() { c.labelStack = c.labelStack[:len(c.labelStack)-1] }()
	c.gotoList = append(c.gotoList, nil)
	defer func() { c.gotoList = c.gotoList[:len(c.gotoList)-1] }()

	for _, p := range m.Params {
		if p.Name.Name == "self" || p.Name.Name == "super" {
			c.errorf("TL3341", p.Name.Span(), "parameter cannot be named '%s' in a method", p.Name.Name)
		}
		var t types.Type = types.Any
		for _, fp := range ft.Params {
			if fp.Name == p.Name.Name {
				t = fp.Type
				break
			}
		}
		c.recordDef(p.Name.Span(), c.bindVar(p.Name.Name, &Symbol{Name: p.Name.Name, Kind: symParam, Type: t, Span: p.Name.Span()}))
	}
	if !m.IsStatic {
		c.cur().vars["self"] = &Symbol{Name: "self", Kind: symParam, Type: def.inst, Span: m.Span()}
	}
	if m.HasVarArg {
		if m.VarArgName != nil {
			restT := types.Type(&types.Array{Elem: types.Any})
			if ft.VarArg != nil {
				restT = &types.Array{Elem: ft.VarArg}
			}
			c.recordDef(m.VarArgName.Span(), c.bindVar(m.VarArgName.Name, &Symbol{Name: m.VarArgName.Name, Kind: symParam, Type: restT, Span: m.VarArgName.Span()}))
		}
		c.varargStack = append(c.varargStack, ft.VarArg)
		if ft.VarArg == nil {
			c.varargStack[len(c.varargStack)-1] = types.Any
		}
	} else {
		c.varargStack = append(c.varargStack, nil)
	}
	defer func() { c.varargStack = c.varargStack[:len(c.varargStack)-1] }()

	if isCtor {
		c.retStack = append(c.retStack, []types.Type{})
	} else if m.ReturnType != nil {
		c.retStack = append(c.retStack, ft.Returns)
	} else {
		c.retStack = append(c.retStack, nil)
	}
	defer func() { c.retStack = c.retStack[:len(c.retStack)-1] }()
	c.inferStack = append(c.inferStack, nil)
	defer func() { c.inferStack = c.inferStack[:len(c.inferStack)-1] }()

	c.classStack = append(c.classStack, &classCtx{def: def.inst.Def, name: n.Name.Name, inst: def.inst, base: baseInst, baseVal: baseVal, inStatic: m.IsStatic, inConstructor: isCtor, retDepth: len(c.retStack)})
	defer func() { c.classStack = c.classStack[:len(c.classStack)-1] }()

	c.checkBlock(m.Body)
	gotos := c.gotoList[len(c.gotoList)-1]
	labels := c.labelStack[len(c.labelStack)-1]
	for _, g := range gotos {
		if _, ok := labels[g.name]; !ok {
			c.errorf("TL3050", g.span, "undefined label '%s'", g.name)
		}
	}
}

// checkClassInstantiate checks `Point(...)` / `Point.new(...)`.
func (c *Checker) checkClassInstantiate(cv *types.ClassValue, args []ast.Expr, span token.Span, explicit []types.Type) []types.Type {
	name := "class"
	if cv.Class != nil && cv.Class.Name != "" {
		name = "'" + cv.Class.Name + "'"
	}
	if cv.Abstract {
		c.errorf("TL3330", span, "cannot instantiate abstract class %s", name)
		// Don't cascade arity noise on top of the root cause.
		for _, a := range args {
			c.checkExpr(a)
		}
		if cv.Class != nil {
			return []types.Type{cv.Class}
		}
		return []types.Type{types.Any}
	}
	if cv.CtorAccess != types.Public {
		cur := c.currentClass()
		allowed := false
		if cur != nil && cv.Class != nil {
			if cv.CtorAccess == types.Private && cur.def == cv.Class.Def {
				allowed = true
			}
			if cv.CtorAccess == types.Protected && (cur.def == cv.Class.Def || isSubclassOf(cur.inst, cv.Class.Def)) {
				allowed = true
			}
		}
		if !allowed {
			c.errorf("TL3331", span, "constructor of %s is %s", name, cv.CtorAccess)
		}
	}
	if cv.Constructor == nil {
		for _, a := range args {
			c.checkExpr(a)
		}
		if cv.Class != nil {
			return []types.Type{cv.Class}
		}
		return []types.Type{types.Any}
	}
	fn := &types.Func{Params: cv.Constructor.Params, HasVarArg: cv.Constructor.HasVarArg, VarArg: cv.Constructor.VarArg, Returns: []types.Type{cv.Class}, TypeParams: cv.Class.TypeParams}
	return c.checkCall(span, fn, args, span, explicit)
}

// checkSuperCall checks `super(...)` (base constructor on self).
func (c *Checker) checkSuperCall(n *ast.CallExpr) []types.Type {
	if len(n.TypeArgs) > 0 {
		c.errorf("TL3234", n.Span(), "explicit type arguments are not allowed on 'super(...)'")
	}
	ctx := c.currentClass()
	if ctx == nil || ctx.base == nil || len(c.retStack) != ctx.retDepth {
		c.errorf("TL3301", n.Span(), "'super' can only be used inside methods of a derived class")
		for _, a := range n.Args {
			c.checkExpr(a)
		}
		return []types.Type{types.Any}
	}
	if ctx.inStatic {
		c.errorf("TL3302", n.Span(), "'super' cannot be used in a static method (use the base class name)")
		for _, a := range n.Args {
			c.checkExpr(a)
		}
		return []types.Type{types.Any}
	}
	if !ctx.inConstructor {
		c.errorf("TL3302", n.Span(), "'super(...)' can only be called in a constructor (use 'super:method()' in methods)")
		for _, a := range n.Args {
			c.checkExpr(a)
		}
		return []types.Type{types.Any}
	}
	baseCtor := ctx.baseVal.Constructor
	if baseCtor == nil {
		for _, a := range n.Args {
			c.checkExpr(a)
		}
		return []types.Type{types.Void}
	}
	c.checkCall(n.Span(), baseCtor, n.Args, n.Span(), nil)
	return []types.Type{types.Void}
}

// checkSuperMethod checks `super:method(...)` (base method on self).
func (c *Checker) checkSuperMethod(n *ast.MethodCallExpr) []types.Type {
	ctx := c.currentClass()
	if ctx == nil || ctx.base == nil || len(c.retStack) != ctx.retDepth {
		c.errorf("TL3301", n.Span(), "'super' can only be used inside methods of a derived class")
		for _, a := range n.Args {
			c.checkExpr(a)
		}
		return []types.Type{types.Any}
	}
	if ctx.inStatic {
		c.errorf("TL3302", n.Span(), "'super' cannot be used in a static method (use the base class name)")
		for _, a := range n.Args {
			c.checkExpr(a)
		}
		return []types.Type{types.Any}
	}
	// Walk the base chain for the method (instance methods only).
	for b := ctx.base; b != nil; b = b.Base {
		if f, ok := b.Fields[n.Method.Name]; ok && f.IsMethod {
			if f.Access == types.Private {
				c.errorf("TL3310", n.Span(), "method '%s' is private to class '%s'", n.Method.Name, b.Name)
				return []types.Type{types.Any}
			}
			if mt, ok := f.Type.(*types.Func); ok {
				return c.checkCall(n.Span(), mt, n.Args, n.Span(), c.resolveTypeArgs(n.TypeArgs))
			}
			for _, a := range n.Args {
				c.checkExpr(a)
			}
			return []types.Type{types.Any}
		}
	}
	// Static with the same name? Point the user at it.
	if ctx.baseVal != nil {
		if _, ok := ctx.baseVal.Statics[n.Method.Name]; ok {
			c.errorf("TL3303", n.Span(), "'%s' is static; call it on the base class by name", n.Method.Name)
			for _, a := range n.Args {
				c.checkExpr(a)
			}
			return []types.Type{types.Any}
		}
	}
	c.errorf("TL3230", n.Span(), "type '%s' has no method '%s'", ctx.base.String(), n.Method.Name)
	for _, a := range n.Args {
		c.checkExpr(a)
	}
	return []types.Type{types.Any}
}

// ---------- class constructor analysis walkers ----------
// These skip nested function/class bodies: only code running during
// construction itself counts for coverage and super() placement.

func isSuperCall(e ast.Expr) bool {
	call, ok := e.(*ast.CallExpr)
	if !ok {
		return false
	}
	id, ok := call.Func.(*ast.Ident)
	return ok && id.Name == "super"
}

func blockHasSuperCall(b *ast.Block) bool {
	if b == nil {
		return false
	}
	for _, st := range b.Stmts {
		if stmtHasSuperCall(st) {
			return true
		}
	}
	if b.Ret != nil {
		for _, e := range b.Ret.Exprs {
			if exprHasSuperCall(e) {
				return true
			}
		}
	}
	return false
}

// topLevelSuperIndex finds a direct `super(...)` statement in a constructor
// body for self-before-super ordering. -1 when nested only.
func topLevelSuperIndex(b *ast.Block) int {
	if b == nil {
		return -1
	}
	for i, st := range b.Stmts {
		if es, ok := st.(*ast.ExprStmt); ok && isSuperCall(es.E) {
			return i
		}
	}
	return -1
}

func stmtHasSuperCall(st ast.Stmt) bool {
	switch n := st.(type) {
	case *ast.ExprStmt:
		return exprHasSuperCall(n.E)
	case *ast.AssignStmt:
		for _, t := range n.Targets {
			if exprHasSuperCall(t) {
				return true
			}
		}
		for _, v := range n.Values {
			if exprHasSuperCall(v) {
				return true
			}
		}
	case *ast.LocalAssign:
		for _, v := range n.Values {
			if exprHasSuperCall(v) {
				return true
			}
		}
	case *ast.DoBlock:
		return blockHasSuperCall(n.Block)
	case *ast.IfStmt:
		if exprHasSuperCall(n.Cond) || blockHasSuperCall(n.Then) {
			return true
		}
		for _, ei := range n.Elseifs {
			if exprHasSuperCall(ei.Cond) || blockHasSuperCall(ei.Block) {
				return true
			}
		}
		if n.Else != nil && blockHasSuperCall(n.Else) {
			return true
		}
	case *ast.WhileStmt:
		return exprHasSuperCall(n.Cond) || blockHasSuperCall(n.Block)
	case *ast.RepeatStmt:
		return blockHasSuperCall(n.Block) || exprHasSuperCall(n.Cond)
	case *ast.NumericFor:
		return exprHasSuperCall(n.Start) || exprHasSuperCall(n.End) ||
			(n.Step != nil && exprHasSuperCall(n.Step)) || blockHasSuperCall(n.Block)
	case *ast.GenericFor:
		for _, e := range n.Exprs {
			if exprHasSuperCall(e) {
				return true
			}
		}
		return blockHasSuperCall(n.Block)
	}
	return false
}

func exprHasSuperCall(e ast.Expr) bool {
	switch n := e.(type) {
	case *ast.CallExpr:
		if isSuperCall(e) {
			return true
		}
		if exprHasSuperCall(n.Func) {
			return true
		}
		for _, a := range n.Args {
			if exprHasSuperCall(a) {
				return true
			}
		}
	case *ast.MethodCallExpr:
		if exprHasSuperCall(n.Obj) {
			return true
		}
		for _, a := range n.Args {
			if exprHasSuperCall(a) {
				return true
			}
		}
	case *ast.BinOpExpr:
		return exprHasSuperCall(n.L) || exprHasSuperCall(n.R)
	case *ast.UnOpExpr:
		return exprHasSuperCall(n.X)
	case *ast.ParenExpr:
		return exprHasSuperCall(n.E)
	case *ast.IndexExpr:
		return exprHasSuperCall(n.Obj) || exprHasSuperCall(n.Key)
	case *ast.FieldExpr:
		return exprHasSuperCall(n.Obj)
	case *ast.TableLit:
		for _, f := range n.Fields {
			if f.KeyX != nil && exprHasSuperCall(f.KeyX) {
				return true
			}
			if exprHasSuperCall(f.Val) {
				return true
			}
		}
	case *ast.CastExpr:
		return exprHasSuperCall(n.E)
	case *ast.NonNullExpr:
		return exprHasSuperCall(n.E)
	case *ast.FuncLit, *ast.Ident, *ast.NumberLit, *ast.StringLit,
		*ast.BoolLit, *ast.NilLit, *ast.VarArg:
	}
	return false
}

// collectSelfAssigns gathers `self.name = ...` targets (construction-time).
func collectSelfAssigns(b *ast.Block, into map[string]bool) {
	if b == nil {
		return
	}
	for _, st := range b.Stmts {
		collectStmtSelfAssigns(st, into)
	}
}

func collectStmtSelfAssigns(st ast.Stmt, into map[string]bool) {
	switch n := st.(type) {
	case *ast.AssignStmt:
		for _, t := range n.Targets {
			if fe, ok := t.(*ast.FieldExpr); ok {
				if id, ok := fe.Obj.(*ast.Ident); ok && id.Name == "self" {
					into[fe.Field.Name] = true
				}
			}
		}
	case *ast.DoBlock:
		collectSelfAssigns(n.Block, into)
	case *ast.IfStmt:
		collectSelfAssigns(n.Then, into)
		for _, ei := range n.Elseifs {
			collectSelfAssigns(ei.Block, into)
		}
		if n.Else != nil {
			collectSelfAssigns(n.Else, into)
		}
	case *ast.WhileStmt:
		collectSelfAssigns(n.Block, into)
	case *ast.RepeatStmt:
		collectSelfAssigns(n.Block, into)
	case *ast.NumericFor:
		collectSelfAssigns(n.Block, into)
	case *ast.GenericFor:
		collectSelfAssigns(n.Block, into)
	}
}

// stmtUsesSelf reports textual `self` use (for super-before-self).
func stmtUsesSelf(st ast.Stmt) bool {
	switch n := st.(type) {
	case *ast.ExprStmt:
		return exprUsesSelf(n.E)
	case *ast.AssignStmt:
		for _, t := range n.Targets {
			if exprUsesSelf(t) {
				return true
			}
		}
		for _, v := range n.Values {
			if exprUsesSelf(v) {
				return true
			}
		}
	case *ast.LocalAssign:
		for _, v := range n.Values {
			if exprUsesSelf(v) {
				return true
			}
		}
	case *ast.DoBlock:
		return blockUsesSelf(n.Block)
	case *ast.IfStmt:
		if exprUsesSelf(n.Cond) || blockUsesSelf(n.Then) {
			return true
		}
		for _, ei := range n.Elseifs {
			if exprUsesSelf(ei.Cond) || blockUsesSelf(ei.Block) {
				return true
			}
		}
		if n.Else != nil && blockUsesSelf(n.Else) {
			return true
		}
	case *ast.WhileStmt:
		return exprUsesSelf(n.Cond) || blockUsesSelf(n.Block)
	case *ast.RepeatStmt:
		return blockUsesSelf(n.Block) || exprUsesSelf(n.Cond)
	case *ast.NumericFor:
		return exprUsesSelf(n.Start) || exprUsesSelf(n.End) ||
			(n.Step != nil && exprUsesSelf(n.Step)) || blockUsesSelf(n.Block)
	case *ast.GenericFor:
		for _, e := range n.Exprs {
			if exprUsesSelf(e) {
				return true
			}
		}
		return blockUsesSelf(n.Block)
	case *ast.ReturnStmt:
		for _, e := range n.Exprs {
			if exprUsesSelf(e) {
				return true
			}
		}
	}
	return false
}

func blockUsesSelf(b *ast.Block) bool {
	if b == nil {
		return false
	}
	for _, st := range b.Stmts {
		if stmtUsesSelf(st) {
			return true
		}
	}
	if b.Ret != nil {
		for _, e := range b.Ret.Exprs {
			if exprUsesSelf(e) {
				return true
			}
		}
	}
	return false
}

func exprUsesSelf(e ast.Expr) bool {
	switch n := e.(type) {
	case *ast.Ident:
		return n.Name == "self"
	case *ast.CallExpr:
		if exprUsesSelf(n.Func) {
			return true
		}
		for _, a := range n.Args {
			if exprUsesSelf(a) {
				return true
			}
		}
	case *ast.MethodCallExpr:
		if exprUsesSelf(n.Obj) {
			return true
		}
		for _, a := range n.Args {
			if exprUsesSelf(a) {
				return true
			}
		}
	case *ast.BinOpExpr:
		return exprUsesSelf(n.L) || exprUsesSelf(n.R)
	case *ast.UnOpExpr:
		return exprUsesSelf(n.X)
	case *ast.ParenExpr:
		return exprUsesSelf(n.E)
	case *ast.IndexExpr:
		return exprUsesSelf(n.Obj) || exprUsesSelf(n.Key)
	case *ast.FieldExpr:
		return exprUsesSelf(n.Obj)
	case *ast.TableLit:
		for _, f := range n.Fields {
			if f.KeyX != nil && exprUsesSelf(f.KeyX) {
				return true
			}
			if exprUsesSelf(f.Val) {
				return true
			}
		}
	case *ast.CastExpr:
		return exprUsesSelf(n.E)
	case *ast.NonNullExpr:
		return exprUsesSelf(n.E)
	case *ast.FuncLit, *ast.NumberLit, *ast.StringLit,
		*ast.BoolLit, *ast.NilLit, *ast.VarArg:
	}
	return false
}

// collectInferNames gathers `infer U` names in an extends clause.
// Nested conditionals bind their own scope and are not descended into.
func collectInferNames(n ast.TypeNode, into map[string]bool) map[string]bool {
	if into == nil {
		into = map[string]bool{}
	}
	switch t := n.(type) {
	case *ast.InferType:
		into[t.Name.Name] = true
	case *ast.UnionType:
		for _, m := range t.Types {
			collectInferNames(m, into)
		}
	case *ast.IntersectionType:
		for _, m := range t.Types {
			collectInferNames(m, into)
		}
	case *ast.ArrayType:
		collectInferNames(t.Elem, into)
	case *ast.OptionalType:
		collectInferNames(t.Elem, into)
	case *ast.ParenType:
		collectInferNames(t.Elem, into)
	case *ast.TupleType:
		for _, e := range t.Elems {
			collectInferNames(e, into)
		}
	case *ast.TableType:
		for _, f := range t.Fields {
			if f.IsIndexer {
				collectInferNames(f.IndexKey, into)
				collectInferNames(f.IndexVal, into)
			} else if f.IsMethod && f.MethodSig != nil {
				collectInferNames(f.MethodSig, into)
			} else if f.Type != nil {
				collectInferNames(f.Type, into)
			}
		}
	case *ast.FuncType:
		for _, p := range t.Params {
			if p.Type != nil {
				collectInferNames(p.Type, into)
			}
		}
		if t.VarArgType != nil {
			collectInferNames(t.VarArgType, into)
		}
		for _, r := range t.Returns {
			collectInferNames(r, into)
		}
	case *ast.NamedType:
		for _, a := range t.Args {
			collectInferNames(a, into)
		}
	case *ast.KeyofType:
		collectInferNames(t.Elem, into)
	case *ast.MappedType:
		// Value-position infers bind in the true branch; constraint
		// infers would have nothing to bind against, skip them.
		collectInferNames(t.Value, into)
	case *ast.IndexedAccessType:
		collectInferNames(t.Obj, into)
		collectInferNames(t.Index, into)
	case *ast.ConditionalType:
		// Own scope; skip.
	}
	return into
}

// ---------- LSP support ----------

// ScopeAt returns the deepest recorded scope containing the byte offset.
func (idx *SymIndex) ScopeAt(offset int) *Scope {
	if idx == nil {
		return nil
	}
	var best *Scope
	bestSize := -1
	for _, ss := range idx.Scopes {
		if ss.Span.Start.Offset <= offset && offset < ss.Span.End.Offset {
			size := ss.Span.End.Offset - ss.Span.Start.Offset
			if best == nil || size < bestSize {
				best = ss.Sc
				bestSize = size
			}
		}
	}
	return best
}

// ParentScope returns the enclosing scope, if any.
func (s *Scope) ParentScope() *Scope {
	if s == nil {
		return nil
	}
	return s.parent
}

// ScopeVar is one visible variable binding.
type ScopeVar struct {
	Name string
	Sym  *Symbol
}

// ScopeVars lists the variables bound directly in this scope.
func (s *Scope) ScopeVars() []ScopeVar {
	if s == nil {
		return nil
	}
	out := make([]ScopeVar, 0, len(s.vars))
	for name, sym := range s.vars {
		if sym == nil {
			continue
		}
		out = append(out, ScopeVar{Name: name, Sym: sym})
	}
	return out
}

// HasType reports whether a type name is visible from this scope.
func (s *Scope) HasType(name string) bool {
	return s.LookupTypeEntry(name) != nil
}

// LookupTypeEntry returns the visible type declaration for name, if any.
func (s *Scope) LookupTypeEntry(name string) *TypeEntry {
	for sc := s; sc != nil; sc = sc.parent {
		if e, ok := sc.types[name]; ok {
			return e
		}
	}
	return nil
}

// ScopeTypeInfo is one visible type declaration.
type ScopeTypeInfo struct {
	Name string
	Kind string
	Span token.Span
}

// ScopeTypes lists type declarations visible from this scope (innermost
// shadows outermost on name clashes).
func (s *Scope) ScopeTypes() []ScopeTypeInfo {
	if s == nil {
		return nil
	}
	// Collect outward, innermost wins.
	var chain []*Scope
	for sc := s; sc != nil; sc = sc.parent {
		chain = append(chain, sc)
	}
	seen := map[string]bool{}
	var out []ScopeTypeInfo
	for i := len(chain) - 1; i >= 0; i-- {
		for name, e := range chain[i].types {
			if seen[name] {
				continue
			}
			seen[name] = true
			out = append(out, ScopeTypeInfo{Name: name, Kind: e.EntryKind(), Span: e.DeclSpan()})
		}
	}
	return out
}

// KindString describes a symbol for hovers and completion items.
func (s *Symbol) KindString() string {
	if s == nil {
		return "variable"
	}
	switch s.Kind {
	case symFunc:
		return "function"
	case symParam:
		return "parameter"
	default:
		return "variable"
	}
}

// TypeName returns the declared type name.
func (e *typeEntry) TypeName() string {
	if e == nil {
		return ""
	}
	return e.name
}

// DeclSpan returns the declaration name span for goto-definition.
func (e *typeEntry) DeclSpan() token.Span {
	if e == nil {
		return token.Span{}
	}
	return e.span
}

// EntryKind describes a type declaration for hovers.
func (e *typeEntry) EntryKind() string {
	if e == nil {
		return ""
	}
	switch {
	case e.alias != nil:
		return "type"
	case e.iface != nil:
		return "interface"
	case e.enumDecl != nil:
		return "enum"
	case e.classDecl != nil:
		return "class"
	default:
		return "type"
	}
}
