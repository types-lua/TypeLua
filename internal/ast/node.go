package ast

import (
	"github.com/typelua/typelua/internal/token"
)

// Base node interfaces, files, and blocks.

// Node is the base of all AST nodes.
type Node interface {
	Span() token.Span
}

// Stmt 是语句家族节点接口：所有语句节点实现未导出的 stmtNode() 标记方法。
type Stmt interface {
	Node
	stmtNode()
}

// Expr 是表达式家族节点接口：所有表达式节点实现未导出的 exprNode() 标记方法。
type Expr interface {
	Node
	exprNode()
}

// TypeNode 是类型语法家族节点接口：所有类型语法节点实现未导出的 typeNode() 标记方法。
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

// Span returns the source span of this node.
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

// Span returns the source span of this node.
func (n *Block) Span() token.Span { return n.S }
