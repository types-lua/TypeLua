package ast

import (
	"github.com/typelua/typelua/internal/token"
)

// Type nodes.

// ---------- Types ----------

// NamedType 表示命名类型语法节点（可带类型参数）；S 为源码 Span，用于诊断与定位。
type NamedType struct {
	Name string
	Args []TypeNode
	S    token.Span
}

func (*NamedType) typeNode() {}

// Span returns the source span of this node.
func (n *NamedType) Span() token.Span { return n.S }

// LiteralType 表示字面量类型语法节点；S 为源码 Span，用于诊断与定位。
type LiteralType struct {
	// Value holds the literal source, e.g. `"red"`, `42`, `true`, `nil`.
	Value string
	S     token.Span
}

func (*LiteralType) typeNode() {}

// Span returns the source span of this node.
func (n *LiteralType) Span() token.Span { return n.S }

// ArrayType 表示数组类型语法节点 `T[]`；S 为源码 Span，用于诊断与定位。
type ArrayType struct {
	Elem TypeNode
	S    token.Span
}

func (*ArrayType) typeNode() {}

// Span returns the source span of this node.
func (n *ArrayType) Span() token.Span { return n.S }

// UnionType 表示联合类型语法节点 `A | B`；S 为源码 Span，用于诊断与定位。
type UnionType struct {
	Types []TypeNode
	S     token.Span
}

func (*UnionType) typeNode() {}

// Span returns the source span of this node.
func (n *UnionType) Span() token.Span { return n.S }

// IntersectionType 表示交叉类型语法节点 `A & B`；S 为源码 Span，用于诊断与定位。
type IntersectionType struct {
	Types []TypeNode
	S     token.Span
}

func (*IntersectionType) typeNode() {}

// Span returns the source span of this node.
func (n *IntersectionType) Span() token.Span { return n.S }

// OptionalType 表示可选类型语法节点 `T?`；S 为源码 Span，用于诊断与定位。
type OptionalType struct {
	Elem TypeNode
	S    token.Span
}

func (*OptionalType) typeNode() {}

// Span returns the source span of this node.
func (n *OptionalType) Span() token.Span { return n.S }

// ParenType 表示带括号的类型语法节点；S 为源码 Span，用于诊断与定位。
type ParenType struct {
	Elem TypeNode
	S    token.Span
}

func (*ParenType) typeNode() {}

// Span returns the source span of this node.
func (n *ParenType) Span() token.Span { return n.S }

// TupleType 表示元组类型语法节点；S 为源码 Span，用于诊断与定位。
type TupleType struct {
	Elems []TypeNode
	S     token.Span
}

func (*TupleType) typeNode() {}

// Span returns the source span of this node.
func (n *TupleType) Span() token.Span { return n.S }

// TableType 表示 table 类型语法节点；S 为源码 Span，用于诊断与定位。
type TableType struct {
	Fields []*TableTypeField
	S      token.Span
}

func (*TableType) typeNode() {}

// Span returns the source span of this node.
func (n *TableType) Span() token.Span { return n.S }

// TableTypeField 表示 table 类型中的一个字段/索引器/方法；S 为源码 Span，用于诊断与定位。
type TableTypeField struct {
	Name     string
	NameSpan token.Span
	Type     TypeNode
	Optional bool
	Readonly bool
	S        token.Span
	// For indexer `[K]: V`
	IsIndexer bool
	IndexKey  TypeNode
	IndexVal  TypeNode
	IsMethod  bool
	MethodSig *FuncType
}

// Span returns the source span of this node.
func (n *TableTypeField) Span() token.Span { return n.S }

// FuncType 表示函数类型语法节点；S 为源码 Span，用于诊断与定位。
type FuncType struct {
	Params     []*FuncTypeParam
	HasVarArg  bool
	VarArgType TypeNode
	Returns    []TypeNode // 0 => void-ish (unannotated?), 1+ return types
	Generics   []*GenericParam
	S          token.Span
}

func (*FuncType) typeNode() {}

// Span returns the source span of this node.
func (n *FuncType) Span() token.Span { return n.S }

// FuncTypeParam 表示函数类型的一个参数；S 为源码 Span，用于诊断与定位。
type FuncTypeParam struct {
	Name     *Ident // may be nil for unnamed
	Type     TypeNode
	IsVarArg bool
	Optional bool
	S        token.Span
}

// Span returns the source span of this node.
func (n *FuncTypeParam) Span() token.Span { return n.S }

// TypeofType 表示 typeof 类型语法节点；S 为源码 Span，用于诊断与定位。
type TypeofType struct {
	Expr Expr
	S    token.Span
}

func (*TypeofType) typeNode() {}

// Span returns the source span of this node.
func (n *TypeofType) Span() token.Span { return n.S }

// KeyofType 表示 keyof 类型语法节点；S 为源码 Span，用于诊断与定位。
type KeyofType struct {
	Elem TypeNode
	S    token.Span
}

func (*KeyofType) typeNode() {}

// Span returns the source span of this node.
func (n *KeyofType) Span() token.Span { return n.S }

// MappedType is `{ [K in Keys]?: V }` with TS-style modifier operators:
// an optional `+`/`-` prefix on `readonly` (`+readonly`/`-readonly`) and on
// `?` (`+?`/`-?`). `+` is the explicit form of the bare marker; `-` removes
// the modifier (meaningful for homomorphic mappings over `keyof T`).
type MappedType struct {
	Param      *Ident
	Constraint TypeNode
	Optional   bool // `?` or `+?`
	OptMinus   bool // `-?`
	Readonly   bool // `readonly` or `+readonly`
	RoMinus    bool // `-readonly`
	Value      TypeNode
	S          token.Span
}

func (*MappedType) typeNode() {}

// Span returns the source span of this node.
func (n *MappedType) Span() token.Span { return n.S }

// IndexedAccessType is `T[K]`.
type IndexedAccessType struct {
	Obj   TypeNode
	Index TypeNode
	S     token.Span
}

func (*IndexedAccessType) typeNode() {}

// Span returns the source span of this node.
func (n *IndexedAccessType) Span() token.Span { return n.S }

// ConditionalType is `Check extends Extends ? True : False`.
type ConditionalType struct {
	Check   TypeNode
	Extends TypeNode
	True    TypeNode
	False   TypeNode
	S       token.Span
}

func (*ConditionalType) typeNode() {}

// Span returns the source span of this node.
func (n *ConditionalType) Span() token.Span { return n.S }

// InferType is `infer U` (only meaningful in an extends clause).
type InferType struct {
	Name *Ident
	S    token.Span
}

func (*InferType) typeNode() {}

// Span returns the source span of this node.
func (n *InferType) Span() token.Span { return n.S }
