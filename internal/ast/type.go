package ast

import (
	"github.com/typelua/typelua/internal/token"
)

// Type nodes.

// ---------- Types ----------

type NamedType struct {
	Name string
	Args []TypeNode
	S    token.Span
}

func (*NamedType) typeNode()          {}
func (n *NamedType) Span() token.Span { return n.S }

type LiteralType struct {
	// Value holds the literal source, e.g. `"red"`, `42`, `true`, `nil`.
	Value string
	S     token.Span
}

func (*LiteralType) typeNode()          {}
func (n *LiteralType) Span() token.Span { return n.S }

type ArrayType struct {
	Elem TypeNode
	S    token.Span
}

func (*ArrayType) typeNode()          {}
func (n *ArrayType) Span() token.Span { return n.S }

type UnionType struct {
	Types []TypeNode
	S     token.Span
}

func (*UnionType) typeNode()          {}
func (n *UnionType) Span() token.Span { return n.S }

type IntersectionType struct {
	Types []TypeNode
	S     token.Span
}

func (*IntersectionType) typeNode()         {}
func (n *IntersectionType) Span() token.Span { return n.S }

type OptionalType struct {
	Elem TypeNode
	S    token.Span
}

func (*OptionalType) typeNode()          {}
func (n *OptionalType) Span() token.Span { return n.S }

type ParenType struct {
	Elem TypeNode
	S    token.Span
}

func (*ParenType) typeNode()          {}
func (n *ParenType) Span() token.Span { return n.S }

type TupleType struct {
	Elems []TypeNode
	S     token.Span
}

func (*TupleType) typeNode()          {}
func (n *TupleType) Span() token.Span { return n.S }

type TableType struct {
	Fields []*TableTypeField
	S      token.Span
}

func (*TableType) typeNode()          {}
func (n *TableType) Span() token.Span { return n.S }

type TableTypeField struct {
	Name     string
	NameSpan token.Span
	Type     TypeNode
	Optional bool
	Readonly bool
	S        token.Span
	// For indexer `[K]: V`
	IsIndexer   bool
	IndexKey    TypeNode
	IndexVal    TypeNode
	IsMethod    bool
	MethodSig   *FuncType
}

func (n *TableTypeField) Span() token.Span { return n.S }

type FuncType struct {
	Params     []*FuncTypeParam
	HasVarArg  bool
	VarArgType TypeNode
	Returns    []TypeNode // 0 => void-ish (unannotated?), 1+ return types
	Generics   []*GenericParam
	S          token.Span
}

func (*FuncType) typeNode()          {}
func (n *FuncType) Span() token.Span { return n.S }

type FuncTypeParam struct {
	Name      *Ident // may be nil for unnamed
	Type      TypeNode
	IsVarArg  bool
	Optional  bool
	S         token.Span
}

func (n *FuncTypeParam) Span() token.Span { return n.S }

type TypeofType struct {
	Expr Expr
	S    token.Span
}

func (*TypeofType) typeNode()          {}
func (n *TypeofType) Span() token.Span { return n.S }

type KeyofType struct {
	Elem TypeNode
	S    token.Span
}

func (*KeyofType) typeNode()          {}
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

func (*MappedType) typeNode()          {}
func (n *MappedType) Span() token.Span { return n.S }

// IndexedAccessType is `T[K]`.
type IndexedAccessType struct {
	Obj   TypeNode
	Index TypeNode
	S     token.Span
}

func (*IndexedAccessType) typeNode()          {}
func (n *IndexedAccessType) Span() token.Span { return n.S }

// ConditionalType is `Check extends Extends ? True : False`.
type ConditionalType struct {
	Check   TypeNode
	Extends TypeNode
	True    TypeNode
	False   TypeNode
	S       token.Span
}

func (*ConditionalType) typeNode()          {}
func (n *ConditionalType) Span() token.Span { return n.S }

// InferType is `infer U` (only meaningful in an extends clause).
type InferType struct {
	Name *Ident
	S    token.Span
}

func (*InferType) typeNode()          {}
func (n *InferType) Span() token.Span { return n.S }
