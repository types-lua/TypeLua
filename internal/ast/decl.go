package ast

import (
	"github.com/typelua/typelua/internal/token"
)

// Declaration nodes (types, interfaces, enums, macros).

// Type declarations.

// TypeAliasDecl 表示 `type Name = T` 类型别名声明；S 为源码 Span，用于诊断与定位。
type TypeAliasDecl struct {
	Name     *Ident
	Generics []*GenericParam
	Type     TypeNode
	Exported bool
	Declared bool
	S        token.Span
}

func (*TypeAliasDecl) stmtNode() {}

// Span returns the source span of this node.
func (n *TypeAliasDecl) Span() token.Span { return n.S }

// InterfaceDecl 表示 `interface Name { ... }` 接口声明；S 为源码 Span，用于诊断与定位。
type InterfaceDecl struct {
	Name     *Ident
	Generics []*GenericParam
	Extends  []TypeNode
	Fields   []*InterfaceField
	Exported bool
	Declared bool
	S        token.Span
}

func (*InterfaceDecl) stmtNode() {}

// Span returns the source span of this node.
func (n *InterfaceDecl) Span() token.Span { return n.S }

// InterfaceField 表示接口中的一个字段/方法签名；S 为源码 Span，用于诊断与定位。
type InterfaceField struct {
	Name     *Ident
	Type     TypeNode
	Optional bool
	Readonly bool
	S        token.Span
}

// Span returns the source span of this node.
func (n *InterfaceField) Span() token.Span { return n.S }

// EnumDecl 表示 `enum Name { ... }` 枚举声明；S 为源码 Span，用于诊断与定位。
type EnumDecl struct {
	Name     *Ident
	Members  []*EnumMember
	Exported bool
	Declared bool
	S        token.Span
}

func (*EnumDecl) stmtNode() {}

// Span returns the source span of this node.
func (n *EnumDecl) Span() token.Span { return n.S }

// EnumMember 表示枚举中的一个成员（可带显式值）；S 为源码 Span，用于诊断与定位。
type EnumMember struct {
	Name  *Ident
	Value Expr // nil => auto
	S     token.Span
}

// Span returns the source span of this node.
func (n *EnumMember) Span() token.Span { return n.S }

// MacroDef is a compile-time macro: `macro name(params) = expr` for
// expressions, `macro name(params) do stmts end` for statements.
// 尾部的 `...`（HasVarArg）收集额外调用实参；函数体内裸 `...` 仅在
// 调用实参位置展开它们。
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

func (*MacroDef) stmtNode() {}

// Span returns the source span of this node.
func (n *MacroDef) Span() token.Span { return n.S }
