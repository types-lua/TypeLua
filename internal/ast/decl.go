package ast

import (
	"github.com/typelua/typelua/internal/token"
)

// Declaration nodes (types, interfaces, enums, macros).

// Type declarations.

type TypeAliasDecl struct {
	Name     *Ident
	Generics []*GenericParam
	Type     TypeNode
	Exported bool
	Declared bool
	S        token.Span
}

func (*TypeAliasDecl) stmtNode()         {}
func (n *TypeAliasDecl) Span() token.Span { return n.S }

type InterfaceDecl struct {
	Name     *Ident
	Generics []*GenericParam
	Extends  []TypeNode
	Fields   []*InterfaceField
	Exported bool
	Declared bool
	S        token.Span
}

func (*InterfaceDecl) stmtNode()         {}
func (n *InterfaceDecl) Span() token.Span { return n.S }

type InterfaceField struct {
	Name     *Ident
	Type     TypeNode
	Optional bool
	Readonly bool
	S        token.Span
}

func (n *InterfaceField) Span() token.Span { return n.S }

type EnumDecl struct {
	Name     *Ident
	Members  []*EnumMember
	Exported bool
	Declared bool
	S        token.Span
}

func (*EnumDecl) stmtNode()         {}
func (n *EnumDecl) Span() token.Span { return n.S }

type EnumMember struct {
	Name  *Ident
	Value Expr // nil => auto
	S     token.Span
}

func (n *EnumMember) Span() token.Span { return n.S }

// MacroDef is a compile-time macro: `macro name(params) = expr` for
// expressions, `macro name(params) do stmts end` for statements.
// A trailing `...` (HasVarArg) collects extra call arguments; inside the
// body a bare `...` splices them, but only in call-argument position.
// Definitions are expanded (and erased) before type checking.
type MacroDef struct {
	Name      *Ident
	Params    []*Ident
	HasVarArg bool
	Body      Expr   // expression macro (IsExpr)
	Block     *Block // statement macro (!IsExpr)
	IsExpr    bool
	S         token.Span
}

func (*MacroDef) stmtNode()          {}
func (n *MacroDef) Span() token.Span { return n.S }
