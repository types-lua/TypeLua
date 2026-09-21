package ast

import (
	"github.com/typelua/typelua/internal/token"
)

// Expression nodes.

// ---------- Expressions ----------

type Ident struct {
	Name string
	S    token.Span
}

func (*Ident) exprNode()          {}
func (n *Ident) Span() token.Span { return n.S }
func (n *Ident) String() string   { return n.Name }

type NumberLit struct {
	Literal string
	S       token.Span
}

func (*NumberLit) exprNode()          {}
func (n *NumberLit) Span() token.Span { return n.S }

type StringLit struct {
	Literal string
	S       token.Span
}

func (*StringLit) exprNode()          {}
func (n *StringLit) Span() token.Span { return n.S }

type BoolLit struct {
	Value bool
	S     token.Span
}

func (*BoolLit) exprNode()          {}
func (n *BoolLit) Span() token.Span { return n.S }

type NilLit struct{ S token.Span }

func (*NilLit) exprNode()          {}
func (n *NilLit) Span() token.Span { return n.S }

type VarArg struct {
	S token.Span
	T TypeNode // optional annotated type from enclosing function; usually nil
}

func (*VarArg) exprNode()          {}
func (n *VarArg) Span() token.Span { return n.S }

type ParenExpr struct {
	E Expr
	S token.Span
}

func (*ParenExpr) exprNode()          {}
func (n *ParenExpr) Span() token.Span { return n.S }

type TableLit struct {
	Fields []*TableField
	S      token.Span
}

func (*TableLit) exprNode()          {}
func (n *TableLit) Span() token.Span { return n.S }

type TableFieldKind int

const (
	ExprField TableFieldKind = iota
	NamedField
	IndexedField
)

type TableField struct {
	Kind TableFieldKind
	Key  *Ident // NamedField
	KeyX Expr   // IndexedField
	Val  Expr
	S    token.Span
}

func (n *TableField) Span() token.Span { return n.S }

type FuncLit struct {
	Params     []*Param
	HasVarArg  bool
	VarArgName *Ident
	VarArgType TypeNode
	ReturnType TypeNode // nil if unannotated; TupleType for multi-return
	Body       *Block
	IsMethod   bool // true if defined with colon (implicit self)
	Generics   []*GenericParam
	S          token.Span
}

func (*FuncLit) exprNode()          {}
func (n *FuncLit) Span() token.Span { return n.S }

type Param struct {
	Name *Ident
	Type TypeNode
	S    token.Span
}

func (n *Param) Span() token.Span { return n.S }

type IndexExpr struct {
	Obj Expr
	Key Expr
	S   token.Span
}

func (*IndexExpr) exprNode()          {}
func (n *IndexExpr) Span() token.Span { return n.S }

type FieldExpr struct {
	Obj   Expr
	Field *Ident
	S     token.Span
}

func (*FieldExpr) exprNode()          {}
func (n *FieldExpr) Span() token.Span { return n.S }

type CallExpr struct {
	Func     Expr
	TypeArgs []TypeNode // explicit generic arguments: f<T>(x)
	Args     []Expr
	S        token.Span
}

func (*CallExpr) exprNode()          {}
func (n *CallExpr) Span() token.Span { return n.S }

type MethodCallExpr struct {
	Obj      Expr
	Method   *Ident
	TypeArgs []TypeNode // obj:m<T>(x)
	Args     []Expr
	S        token.Span
}

func (*MethodCallExpr) exprNode()          {}
func (n *MethodCallExpr) Span() token.Span { return n.S }

type BinOpExpr struct {
	Op token.Kind
	L  Expr
	R  Expr
	S  token.Span
}

func (*BinOpExpr) exprNode()          {}
func (n *BinOpExpr) Span() token.Span { return n.S }

type UnOpExpr struct {
	Op token.Kind
	X  Expr
	S  token.Span
}

func (*UnOpExpr) exprNode()          {}
func (n *UnOpExpr) Span() token.Span { return n.S }

type CastExpr struct {
	E Expr
	T TypeNode
	S token.Span
}

func (*CastExpr) exprNode()          {}
func (n *CastExpr) Span() token.Span { return n.S }

type NonNullExpr struct {
	E Expr
	S token.Span
}

func (*NonNullExpr) exprNode()          {}
func (n *NonNullExpr) Span() token.Span { return n.S }

// IsLValue reports whether e can appear on the left of `=`.
func IsLValue(e Expr) bool {
	switch e.(type) {
	case *Ident, *IndexExpr, *FieldExpr:
		return true
	}
	return false
}
