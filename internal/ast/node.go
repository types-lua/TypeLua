package ast

import (
	"github.com/typelua/typelua/internal/token"
)

// Base node interfaces, files, and blocks.

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
