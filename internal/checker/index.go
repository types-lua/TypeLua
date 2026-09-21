package checker

import (
	"github.com/typelua/typelua/internal/ast"
	"github.com/typelua/typelua/internal/token"
	"github.com/typelua/typelua/internal/types"
)

// Position index recording for language servers (best-effort, never affects diagnostics).

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

// lookupNamedField resolves a DECLARED member (no indexer fallback), for
// member tracking (references/rename) which needs a definition site.
func lookupNamedField(ot types.Type, name string) (types.Field, bool) {
	switch x := ot.(type) {
	case *types.Object:
		if f, ok := x.Fields[name]; ok {
			return f, true
		}
	case *types.Class:
		if f, ok := x.Fields[name]; ok {
			return types.Field{Type: f.Type, Optional: f.Optional, Readonly: f.Readonly, Access: f.Access, Owner: f.Owner}, true
		}
	case *types.ClassValue:
		if f, ok := x.Statics[name]; ok {
			return types.Field{Type: f.Type, Optional: f.Optional, Readonly: f.Readonly, Access: f.Access, Owner: f.Owner}, true
		}
		// Synthetic `new` has no definition; never tracked.
	case *types.Enum:
		if m, ok := x.Members[name]; ok {
			return types.Field{Type: m}, true
		}
	}
	return types.Field{}, false
}

// recordMemberUse records a member access on a resolved owner type.
// Unknown owners (structural objects, maps, any) are skipped, as are
// indexer-only resolutions (no definition site to unify).
// Union bases record only on unanimous ownership: every member must
// declare the field under the SAME entry, otherwise the use stays
// untracked (access itself still checks via lookupField).
func (c *Checker) recordMemberUse(span token.Span, objT types.Type, name string) {
	if c.Index == nil {
		return
	}
	if u, ok := objT.(*types.Union); ok {
		var e *typeEntry
		for _, m := range u.Types {
			if _, ok := lookupNamedField(m, name); !ok {
				return
			}
			me := c.ownerEntry(m)
			if me == nil {
				return
			}
			if e == nil {
				e = me
			} else if e != me {
				return
			}
		}
		if e == nil {
			return
		}
		c.Index.Uses = append(c.Index.Uses, SymUse{Span: span,
			Member: &MemberRef{Owner: e, OwnerName: e.name, Name: name}})
		return
	}
	if _, ok := lookupNamedField(objT, name); !ok {
		return
	}
	e := c.ownerEntry(objT)
	if e == nil {
		return
	}
	c.Index.Uses = append(c.Index.Uses, SymUse{Span: span,
		Member: &MemberRef{Owner: e, OwnerName: e.name, Name: name}})
}

// recordStringIndex records `obj["field"]` with a quoted literal key as a
// member occurrence (inner span, quotes preserved for edits), when the
// owner declares the field. Indexer-only resolutions stay untracked.
func (c *Checker) recordStringIndex(key ast.Expr, objT types.Type) {
	if c.Index == nil {
		return
	}
	sl, ok := key.(*ast.StringLit)
	if !ok {
		return
	}
	name, inner, ok := stringKeyName(sl)
	if !ok {
		return
	}
	if _, ok := lookupNamedField(objT, name); !ok {
		return
	}
	e := c.ownerEntry(objT)
	if e == nil {
		return
	}
	c.Index.Uses = append(c.Index.Uses, SymUse{Span: inner,
		Member: &MemberRef{Owner: e, OwnerName: e.name, Name: name}})
}

// recordStructIndex records `obj["field"]` through a plain variable with
// a tracked literal field (inner span, quotes preserved for edits).
func (c *Checker) recordStructIndex(key, obj ast.Expr) {
	if c.Index == nil {
		return
	}
	sl, ok := key.(*ast.StringLit)
	if !ok {
		return
	}
	name, inner, ok := stringKeyName(sl)
	if !ok {
		return
	}
	if root := structRoot(c, obj); root != nil {
		c.recordStructUse(inner, root, name)
	}
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

// recordStructLiteral records named keys of a table literal as structural
// definitions under root (see recordStructDef).
func (c *Checker) recordStructLiteral(root *Symbol, lit *ast.TableLit) {
	if c.Index == nil || root == nil || lit == nil {
		return
	}
	for _, f := range lit.Fields {
		var name string
		var sp token.Span
		switch f.Kind {
		case ast.NamedField:
			if f.Key == nil {
				continue
			}
			name, sp = f.Key.Name, f.Key.Span()
		case ast.IndexedField:
			sl, ok := f.KeyX.(*ast.StringLit)
			if !ok {
				continue
			}
			nm, inner, ok := stringKeyName(sl)
			if !ok {
				continue
			}
			name, sp = nm, inner
		default:
			continue
		}
		c.recordStructDef(root, name, sp)
	}
}

// recordStructDef records an anonymous literal field under its root
// variable (definition side of structural member tracking).
func (c *Checker) recordStructDef(root *Symbol, name string, span token.Span) {
	if c.Index == nil || root == nil {
		return
	}
	c.Index.Uses = append(c.Index.Uses, SymUse{Span: span, IsDef: true,
		Member: &MemberRef{OwnerName: root.Name, Name: name, Kind: "field", StructOwner: root}})
}

// hasStructDef reports a recorded structural definition.
func (c *Checker) hasStructDef(root *Symbol, name string) bool {
	if c.Index == nil || root == nil {
		return false
	}
	for i := range c.Index.Uses {
		u := &c.Index.Uses[i]
		if u.IsDef && u.Member != nil && u.Member.StructOwner == root && u.Member.Name == name {
			return true
		}
	}
	return false
}

// recordStructUse records a member access through a plain variable whose
// root holds a tracked literal field. Only direct flows unify (aliases,
// params, and table values stay untracked); callers must resolve the base
// identifier themselves.
func (c *Checker) recordStructUse(span token.Span, root *Symbol, name string) {
	if c.Index == nil || root == nil {
		return
	}
	if !c.hasStructDef(root, name) {
		return
	}
	c.Index.Uses = append(c.Index.Uses, SymUse{Span: span,
		Member: &MemberRef{OwnerName: root.Name, Name: name, StructOwner: root}})
}

// structRoot resolves a member-access base to its variable root symbol
// (Ident only: deeper bases have no variable identity).
func structRoot(c *Checker, obj ast.Expr) *Symbol {
	id, ok := obj.(*ast.Ident)
	if !ok {
		return nil
	}
	if s := c.cur().lookupVar(id.Name); s != nil {
		return symRoot(s)
	}
	return nil
}

func (c *Checker) recordScope(sc *Scope, span token.Span) {
	if c.Index == nil {
		return
	}
	c.Index.Scopes = append(c.Index.Scopes, ScopeSpan{Sc: sc, Span: span})
}
