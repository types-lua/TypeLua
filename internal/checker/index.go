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
