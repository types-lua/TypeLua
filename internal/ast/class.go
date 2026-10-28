package ast

import (
	"github.com/typelua/typelua/internal/token"
)

// Class declaration nodes.

// ---------- Classes ----------

// Visibility 表示类成员的可见性修饰（public/private/protected），用于检查器与发射器的访问控制。
type Visibility int

const (
	Public Visibility = iota
	Private
	Protected
)

// ClassDecl 表示 `class` 声明语句节点；S 为源码 Span，用于诊断与定位。
type ClassDecl struct {
	Name       *Ident
	Generics   []*GenericParam
	Extends    TypeNode // nil if none
	Implements []TypeNode
	Members    []*ClassMember
	Exported   bool
	Declared   bool
	Abstract   bool
	IsLocal    bool // `local class` (or class in non-global scope intent)
	S          token.Span
}

func (*ClassDecl) stmtNode() {}

// Span returns the source span of this node.
func (n *ClassDecl) Span() token.Span { return n.S }

// ClassMemberKind 区分类成员的种类（字段/方法/构造器），决定后续字段的解释方式。
type ClassMemberKind int

const (
	ClassField ClassMemberKind = iota
	ClassMethod
	ClassConstructor
)

// ClassMember 表示类体中的一个成员（字段/方法/构造器）；S 为源码 Span，用于诊断与定位。
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
	IsAsync    bool     // `async` method: may await; erases to plain method
	S          token.Span
}

// Span returns the source span of this node.
func (n *ClassMember) Span() token.Span { return n.S }

// IsConstructor 报告该成员是否为构造器，供发射器生成 __init 调用约定。
func (n *ClassMember) IsConstructor() bool { return n.Kind == ClassConstructor }

// GenericParam 表示泛型参数声明（名称 + 约束 + 默认值）；S 为源码 Span，用于诊断与定位。
type GenericParam struct {
	Name       *Ident
	Constraint TypeNode
	Default    TypeNode
	S          token.Span
}

// Span returns the source span of this node.
func (n *GenericParam) Span() token.Span { return n.S }
