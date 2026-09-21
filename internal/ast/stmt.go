package ast

import (
	"github.com/typelua/typelua/internal/token"
)

// Statement nodes.

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
