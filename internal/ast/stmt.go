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

// DeferStmt is `defer <call>` / `errdefer <call>` (soft keywords):// cleanup runs LIFO at function exit — deferred calls on every normal
// return, errdeferred calls only when leaving via an explicit `error()`.
// `defer`/`errdefer` stay plain identifiers everywhere else.
type DeferStmt struct {
	IsErr bool // errdefer
	Call  Expr // CallExpr or MethodCallExpr
	S     token.Span
}

func (*DeferStmt) stmtNode()          {}
func (n *DeferStmt) Span() token.Span { return n.S }

// ImportStmt is require sugar:
//
//	import "path"                 (side-effect require)
//	import Name from "path"       (local binding, any)
//	import Name: Type from "path" (annotated binding)
// It desugars to `local Name = require("path")` (bare require for the
// side-effect form); `import` stays a plain identifier elsewhere.
type ImportStmt struct {
	Name *Ident    // nil for side-effect imports
	Type TypeNode  // optional annotation
	Path *StringLit
	S    token.Span
}

func (*ImportStmt) stmtNode()          {}
func (n *ImportStmt) Span() token.Span { return n.S }

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

// SpawnStmt is `spawn <call>` (soft keyword): spawns the call as a
// concurrent task on the cooperative scheduler. Arguments are captured
// lazily in a closure (same convention as `defer`: see SPEC §12), i.e.
// evaluated when the task starts, not at the `spawn` site.
// `spawn` stays a plain identifier elsewhere (`local spawn = 1`, `spawn(x)`).
type SpawnStmt struct {
	Call Expr // CallExpr or MethodCallExpr (may carry explicit type args)
	S    token.Span
}

func (*SpawnStmt) stmtNode()         {}
func (n *SpawnStmt) Span() token.Span { return n.S }

// SelectStmt is Go-style `select { case ... default ... }` over channels:
// each case is one send / receive / timeout guard plus a body. It suspends
// the current async task until one guard is ready (or `default` fires
// immediately). Only valid inside `async` (checker-enforced).
//
// Source form (braces, no `end`, mirroring class/enum bodies):
//
//	select {
//	  case v = ch:recv() then
//	    print(v)
//	  case ch:send(x) then
//	    print("sent")
//	  case timeout(1.0) then
//	    print("timed out")
//	  default then
//	    print("busy")
//	}
type SelectStmt struct {
	Cases   []*SelectCase
	Default *Block
	DS      token.Span // span of `default` (zero if absent)
	S       token.Span
}

func (*SelectStmt) stmtNode()         {}
func (n *SelectStmt) Span() token.Span { return n.S }

// SelectCase is one `case <guard> then <block>` in a select.
type SelectCase struct {
	// Guard kinds (exactly one non-nil, or Timeout):
	//   Recv: `v = ch:recv()` / `v, ok = ch:recv()` (RecvVars 1-2 idents)
	//   Send: `ch:send(v)` (MethodCallExpr, method "send")
	//   Timeout: `sleep(sec)` / `timeout(sec)` (CallExpr, 1 numeric arg)
	Recv      *MethodCallExpr // method must be "recv"
	RecvVars  []*Ident        // 1-2 locals bound in the case body
	RecvLocal bool            // `case local v = ...`
	Send      *MethodCallExpr // method must be "send"
	Timeout   Expr            // seconds expression
	Block     *Block
	S         token.Span
}

func (n *SelectCase) Span() token.Span { return n.S }
