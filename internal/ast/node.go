package ast

import (
	"github.com/typelua/typelua/internal/token"
)

// 基节点三件：Node/File/Block，Span 全员有。

// 节点详见实现，排查看 Span。
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

// 文件详见实现，排查看 Span。
type File struct {
	Name  string
	Block *Block
	EOF   token.Span
}

// Span 取源码区间，如 `x=1` 圈整句供诊断画线。
//
// 设计者视角：File 是“一源一文件”的根容器，好比档案袋封面——Name 标明归属，
// Block 装正文，EOF 兜底空文件。障碍是空文件无语句可取坐标：若直接解引用
// Block 会 panic，故 Block 为 nil 时退守 EOF，保证任何 File 都有可报的 Span。
// 候选：存 token 指针 vs 存值 Span；选值 Span，让合成/空文件也能造坐标，且
// checker/emitter/LSP 只依赖 Span() 这一扇门。不变量：返回的 Span.File 恒为
// Name（覆盖 Block 自带的 File）。排查现场：若跨文件诊断串了文件，先看此处
// File 回填是否被绕过，次看 parser 的 EOF Span 是否带对文件名。
func (n *File) Span() token.Span {
	if n.Block == nil {
		return n.EOF
	}
	s := n.Block.Span()
	s.File = n.Name
	return s
}

// Block 是语句列+可选尾 return，如 `a=1; return a`。
type Block struct {
	Stmts []Stmt
	Ret   *ReturnStmt
	S     token.Span
}

// Span 取源码区间，如 `x=1` 圈整句供诊断画线。
//
// 设计者视角：Block 是“语句序列 + 可选 return”的扁容器，好比火车车厢挂钩——
// Stmts 只装普通语句，Ret 独立存放，把“return 必须在块末”的 Lua 语法直接编码
// 进数据结构，想放错位置都没地方放。此处直接返回预拼好的 S，避免每次遍历重算
// Start/End。取舍：不存父指针，遍历方向唯一自顶向下，需要上下文由 checker/
// emitter 自己压栈换取零耦合。不变量：Stmts 永不含 ReturnStmt，Ret 为 nil 即无
// 返回。排查现场：若 return 行号漂移，检查 parser 拼 S 时是否把 Ret 的 End 纳入。
func (n *Block) Span() token.Span { return n.S }
