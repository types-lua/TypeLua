package ast

import (
	"github.com/typelua/typelua/internal/token"
)

// Expression nodes.

// ---------- Expressions ----------

// Ident 表示标识符表达式节点；Name 为源码名，S 为源码 Span，用于诊断与定位。
type Ident struct {
	Name string
	S    token.Span
}

func (*Ident) exprNode() {}

// Span returns the source span of this node.
func (n *Ident) Span() token.Span { return n.S }

// String 返回标识符名称，用于调试打印与诊断上下文。
func (n *Ident) String() string { return n.Name }

// NumberLit 表示数字字面量表达式节点；Literal 保留原文，S 为源码 Span，用于诊断与定位。
type NumberLit struct {
	Literal string
	S       token.Span
}

func (*NumberLit) exprNode() {}

// Span returns the source span of this node.
func (n *NumberLit) Span() token.Span { return n.S }

// StringLit 表示字符串字面量表达式节点；Literal 保留原文，S 为源码 Span，用于诊断与定位。
type StringLit struct {
	Literal string
	S       token.Span
}

func (*StringLit) exprNode() {}

// Span returns the source span of this node.
func (n *StringLit) Span() token.Span { return n.S }

// BoolLit 表示布尔字面量表达式节点；Value 为真值，S 为源码 Span，用于诊断与定位。
type BoolLit struct {
	Value bool
	S     token.Span
}

func (*BoolLit) exprNode() {}

// Span returns the source span of this node.
func (n *BoolLit) Span() token.Span { return n.S }

// NilLit 表示 nil 字面量表达式节点；S 为源码 Span，用于诊断与定位。
type NilLit struct{ S token.Span }

func (*NilLit) exprNode() {}

// Span returns the source span of this node.
func (n *NilLit) Span() token.Span { return n.S }

// VarArg 表示 vararg 表达式（`...`）节点；S 为源码 Span，用于诊断与定位。
type VarArg struct {
	S token.Span
	T TypeNode // optional annotated type from enclosing function; usually nil
}

func (*VarArg) exprNode() {}

// Span returns the source span of this node.
func (n *VarArg) Span() token.Span { return n.S }

// ParenExpr 表示带括号的表达式节点；S 为源码 Span，用于诊断与定位。
type ParenExpr struct {
	E Expr
	S token.Span
}

func (*ParenExpr) exprNode() {}

// Span returns the source span of this node.
func (n *ParenExpr) Span() token.Span { return n.S }

// TableLit 表示 table 字面量表达式节点；S 为源码 Span，用于诊断与定位。
type TableLit struct {
	Fields []*TableField
	S      token.Span
}

func (*TableLit) exprNode() {}

// Span returns the source span of this node.
func (n *TableLit) Span() token.Span { return n.S }

// TableFieldKind 区分 table 字段的种类（表达式/具名/索引）。
type TableFieldKind int

const (
	ExprField TableFieldKind = iota
	NamedField
	IndexedField
)

// TableField 表示 table 字面量中的一个字段；S 为源码 Span，用于诊断与定位。
type TableField struct {
	Kind TableFieldKind
	Key  *Ident // NamedField
	KeyX Expr   // IndexedField
	Val  Expr
	S    token.Span
}

// Span returns the source span of this node.
func (n *TableField) Span() token.Span { return n.S }

// FuncLit 表示函数字面量表达式节点；S 为源码 Span，用于诊断与定位。
type FuncLit struct {
	Params     []*Param
	HasVarArg  bool
	VarArgName *Ident
	VarArgType TypeNode
	ReturnType TypeNode // nil if unannotated; TupleType for multi-return
	Body       *Block
	IsMethod   bool // true if defined with colon (implicit self)
	Generics   []*GenericParam
	IsAsync    bool // `async function`: may await/yield; erases to plain function
	S          token.Span
}

func (*FuncLit) exprNode() {}

// Span returns the source span of this node.
func (n *FuncLit) Span() token.Span { return n.S }

// Param 表示函数参数声明；S 为源码 Span，用于诊断与定位。
type Param struct {
	Name *Ident
	Type TypeNode
	S    token.Span
}

// Span returns the source span of this node.
func (n *Param) Span() token.Span { return n.S }

// IndexExpr 表示索引访问表达式 `a[b]` 节点；S 为源码 Span，用于诊断与定位。
type IndexExpr struct {
	Obj Expr
	Key Expr
	S   token.Span
}

func (*IndexExpr) exprNode() {}

// Span returns the source span of this node.
func (n *IndexExpr) Span() token.Span { return n.S }

// FieldExpr 表示字段访问表达式 `a.b` 节点；S 为源码 Span，用于诊断与定位。
type FieldExpr struct {
	Obj   Expr
	Field *Ident
	S     token.Span
}

func (*FieldExpr) exprNode() {}

// Span returns the source span of this node.
func (n *FieldExpr) Span() token.Span { return n.S }

// CallExpr 表示函数调用表达式节点；S 为源码 Span，用于诊断与定位。
type CallExpr struct {
	Func     Expr
	TypeArgs []TypeNode // explicit generic arguments: f<T>(x)
	Args     []Expr
	// ErrorCall marks a call to the `error` builtin (set by the checker
	// when unshadowed); the emitter runs errdefers around it.
	ErrorCall bool
	S         token.Span
}

func (*CallExpr) exprNode() {}

// Span returns the source span of this node.
func (n *CallExpr) Span() token.Span { return n.S }

// MethodCallExpr 表示方法调用表达式节点 `a:m(...)`；S 为源码 Span，用于诊断与定位。
type MethodCallExpr struct {
	Obj      Expr
	Method   *Ident
	TypeArgs []TypeNode // obj:m<T>(x)
	Args     []Expr
	S        token.Span
}

func (*MethodCallExpr) exprNode() {}

// Span returns the source span of this node.
func (n *MethodCallExpr) Span() token.Span { return n.S }

// BinOpExpr 表示二元运算表达式节点；S 为源码 Span，用于诊断与定位。
type BinOpExpr struct {
	Op token.Kind
	L  Expr
	R  Expr
	S  token.Span
}

func (*BinOpExpr) exprNode() {}

// Span returns the source span of this node.
func (n *BinOpExpr) Span() token.Span { return n.S }

// UnOpExpr 表示一元运算表达式节点；S 为源码 Span，用于诊断与定位。
type UnOpExpr struct {
	Op token.Kind
	X  Expr
	S  token.Span
}

func (*UnOpExpr) exprNode() {}

// Span returns the source span of this node.
func (n *UnOpExpr) Span() token.Span { return n.S }

// CastExpr 表示类型断言/转换表达式 `x as T` 节点；S 为源码 Span，用于诊断与定位。
type CastExpr struct {
	E Expr
	T TypeNode
	S token.Span
}

func (*CastExpr) exprNode() {}

// Span returns the source span of this node.
func (n *CastExpr) Span() token.Span { return n.S }

// NonNullExpr 表示非空断言表达式 `x!` 节点；S 为源码 Span，用于诊断与定位。
type NonNullExpr struct {
	E Expr
	S token.Span
}

func (*NonNullExpr) exprNode() {}

// Span returns the source span of this node.
func (n *NonNullExpr) Span() token.Span { return n.S }

// AwaitExpr is `await <expr>` (soft keyword): marks a suspension point
// inside an `async` function (or the top-level chunk). It erases to the
// inner expression: blocking runtime calls yield by themselves, await is
// the explicit marker the checker validates.
type AwaitExpr struct {
	X Expr
	S token.Span
}

func (*AwaitExpr) exprNode() {}

// Span returns the source span of this node.
func (n *AwaitExpr) Span() token.Span { return n.S }

// IsLValue reports whether e can appear on the left of `=`.
func IsLValue(e Expr) bool {
	switch e.(type) {
	case *Ident, *IndexExpr, *FieldExpr:
		return true
	}
	return false
}
