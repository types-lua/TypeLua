package ast

import (
	"github.com/typelua/typelua/internal/token"
)

// Node is the base of all AST nodes.
type Node interface {
	Span() token.Span
}

type Stmt interface {
	Node
	stmtNode()
}
type Expr interface {
	Node
	exprNode()
}
type TypeNode interface {
	Node
	typeNode()
}

// File is a single .tl source unit.
type File struct {
	Name  string
	Block *Block
	EOF   token.Span
}

func (n *File) Span() token.Span {
	if n.Block == nil {
		return n.EOF
	}
	s := n.Block.Span()
	s.File = n.Name
	return s
}

// Block is a sequence of statements with optional return.
type Block struct {
	Stmts []Stmt
	Ret   *ReturnStmt
	S     token.Span
}

func (n *Block) Span() token.Span { return n.S }

// ---------- Statements ----------

type ReturnStmt struct {
	S     token.Span
	Exprs []Expr
}

func (*ReturnStmt) stmtNode()       {}
func (n *ReturnStmt) Span() token.Span { return n.S }

type EmptyStmt struct{ S token.Span }

func (*EmptyStmt) stmtNode()         {}
func (n *EmptyStmt) Span() token.Span { return n.S }

type ExprStmt struct {
	E Expr
	S token.Span
}

func (*ExprStmt) stmtNode()         {}
func (n *ExprStmt) Span() token.Span { return n.S }

type AssignStmt struct {
	Targets []Expr
	Values  []Expr
	S       token.Span
}

func (*AssignStmt) stmtNode()         {}
func (n *AssignStmt) Span() token.Span { return n.S }

type LocalAssign struct {
	Names  []*Ident
	Attrs  []string // e.g. "const", "close"; "" if none
	Types  []TypeNode
	Values []Expr
	S      token.Span
}

func (*LocalAssign) stmtNode()         {}
func (n *LocalAssign) Span() token.Span { return n.S }

type LocalFuncDef struct {
	Name     *Ident
	Generics []*GenericParam
	Func     *FuncLit
	Exported bool
	S        token.Span
}

func (*LocalFuncDef) stmtNode()         {}
func (n *LocalFuncDef) Span() token.Span { return n.S }

type FuncDefStmt struct {
	Name     *FuncName
	Generics []*GenericParam
	Func     *FuncLit
	S        token.Span
}

func (*FuncDefStmt) stmtNode()         {}
func (n *FuncDefStmt) Span() token.Span { return n.S }

// FuncName is a.b.c or a.b:c
type FuncName struct {
	Prefix []*Ident
	Method *Ident // non-nil if colon form
	S      token.Span
}

func (n *FuncName) Span() token.Span { return n.S }

type DoBlock struct {
	Block *Block
	S     token.Span
}

func (*DoBlock) stmtNode()         {}
func (n *DoBlock) Span() token.Span { return n.S }

type IfStmt struct {
	Cond    Expr
	Then    *Block
	Elseifs []*ElseifClause
	Else    *Block
	S       token.Span
}

func (*IfStmt) stmtNode()         {}
func (n *IfStmt) Span() token.Span { return n.S }

type ElseifClause struct {
	Cond  Expr
	Block *Block
	S     token.Span
}

type WhileStmt struct {
	Cond  Expr
	Block *Block
	S     token.Span
}

func (*WhileStmt) stmtNode()         {}
func (n *WhileStmt) Span() token.Span { return n.S }

type RepeatStmt struct {
	Block *Block
	Cond  Expr
	S     token.Span
}

func (*RepeatStmt) stmtNode()         {}
func (n *RepeatStmt) Span() token.Span { return n.S }

type NumericFor struct {
	Var   *Ident
	VarTy TypeNode
	Start Expr
	End   Expr
	Step  Expr // may be nil
	Block *Block
	S     token.Span
}

func (*NumericFor) stmtNode()         {}
func (n *NumericFor) Span() token.Span { return n.S }

type GenericFor struct {
	Names []*Ident
	Types []TypeNode
	Exprs []Expr
	Block *Block
	S     token.Span
}

func (*GenericFor) stmtNode()         {}
func (n *GenericFor) Span() token.Span { return n.S }

type BreakStmt struct{ S token.Span }

func (*BreakStmt) stmtNode()         {}
func (n *BreakStmt) Span() token.Span { return n.S }

type GotoStmt struct {
	Label *Ident
	S     token.Span
}

func (*GotoStmt) stmtNode()         {}
func (n *GotoStmt) Span() token.Span { return n.S }

type LabelStmt struct {
	Name *Ident
	S    token.Span
}

func (*LabelStmt) stmtNode()         {}
func (n *LabelStmt) Span() token.Span { return n.S }

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

// ---------- Classes ----------

type Visibility int

const (
	Public Visibility = iota
	Private
	Protected
)

type ClassDecl struct {
	Name      *Ident
	Generics  []*GenericParam
	Extends   TypeNode // nil if none
	Implements []TypeNode
	Members   []*ClassMember
	Exported  bool
	Declared  bool
	Abstract  bool
	IsLocal   bool // `local class` (or class in non-global scope intent)
	S         token.Span
}

func (*ClassDecl) stmtNode()          {}
func (n *ClassDecl) Span() token.Span { return n.S }

type ClassMemberKind int

const (
	ClassField ClassMemberKind = iota
	ClassMethod
	ClassConstructor
)

type ClassMember struct {
	Kind       ClassMemberKind
	Name       *Ident // method/field name; "constructor" for constructors
	Visibility Visibility
	IsStatic   bool
	IsReadonly bool
	IsAbstract bool
	// Field:
	FieldType TypeNode // nil => infer from Init
	Init      Expr     // nil if none
	// Method / constructor:
	Generics   []*GenericParam
	Params     []*Param
	HasVarArg  bool
	VarArgName *Ident
	VarArgType TypeNode
	ReturnType TypeNode // nil for constructors or unannotated
	Body       *Block   // nil for abstract/declare (no body)
	S          token.Span
}

func (n *ClassMember) Span() token.Span { return n.S }
func (n *ClassMember) IsConstructor() bool { return n.Kind == ClassConstructor }

type GenericParam struct {
	Name       *Ident
	Constraint TypeNode
	Default    TypeNode
	S          token.Span
}

func (n *GenericParam) Span() token.Span { return n.S }

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

// IsLValue reports whether e can appear on the left of `=`.
func IsLValue(e Expr) bool {
	switch e.(type) {
	case *Ident, *IndexExpr, *FieldExpr:
		return true
	}
	return false
}
