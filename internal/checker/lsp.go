package checker

import (
	"github.com/typelua/typelua/internal/token"
	"github.com/typelua/typelua/internal/types"
)

// Read API over the position index for the language server.

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

// MemberSpan returns the declaration span of a field/method/enum member.
// It covers ambient declarations whose definition occurrences live in a
// different file's index (the LSP only records the open file's index).
func (e *typeEntry) MemberSpan(name string) (token.Span, bool) {
	if e == nil {
		return token.Span{}, false
	}
	if e.iface != nil {
		for _, f := range e.iface.Fields {
			if f.Name != nil && f.Name.Name == name {
				return f.Name.Span(), true
			}
		}
	}
	if e.classDecl != nil {
		for _, m := range e.classDecl.Members {
			if m.Name != nil && m.Name.Name == name {
				return m.Name.Span(), true
			}
		}
	}
	if e.enumDecl != nil {
		for _, m := range e.enumDecl.Members {
			if m.Name != nil && m.Name.Name == name {
				return m.Name.Span(), true
			}
		}
	}
	return token.Span{}, false
}

// DeclRange returns the full declaration span (for visibility checks:
// private members complete only inside it).
func (e *typeEntry) DeclRange() token.Span {
	if e == nil {
		return token.Span{}
	}
	switch {
	case e.alias != nil:
		return e.alias.Span()
	case e.iface != nil:
		return e.iface.Span()
	case e.enumDecl != nil:
		return e.enumDecl.Span()
	case e.classDecl != nil:
		return e.classDecl.Span()
	}
	return token.Span{}
}

// LookupFieldType resolves a named member's type (for completion chains;
// indexers included, like field access).
func LookupFieldType(ot types.Type, name string) (types.Type, bool) {
	f, ok := lookupField(ot, name)
	if !ok {
		return nil, false
	}
	return f.Type, true
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
