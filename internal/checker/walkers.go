package checker

import (
	"github.com/typelua/typelua/internal/ast"
)

// 设计者视角：构造器分析 walker 显式跳过嵌套函数/类体，只统计构造期
// 直接执行的代码，避免把闭包内的 self 误算。

// AST walkers for constructor analysis (super/self) and infer names.
//
// 设计者视角：walker 是构造器分析的“三探针”——super 存在（blockHas/stmtHas/exprHas）、
// self 赋值/使用（collectSelfAssigns/stmtUsesSelf 系）、infer 名收集。每系皆跳过
// 嵌套函数/类体（闭包内代码非构造期直行，误算会把回调内 self 当初始化）。障碍是
// 新语句形易漏分支：漏即分析缺（super 漏报/字段误报）。取舍：手写递归（显式跳
// FuncLit）。图：
//
//	Block → Stmt → Expr 三层透视，遇 FuncLit 即止
//
// 排查现场：super 在回调内被算先查是否忘止嵌套。

// ---------- class constructor analysis walkers ----------
// 这些跳过嵌套函数/类体：只有构造过程中实际运行的代码才计入覆盖与
// super() 位置。

// isSuperCall reports a bare `super(...)` call (constructor ignition shape).
//
// 设计者视角：isSuperCall 只认裸 Ident `super` 的 Call（`super:method` 另属方法系）。
// 形认（快），义（是否派生构造内）由调用方判。排查现场：super:method 误入此处先查
// Call vs MethodCall 分流。
func isSuperCall(e ast.Expr) bool {
	call, ok := e.(*ast.CallExpr)
	if !ok {
		return false
	}
	id, ok := call.Func.(*ast.Ident)
	return ok && id.Name == "super"
}

// blockHasSuperCall reports any super call in a block (deep, nesting-aware).
//
// 设计者视角：blockHasSuperCall 是存在性“地毯搜”——语句+返回双覆，嵌套函数内止步
// （exprHas 遇 FuncLit 不入）。派生构造必须调 super 的存在判据即此。排查现场：
// 回调内 super 被算先查 FuncLit 止步。
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
//
// 设计者视角：topLevelSuperIndex 只看顶层语句的裸 `super(...)`（ExprStmt 直子），
// 嵌套（if/do 内）不算——self-before-super 只管顶层序（嵌套内 self 另难精判）。
// -1 表无顶层。排查现场：`if c then super() end` 前的 self 误报属设计从宽（只查顶层）。

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

// stmtHasSuperCall reports super in one statement (deep, function-nesting stops).
//
// 设计者视角：语句级分流—— control 块透视（if/while/for/do），赋值/局部验值，
// 余者假（return/goto/label 不含）。FuncLit 由 exprHas 止。排查现场：新语句形漏
// super 先补此分支。
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

// exprHasSuperCall reports super in one expression (FuncLit stops recursion).
//
// 设计者视角：表达式级透视——调用/方法/二目/表/ cast/await 递归，字面/变量止，
// FuncLit 恒止（闭包非构造期）。与 stmtUsesSelf 系对偶（彼查 self，此查 super）。
// 排查现场：回调内 super 误算先查此止步。
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
//
// 设计者视角：collectSelfAssigns 收构造期 `self.x=` 以验必填字段——块逐句，
// 控制块透视，嵌套函数不入（回调内赋值非构造保证）。只认 Field 基为裸 self。
// 排查现场：必填误报先查赋值是否在回调内（设计不计）。
func collectSelfAssigns(b *ast.Block, into map[string]bool) {
	if b == nil {
		return
	}
	for _, st := range b.Stmts {
		collectStmtSelfAssigns(st, into)
	}
}

// collectStmtSelfAssigns collects self-assigns in one statement (control透视).
//
// 设计者视角：语句级收集——Assign 验靶，控制块透视，余者无。
// 与 super 系同骨（跳函数）。排查现场：分支内 `self.x=` 漏收先查控制透视是否缺分支。
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
//
// 设计者视角：stmtUsesSelf 是 self-before-super 的“嗅探”——super 前语句含 self
// 文本即错。语句/块/表达式三层透视（含 Return），FuncLit 止？此处 exprUsesSelf
// 对 FuncLit 止（闭包内 self 非顶层序，见 topLevelSuperIndex 从宽）。取舍：文本级
// （不辨作用域，宁严）。排查现场：super 前闭包用 self 误报属文本级从严（设计如此）。
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

// blockUsesSelf reports textual self use in a block (returns included).
//
// 设计者视角：块级 self 存在判——语句逐个，返回亦验。供 stmtUsesSelf 的块透视。
// 排查现场同上。
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

// exprUsesSelf reports textual self use in an expression (FuncLit stops).
//
// 设计者视角：表达式级 self 嗅探——Ident `self` 即中，余递归，FuncLit 止。
// 排查现场：回调内 self 被算/不算皆由此止步位定。
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
//
// 设计者视角：collectInferNames 收真支待绑的 infer 名——union/交叉/数组/可选/括号/
// 元组/表/函数/具名参/ keyof/映射值/索引双端递归，嵌套条件止（自有域）。映射只收
// 值位（约束位无可绑）。障碍是嵌套条件误收会串域：止步即对。排查现场：真支 U 未绑
// 先查 extends 形是否嵌套条件内。

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
