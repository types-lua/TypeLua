package ast

import (
	"github.com/typelua/typelua/internal/token"
)

// Class declaration nodes.

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
	IsAsync    bool     // `async` method: may await; erases to plain method
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
