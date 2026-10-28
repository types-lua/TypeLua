package checker

import (
	"github.com/typelua/typelua/internal/ast"
	"github.com/typelua/typelua/internal/diag"
	"github.com/typelua/typelua/internal/types"
)

// 设计者视角：语句检查维护 retStack/labelStack/gotoList 三栈；if 的
// 分支发散信息用于流敏感窄化（return 后条件翻转）。

// Statement checking (locals, assignment, control flow, return).

// ---------- statements ----------

func (c *Checker) checkStmt(st ast.Stmt) {
	if st == nil {
		// Recovery artifact (parser already reported); never crash.
		return
	}
	switch n := st.(type) {
	case *ast.EmptyStmt:
	case *ast.ExprStmt:
		c.checkExpr(n.E)
	case *ast.LocalAssign:
		c.checkLocalAssign(n)
	case *ast.LocalFuncDef:
		c.checkLocalFunc(n)
	case *ast.FuncDefStmt:
		c.checkGlobalFunc(n)
	case *ast.AssignStmt:
		c.checkAssign(n)
	case *ast.DoBlock:
		c.push()
		c.checkBlock(n.Block)
		c.pop()
	case *ast.IfStmt:
		c.checkIf(n)
	case *ast.WhileStmt:
		condT := c.checkExpr(n.Cond)
		_ = condT
		c.push()
		c.applyNarrowing(n.Cond, true)
		c.checkBlock(n.Block)
		c.pop()
		c.loopDepth++
		_ = c.loopDepth
		c.loopDepth--
	case *ast.RepeatStmt:
		c.push()
		c.checkBlock(n.Block)
		condT := c.checkExpr(n.Cond)
		_ = condT
		c.pop()
	case *ast.NumericFor:
		c.checkNumericFor(n)
	case *ast.GenericFor:
		c.checkGenericFor(n)
	case *ast.BreakStmt:
		// TODO: verify inside loop; we track loosely.
	case *ast.GotoStmt:
		if len(c.gotoList) > 0 {
			c.gotoList[len(c.gotoList)-1] = append(c.gotoList[len(c.gotoList)-1], gotoRef{name: n.Label.Name, span: n.Span()})
		}
	case *ast.LabelStmt:
		if len(c.labelStack) > 0 {
			top := c.labelStack[len(c.labelStack)-1]
			if _, ok := top[n.Name.Name]; ok {
				c.errorf(diag.DuplicateLabel, n.Span(), "duplicate label '%s'", n.Name.Name)
			} else {
				top[n.Name.Name] = n.Span()
			}
		}
	case *ast.TypeAliasDecl:
		c.checkTypeAlias(n)
	case *ast.InterfaceDecl:
		c.checkInterface(n)
	case *ast.EnumDecl:
		c.checkEnum(n)
	case *ast.ClassDecl:
		c.checkClass(n)
	case *ast.MacroDef:
		// Unreachable via CheckFile (expansion runs first); defensive.
		c.errorf(diag.UnexpandedMacro, st.Span(), "internal error: unexpanded macro '%s'", n.Name.Name)
	case *ast.ImportStmt:
		c.checkImport(n)
	case *ast.DeferStmt:
		c.checkDefer(n)
	case *ast.SpawnStmt:
		c.checkSpawn(n)
	case *ast.SelectStmt:
		c.checkSelect(n)
	default:
		c.errorf(diag.CheckerUnsupportedStmt, st.Span(), "unsupported statement %T", st)
	}
}

func (c *Checker) checkLocalAssign(n *ast.LocalAssign) {
	var declTypes []types.Type
	for _, tn := range n.Types {
		if tn == nil {
			declTypes = append(declTypes, nil)
		} else {
			declTypes = append(declTypes, c.resolveType(tn))
		}
	}
	var valTypes []types.Type
	if len(n.Values) > 0 {
		valTypes = c.checkExprListMulti(n.Values)
	}
	for i, name := range n.Names {
		var want types.Type
		if i < len(declTypes) {
			want = declTypes[i]
		}
		var got types.Type
		if i < len(valTypes) {
			got = valTypes[i]
		} else if len(n.Values) > 0 {
			got = types.Nil // missing trailing values are nil
		}
		var final types.Type
		if want != nil && got != nil {
			if !types.IsAssignable(got, want, c.opt.toTypes()) {
				c.errorf(diag.TypeNotAssignable, name.Span(), "type '%s' is not assignable to type '%s'", got.String(), want.String())
			}
			final = want
		} else if want != nil {
			final = want
		} else if got != nil {
			// Unannotated inference: list literals become growable arrays (not fixed tuples),
			// and literal members are widened to avoid over-narrowing.
			if i < len(n.Values) {
				if lit, ok := n.Values[i].(*ast.TableLit); ok && isPureList(lit) {
					if tup, ok := got.(*types.Tuple); ok {
						final = &types.Array{Elem: widenInferred(types.NewUnion(tup.Elems))}
					} else {
						final = widenInferred(got)
					}
				} else {
					final = widenInferred(got)
				}
			} else {
				// Multi-expanded value beyond single node (e.g. from call): widen.
				final = widenInferred(got)
			}
		} else {
			final = types.Nil
		}
		isConst := i < len(n.Attrs) && n.Attrs[i] == "const"
		if existing := c.cur().vars[name.Name]; existing != nil && !existing.IsBuiltin {
			// Shadowing in same scope: error (Lua allows, but likely bug).
			// Builtins (print/next/...) may be localized, so exempt them.
			c.errorf(diag.RedefinitionOfLocal, name.Span(), "redefinition of local '%s'", name.Name)
		}
		if i < len(n.Values) {
			if tl, ok := n.Values[i].(*ast.TableLit); ok {
				c.recordTableKeys(tl, want)
			}
		}
		bound := c.bindVar(name.Name, &Symbol{Name: name.Name, Kind: symVar, Type: final, Span: name.Span(), IsConst: isConst})
		c.recordDef(name.Span(), bound)
		// Anonymous literals also define structural members under the
		// variable (unless a nominal type owns them; see recordTableKeys).
		if i < len(n.Values) {
			if tl, ok := n.Values[i].(*ast.TableLit); ok && c.ownerEntry(final) == nil {
				c.recordStructLiteral(symRoot(bound), tl)
			}
		}
	}
	if len(valTypes) > len(n.Names) && len(n.Names) > 0 {
		// Extra values dropped: allowed in Lua, no error.
	}
	// Check const/close misuse: <close> must have a value? Lua requires? Just warn if no value.
	for i, a := range n.Attrs {
		if a == "close" && len(n.Values) == 0 && i < len(n.Names) {
			c.errorf(diag.CloseVarUninitialized, n.Names[i].Span(), "to-be-closed variable '%s' must be initialized", n.Names[i].Name)
		}
	}
}

func widen(t types.Type) types.Type {
	if lit, ok := t.(*types.Literal); ok {
		return lit.Base
	}
	return t
}

// widenInferred deep-widens literals for unannotated inference to avoid over-narrowing.
func widenInferred(t types.Type) types.Type {
	switch x := t.(type) {
	case *types.Literal:
		return x.Base
	case *types.Union:
		var ms []types.Type
		for _, m := range x.Types {
			ms = append(ms, widenInferred(m))
		}
		return types.NewUnion(ms)
	case *types.Array:
		return &types.Array{Elem: widenInferred(x.Elem)}
	case *types.Tuple:
		es := make([]types.Type, len(x.Elems))
		for i, e := range x.Elems {
			es[i] = widenInferred(e)
		}
		return &types.Tuple{Elems: es}
	case *types.Map:
		return &types.Map{Key: widenInferred(x.Key), Value: widenInferred(x.Value)}
	case *types.Object:
		nf := map[string]types.Field{}
		for n, f := range x.Fields {
			nf[n] = types.Field{Type: widenInferred(f.Type), Optional: f.Optional, Readonly: f.Readonly, Access: f.Access, Owner: f.Owner}
		}
		var nix []types.Indexer
		for _, ix := range x.Indexers {
			nix = append(nix, types.Indexer{Key: widenInferred(ix.Key), Value: widenInferred(ix.Value)})
		}
		return &types.Object{Name: x.Name, Fields: nf, Indexers: nix}
	case *types.Class:
		na := make([]types.Type, len(x.Args))
		for i, a := range x.Args {
			na[i] = widenInferred(a)
		}
		return &types.Class{Def: x.Def, Name: x.Name, Args: na, Params: x.Params, Fields: x.Fields, Base: x.Base, IsAbstract: x.IsAbstract}
	case *types.ClassValue:
		return x
	default:
		return widen(t)
	}
}

func isPureList(lit *ast.TableLit) bool {
	for _, f := range lit.Fields {
		if f.Kind != ast.ExprField {
			return false
		}
	}
	return true
}

// checkImport binds `import Name [: Type] from "path"` like
// `local Name [: Type] = require("path")`. Without an annotation the
// driver-resolved module type applies (any when unresolved); with one,
// the resolved type must satisfy it.
func (c *Checker) checkImport(n *ast.ImportStmt) {
	if n.Name == nil {
		return
	}
	want := types.Type(types.Any)
	if n.Type != nil {
		want = c.resolveType(n.Type)
		if got := c.requirePathType(n.Path); !isAnyLike(got) {
			if !types.IsAssignable(got, want, c.opt.toTypes()) {
				c.errorf(diag.TypeNotAssignable, n.Name.Span(), "type '%s' is not assignable to type '%s'", got.String(), want.String())
			}
		}
	} else {
		want = c.requirePathType(n.Path)
	}
	if existing := c.cur().vars[n.Name.Name]; existing != nil && !existing.IsBuiltin {
		c.errorf(diag.RedefinitionOfLocal, n.Name.Span(), "redefinition of local '%s'", n.Name.Name)
	}
	c.recordDef(n.Name.Span(), c.bindVar(n.Name.Name, &Symbol{Name: n.Name.Name, Kind: symVar, Type: want, Span: n.Name.Span()}))
}

// checkDefer checks a deferred call like a normal call (argument checking
// included). Bare `...` cannot cross into the deferred closure (it would
// bind the closure's nonexistent varargs); copy into a local first.
func (c *Checker) checkDefer(n *ast.DeferStmt) {
	c.checkExpr(n.Call)
	if hasBareVarArg(n.Call) {
		c.errorf(diag.BareVarargInTaskCall, n.Call.Span(), "cannot use '...' in a deferred call (copy it into a local first)")
	}
}

// hasBareVarArg reports a `...` outside nested function bodies.
func hasBareVarArg(x ast.Expr) bool {
	switch n := x.(type) {
	case *ast.VarArg:
		return true
	case *ast.FuncLit:
		return false
	case *ast.ParenExpr:
		return hasBareVarArg(n.E)
	case *ast.CallExpr:
		if hasBareVarArg(n.Func) {
			return true
		}
		for _, a := range n.Args {
			if hasBareVarArg(a) {
				return true
			}
		}
	case *ast.MethodCallExpr:
		if hasBareVarArg(n.Obj) {
			return true
		}
		for _, a := range n.Args {
			if hasBareVarArg(a) {
				return true
			}
		}
	case *ast.BinOpExpr:
		return hasBareVarArg(n.L) || hasBareVarArg(n.R)
	case *ast.UnOpExpr:
		return hasBareVarArg(n.X)
	case *ast.IndexExpr:
		return hasBareVarArg(n.Obj) || hasBareVarArg(n.Key)
	case *ast.FieldExpr:
		return hasBareVarArg(n.Obj)
	case *ast.TableLit:
		for _, f := range n.Fields {
			if f.KeyX != nil && hasBareVarArg(f.KeyX) {
				return true
			}
			if f.Val != nil && hasBareVarArg(f.Val) {
				return true
			}
		}
	case *ast.CastExpr:
		return hasBareVarArg(n.E)
	case *ast.NonNullExpr:
		return hasBareVarArg(n.E)
	case *ast.AwaitExpr:
		return hasBareVarArg(n.X)
	}
	return false
}

func (c *Checker) checkLocalFunc(n *ast.LocalFuncDef) {
	ft := c.funcTypeFromLit(n.Func, n.Generics)
	if existing := c.cur().vars[n.Name.Name]; existing != nil && !existing.IsBuiltin {
		c.errorf(diag.RedefinitionOfLocal, n.Name.Span(), "redefinition of local '%s'", n.Name.Name)
	}
	sym := c.bindVar(n.Name.Name, &Symbol{Name: n.Name.Name, Kind: symFunc, Type: ft, Span: n.Span(), GenericNames: genericNames(n.Generics), FuncDecl: n.Func})
	c.recordDef(n.Name.Span(), sym)
	c.checkFuncBody(n.Func, n.Generics, ft)
}

func genericNames(gs []*ast.GenericParam) []string {
	var out []string
	for _, g := range gs {
		out = append(out, g.Name.Name)
	}
	return out
}

func (c *Checker) checkGlobalFunc(n *ast.FuncDefStmt) {
	ft := c.funcTypeFromLit(n.Func, n.Generics)
	if n.Name == nil {
		return
	}
	if len(n.Name.Prefix) == 1 && n.Name.Method == nil {
		name := n.Name.Prefix[0].Name
		if g := c.cur().lookupVar(name); g != nil {
			// Redefinition of function: allow (Lua pattern), but update type.
			g.Type = ft
			c.recordDef(n.Name.Prefix[0].Span(), g)
		} else {
			// Define as global in current scope (modules use locals+return, globals rare).
			sym := c.bindVar(name, &Symbol{Name: name, Kind: symFunc, Type: ft, Span: n.Span(), GenericNames: genericNames(n.Generics), FuncDecl: n.Func})
			c.recordDef(n.Name.Prefix[0].Span(), sym)
		}
	} else {
		// a.b.c or a:b — verify base exists.
		base := n.Name.Prefix[0].Name
		sym := c.cur().lookupVar(base)
		if sym == nil {
			c.errorf(diag.UndefinedTableInFuncDef, n.Name.Span(), "undefined table '%s' in function definition", base)
		} else {
			// If base is an object, record method field for later lookups.
			if obj, ok := sym.Type.(*types.Object); ok {
				fieldName := n.Name.Prefix[len(n.Name.Prefix)-1].Name
				if n.Name.Method != nil {
					fieldName = n.Name.Method.Name
				} else if len(n.Name.Prefix) > 1 {
					// a.b.c defines c in... we only track one level; keep simple.
				}
				if obj.Fields == nil {
					obj.Fields = map[string]types.Field{}
				}
				obj.Fields[fieldName] = types.Field{Type: ft}
			}
		}
	}
	c.checkFuncBody(n.Func, n.Generics, ft)
}

func (c *Checker) checkAssign(n *ast.AssignStmt) {
	valTypes := []types.Type{}
	if len(n.Values) > 0 {
		valTypes = c.checkExprListMulti(n.Values)
	}
	for i, tgt := range n.Targets {
		var want types.Type
		var sym *Symbol
		var readonly bool
		switch t := tgt.(type) {
		case *ast.Ident:
			sym = c.cur().lookupVar(t.Name)
			if sym == nil {
				c.errorf(diag.UndefinedVariable, t.Span(), "undefined variable '%s'", t.Name)
				want = types.Any
			} else {
				if sym.IsConst {
					c.errorf(diag.AssignToConst, t.Span(), "cannot assign to const variable '%s'", t.Name)
				}
				c.recordUse(t.Span(), sym)
				want = sym.Type
			}
		case *ast.FieldExpr:
			ot := c.checkExpr(t.Obj)
			f, ok := lookupField(ot, t.Field.Name)
			if !ok {
				if !isAnyLike(ot) {
					c.errorf(diag.AssignTargetTypeError, t.Span(), "type '%s' has no field '%s'", ot.String(), t.Field.Name)
				}
				want = types.Any
			} else {
				if !c.checkAccess(f.Access, f.Owner, t.Span(), "field '"+t.Field.Name+"'") {
					want = types.Any
				} else {
					c.recordMemberUse(t.Field.Span(), ot, t.Field.Name)
					if root := structRoot(c, t.Obj); root != nil {
						c.recordStructUse(t.Field.Span(), root, t.Field.Name)
					}
					// Structural definition for `p.q = {...}` direct
					// flows (nominally owned bases stay nominal).
					if c.ownerEntry(ot) == nil {
						if i < len(n.Values) {
							if _, ok := n.Values[i].(*ast.TableLit); ok {
								if base := structRoot(c, t.Obj); base != nil {
									c.recordStructDef(base, t.Field.Name, t.Field.Span())
								}
							}
						}
					}
					if f.Readonly {
						readonly = true
					}
					want = f.Type
				}
			}
		case *ast.IndexExpr:
			ot := c.checkExpr(t.Obj)
			kt := c.checkExpr(t.Key)
			want = indexResult(ot, kt)
			if want == nil {
				if !isAnyLike(ot) {
					c.errorf(diag.AssignTargetTypeError, t.Span(), "cannot index type '%s'", ot.String())
				}
				want = types.Any
			} else {
				c.recordStringIndex(t.Key, ot)
				c.recordStructIndex(t.Key, t.Obj)
			}
		default:
			c.errorf(diag.AssignTargetInvalid, tgt.Span(), "invalid assignment target")
			want = types.Any
		}
		if readonly {
			c.errorf(diag.AssignToReadonly, tgt.Span(), "cannot assign to readonly field")
		}
		var got types.Type
		if i < len(valTypes) {
			got = valTypes[i]
		} else if len(n.Values) > 0 {
			got = types.Nil
		} else {
			got = types.Any
		}
		if got != nil && want != nil {
			if !types.IsAssignable(got, want, c.opt.toTypes()) {
				c.errorf(diag.TypeNotAssignable, tgt.Span(), "type '%s' is not assignable to type '%s'", got.String(), want.String())
			}
		}
		if i < len(n.Values) {
			if tl, ok := n.Values[i].(*ast.TableLit); ok {
				c.recordTableKeys(tl, want)
				// Structural definition for direct flows (`p = {...}`;
				// nominally owned vars stay nominal).
				if _, ok := tgt.(*ast.Ident); ok && sym != nil && c.ownerEntry(sym.Type) == nil {
					c.recordStructLiteral(symRoot(sym), tl)
				}
			}
		}
		_ = sym
	}
}

func (c *Checker) checkIf(n *ast.IfStmt) {
	c.checkExpr(n.Cond)
	c.push()
	c.applyNarrowing(n.Cond, true)
	c.checkBlock(n.Then)
	c.pop()
	thenDiv := blockDiverges(n.Then)
	for _, ei := range n.Elseifs {
		c.checkExpr(ei.Cond)
		c.push()
		c.applyNarrowing(ei.Cond, true)
		c.checkBlock(ei.Block)
		c.pop()
	}
	elseDiv := false
	if n.Else != nil {
		c.push()
		c.applyNarrowing(n.Cond, false)
		c.checkBlock(n.Else)
		c.pop()
		elseDiv = blockDiverges(n.Else)
	}
	// Flow narrowing after if: if one branch diverges (return), the other condition holds after.
	// `if x == nil then return end; use(x as non-nil)`
	if thenDiv && n.Else == nil && len(n.Elseifs) == 0 {
		c.applyNarrowing(n.Cond, false)
	} else if n.Else != nil && elseDiv && !thenDiv && len(n.Elseifs) == 0 {
		c.applyNarrowing(n.Cond, true)
	}
}

func blockDiverges(b *ast.Block) bool {
	if b == nil {
		return false
	}
	if b.Ret != nil {
		return true
	}
	if len(b.Stmts) == 0 {
		return false
	}
	// Last statement determines divergence for our purposes.
	switch n := b.Stmts[len(b.Stmts)-1].(type) {
	case *ast.IfStmt:
		// Both branches must diverge.
		if n.Else == nil {
			return false
		}
		if !blockDiverges(n.Then) {
			return false
		}
		if !blockDiverges(n.Else) {
			return false
		}
		for _, ei := range n.Elseifs {
			if !blockDiverges(ei.Block) {
				return false
			}
		}
		return true
	case *ast.DoBlock:
		return blockDiverges(n.Block)
	case *ast.WhileStmt:
		// `while true do ... end` with no break? Conservative false.
		return false
	}
	return false
}

func (c *Checker) checkNumericFor(n *ast.NumericFor) {
	s := c.checkExpr(n.Start)
	e := c.checkExpr(n.End)
	if n.Step != nil {
		c.checkExpr(n.Step)
	}
	// Bounds should be numbers.
	for _, t := range []types.Type{s, e} {
		if !isAnyLike(t) && !types.IsAssignable(t, types.Number, c.opt.toTypes()) {
			c.errorf(diag.NumericForBoundNotNum, n.Span(), "numeric for bounds must be numbers, got '%s'", t.String())
			break
		}
	}
	c.push()
	var vt types.Type = types.Number
	if n.VarTy != nil {
		vt = c.resolveType(n.VarTy)
	}
	c.recordDef(n.Var.Span(), c.bindVar(n.Var.Name, &Symbol{Name: n.Var.Name, Kind: symVar, Type: vt, Span: n.Var.Span()}))
	c.checkBlock(n.Block)
	c.pop()
}

func (c *Checker) checkGenericFor(n *ast.GenericFor) {
	var exprTypes []types.Type
	for _, e := range n.Exprs {
		exprTypes = append(exprTypes, c.checkExpr(e))
	}
	_ = exprTypes
	c.push()
	for i, name := range n.Names {
		var t types.Type = types.Any
		if i < len(n.Types) && n.Types[i] != nil {
			t = c.resolveType(n.Types[i])
		}
		c.recordDef(name.Span(), c.bindVar(name.Name, &Symbol{Name: name.Name, Kind: symVar, Type: t, Span: name.Span()}))
	}
	c.checkBlock(n.Block)
	c.pop()
}

func (c *Checker) checkReturn(r *ast.ReturnStmt) {
	vals := []types.Type{}
	if len(r.Exprs) > 0 {
		vals = c.checkExprListMulti(r.Exprs)
	}
	if len(c.retStack) == 0 {
		// Top-level return (module): just check exprs, no signature.
		return
	}
	want := c.retStack[len(c.retStack)-1]
	if want == nil {
		// Unannotated: collect for potential inference (currently any, so no check).
		if len(c.inferStack) > 0 {
			c.inferStack[len(c.inferStack)-1] = append(c.inferStack[len(c.inferStack)-1], vals...)
		}
		return
	}
	if len(want) == 0 && len(vals) == 0 {
		return
	}
	if len(want) == 0 && len(vals) > 0 {
		// Returning values from void function.
		// Allow `return` bare only.
		c.errorf(diag.VoidFunctionReturns, r.Span(), "function returns no values, but %d value(s) returned", len(vals))
		return
	}
	for i, w := range want {
		var g types.Type = types.Nil
		if i < len(vals) {
			g = vals[i]
		}
		if i < len(r.Exprs) {
			if tl, ok := r.Exprs[i].(*ast.TableLit); ok {
				c.recordTableKeys(tl, w)
			}
		}
		if !types.IsAssignable(g, w, c.opt.toTypes()) {
			c.errorf(diag.ReturnTypeMismatch, r.Span(), "return type '%s' is not assignable to '%s'", g.String(), w.String())
		}
	}
}
