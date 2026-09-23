package checker

import (
	"github.com/typelua/typelua/internal/ast"
)

// AST walkers for constructor analysis (super/self) and infer names.

// ---------- class constructor analysis walkers ----------
// These skip nested function/class bodies: only code running during
// construction itself counts for coverage and super() placement.

func isSuperCall(e ast.Expr) bool {
	call, ok := e.(*ast.CallExpr)
	if !ok {
		return false
	}
	id, ok := call.Func.(*ast.Ident)
	return ok && id.Name == "super"
}

func blockHasSuperCall(b *ast.Block) bool {
	if b == nil {
		return false
	}
	for _, st := range b.Stmts {
		if stmtHasSuperCall(st) {
			return true
		}
	}
	if b.Ret != nil {
		for _, e := range b.Ret.Exprs {
			if exprHasSuperCall(e) {
				return true
			}
		}
	}
	return false
}

// topLevelSuperIndex finds a direct `super(...)` statement in a constructor
// body for self-before-super ordering. -1 when nested only.
func topLevelSuperIndex(b *ast.Block) int {
	if b == nil {
		return -1
	}
	for i, st := range b.Stmts {
		if es, ok := st.(*ast.ExprStmt); ok && isSuperCall(es.E) {
			return i
		}
	}
	return -1
}

func stmtHasSuperCall(st ast.Stmt) bool {
	switch n := st.(type) {
	case *ast.ExprStmt:
		return exprHasSuperCall(n.E)
	case *ast.AssignStmt:
		for _, t := range n.Targets {
			if exprHasSuperCall(t) {
				return true
			}
		}
		for _, v := range n.Values {
			if exprHasSuperCall(v) {
				return true
			}
		}
	case *ast.LocalAssign:
		for _, v := range n.Values {
			if exprHasSuperCall(v) {
				return true
			}
		}
	case *ast.DoBlock:
		return blockHasSuperCall(n.Block)
	case *ast.IfStmt:
		if exprHasSuperCall(n.Cond) || blockHasSuperCall(n.Then) {
			return true
		}
		for _, ei := range n.Elseifs {
			if exprHasSuperCall(ei.Cond) || blockHasSuperCall(ei.Block) {
				return true
			}
		}
		if n.Else != nil && blockHasSuperCall(n.Else) {
			return true
		}
	case *ast.WhileStmt:
		return exprHasSuperCall(n.Cond) || blockHasSuperCall(n.Block)
	case *ast.RepeatStmt:
		return blockHasSuperCall(n.Block) || exprHasSuperCall(n.Cond)
	case *ast.NumericFor:
		return exprHasSuperCall(n.Start) || exprHasSuperCall(n.End) ||
			(n.Step != nil && exprHasSuperCall(n.Step)) || blockHasSuperCall(n.Block)
	case *ast.GenericFor:
		for _, e := range n.Exprs {
			if exprHasSuperCall(e) {
				return true
			}
		}
		return blockHasSuperCall(n.Block)
	}
	return false
}

func exprHasSuperCall(e ast.Expr) bool {
	switch n := e.(type) {
	case *ast.CallExpr:
		if isSuperCall(e) {
			return true
		}
		if exprHasSuperCall(n.Func) {
			return true
		}
		for _, a := range n.Args {
			if exprHasSuperCall(a) {
				return true
			}
		}
	case *ast.MethodCallExpr:
		if exprHasSuperCall(n.Obj) {
			return true
		}
		for _, a := range n.Args {
			if exprHasSuperCall(a) {
				return true
			}
		}
	case *ast.BinOpExpr:
		return exprHasSuperCall(n.L) || exprHasSuperCall(n.R)
	case *ast.UnOpExpr:
		return exprHasSuperCall(n.X)
	case *ast.ParenExpr:
		return exprHasSuperCall(n.E)
	case *ast.IndexExpr:
		return exprHasSuperCall(n.Obj) || exprHasSuperCall(n.Key)
	case *ast.FieldExpr:
		return exprHasSuperCall(n.Obj)
	case *ast.TableLit:
		for _, f := range n.Fields {
			if f.KeyX != nil && exprHasSuperCall(f.KeyX) {
				return true
			}
			if exprHasSuperCall(f.Val) {
				return true
			}
		}
	case *ast.CastExpr:
		return exprHasSuperCall(n.E)
	case *ast.NonNullExpr:
		return exprHasSuperCall(n.E)
	case *ast.AwaitExpr:
		return exprHasSuperCall(n.X)
	case *ast.FuncLit, *ast.Ident, *ast.NumberLit, *ast.StringLit,
		*ast.BoolLit, *ast.NilLit, *ast.VarArg:
	}
	return false
}

// collectSelfAssigns gathers `self.name = ...` targets (construction-time).
func collectSelfAssigns(b *ast.Block, into map[string]bool) {
	if b == nil {
		return
	}
	for _, st := range b.Stmts {
		collectStmtSelfAssigns(st, into)
	}
}

func collectStmtSelfAssigns(st ast.Stmt, into map[string]bool) {
	switch n := st.(type) {
	case *ast.AssignStmt:
		for _, t := range n.Targets {
			if fe, ok := t.(*ast.FieldExpr); ok {
				if id, ok := fe.Obj.(*ast.Ident); ok && id.Name == "self" {
					into[fe.Field.Name] = true
				}
			}
		}
	case *ast.DoBlock:
		collectSelfAssigns(n.Block, into)
	case *ast.IfStmt:
		collectSelfAssigns(n.Then, into)
		for _, ei := range n.Elseifs {
			collectSelfAssigns(ei.Block, into)
		}
		if n.Else != nil {
			collectSelfAssigns(n.Else, into)
		}
	case *ast.WhileStmt:
		collectSelfAssigns(n.Block, into)
	case *ast.RepeatStmt:
		collectSelfAssigns(n.Block, into)
	case *ast.NumericFor:
		collectSelfAssigns(n.Block, into)
	case *ast.GenericFor:
		collectSelfAssigns(n.Block, into)
	}
}

// stmtUsesSelf reports textual `self` use (for super-before-self).
func stmtUsesSelf(st ast.Stmt) bool {
	switch n := st.(type) {
	case *ast.ExprStmt:
		return exprUsesSelf(n.E)
	case *ast.AssignStmt:
		for _, t := range n.Targets {
			if exprUsesSelf(t) {
				return true
			}
		}
		for _, v := range n.Values {
			if exprUsesSelf(v) {
				return true
			}
		}
	case *ast.LocalAssign:
		for _, v := range n.Values {
			if exprUsesSelf(v) {
				return true
			}
		}
	case *ast.DoBlock:
		return blockUsesSelf(n.Block)
	case *ast.IfStmt:
		if exprUsesSelf(n.Cond) || blockUsesSelf(n.Then) {
			return true
		}
		for _, ei := range n.Elseifs {
			if exprUsesSelf(ei.Cond) || blockUsesSelf(ei.Block) {
				return true
			}
		}
		if n.Else != nil && blockUsesSelf(n.Else) {
			return true
		}
	case *ast.WhileStmt:
		return exprUsesSelf(n.Cond) || blockUsesSelf(n.Block)
	case *ast.RepeatStmt:
		return blockUsesSelf(n.Block) || exprUsesSelf(n.Cond)
	case *ast.NumericFor:
		return exprUsesSelf(n.Start) || exprUsesSelf(n.End) ||
			(n.Step != nil && exprUsesSelf(n.Step)) || blockUsesSelf(n.Block)
	case *ast.GenericFor:
		for _, e := range n.Exprs {
			if exprUsesSelf(e) {
				return true
			}
		}
		return blockUsesSelf(n.Block)
	case *ast.ReturnStmt:
		for _, e := range n.Exprs {
			if exprUsesSelf(e) {
				return true
			}
		}
	}
	return false
}

func blockUsesSelf(b *ast.Block) bool {
	if b == nil {
		return false
	}
	for _, st := range b.Stmts {
		if stmtUsesSelf(st) {
			return true
		}
	}
	if b.Ret != nil {
		for _, e := range b.Ret.Exprs {
			if exprUsesSelf(e) {
				return true
			}
		}
	}
	return false
}

func exprUsesSelf(e ast.Expr) bool {
	switch n := e.(type) {
	case *ast.Ident:
		return n.Name == "self"
	case *ast.CallExpr:
		if exprUsesSelf(n.Func) {
			return true
		}
		for _, a := range n.Args {
			if exprUsesSelf(a) {
				return true
			}
		}
	case *ast.MethodCallExpr:
		if exprUsesSelf(n.Obj) {
			return true
		}
		for _, a := range n.Args {
			if exprUsesSelf(a) {
				return true
			}
		}
	case *ast.BinOpExpr:
		return exprUsesSelf(n.L) || exprUsesSelf(n.R)
	case *ast.UnOpExpr:
		return exprUsesSelf(n.X)
	case *ast.ParenExpr:
		return exprUsesSelf(n.E)
	case *ast.IndexExpr:
		return exprUsesSelf(n.Obj) || exprUsesSelf(n.Key)
	case *ast.FieldExpr:
		return exprUsesSelf(n.Obj)
	case *ast.TableLit:
		for _, f := range n.Fields {
			if f.KeyX != nil && exprUsesSelf(f.KeyX) {
				return true
			}
			if exprUsesSelf(f.Val) {
				return true
			}
		}
	case *ast.CastExpr:
		return exprUsesSelf(n.E)
	case *ast.NonNullExpr:
		return exprUsesSelf(n.E)
	case *ast.AwaitExpr:
		return exprUsesSelf(n.X)
	case *ast.FuncLit, *ast.NumberLit, *ast.StringLit,
		*ast.BoolLit, *ast.NilLit, *ast.VarArg:
	}
	return false
}

// collectInferNames gathers `infer U` names in an extends clause.
// Nested conditionals bind their own scope and are not descended into.
func collectInferNames(n ast.TypeNode, into map[string]bool) map[string]bool {
	if into == nil {
		into = map[string]bool{}
	}
	switch t := n.(type) {
	case *ast.InferType:
		into[t.Name.Name] = true
	case *ast.UnionType:
		for _, m := range t.Types {
			collectInferNames(m, into)
		}
	case *ast.IntersectionType:
		for _, m := range t.Types {
			collectInferNames(m, into)
		}
	case *ast.ArrayType:
		collectInferNames(t.Elem, into)
	case *ast.OptionalType:
		collectInferNames(t.Elem, into)
	case *ast.ParenType:
		collectInferNames(t.Elem, into)
	case *ast.TupleType:
		for _, e := range t.Elems {
			collectInferNames(e, into)
		}
	case *ast.TableType:
		for _, f := range t.Fields {
			if f.IsIndexer {
				collectInferNames(f.IndexKey, into)
				collectInferNames(f.IndexVal, into)
			} else if f.IsMethod && f.MethodSig != nil {
				collectInferNames(f.MethodSig, into)
			} else if f.Type != nil {
				collectInferNames(f.Type, into)
			}
		}
	case *ast.FuncType:
		for _, p := range t.Params {
			if p.Type != nil {
				collectInferNames(p.Type, into)
			}
		}
		if t.VarArgType != nil {
			collectInferNames(t.VarArgType, into)
		}
		for _, r := range t.Returns {
			collectInferNames(r, into)
		}
	case *ast.NamedType:
		for _, a := range t.Args {
			collectInferNames(a, into)
		}
	case *ast.KeyofType:
		collectInferNames(t.Elem, into)
	case *ast.MappedType:
		// Value-position infers bind in the true branch; constraint
		// infers would have nothing to bind against, skip them.
		collectInferNames(t.Value, into)
	case *ast.IndexedAccessType:
		collectInferNames(t.Obj, into)
		collectInferNames(t.Index, into)
	case *ast.ConditionalType:
		// Own scope; skip.
	}
	return into
}
