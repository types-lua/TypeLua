package checker

import (
	"github.com/typelua/typelua/internal/ast"
	"github.com/typelua/typelua/internal/diag"
	"github.com/typelua/typelua/internal/types"
)

// 设计者视角：语句检查维护 retStack/labelStack/gotoList 三栈；if 的
// 分支发散信息用于流敏感窄化（return 后条件翻转）。
// 语句详见实现，排查看 Span。
//
// 设计者视角：语句是三栈协同——retStack 管返回契约、labelStack/gotoList 管跳转
// （函数级配对，见 func/class 体），if/while 携窄化（分支影子+发散翻转，见
// checkIf）。障碍是恢复节点（nil Stmt）与未知语句：前者静返，后者报
// …（略，详见实现；排查先看 Span/诊断）
// 分流，Do/If/Loop 各有层与窄，类型声明转 typedecl/class，宏残留报内部错，
// import/defer/spawn/select 各专函。障碍是新语法漏分支：default 报不支持而非
// panic，保全量。取舍：nil 早返（恢复）。排查现场：某语句无检查先查 switch 是否缺分支。
func (c *Checker) checkStmt(st ast.Stmt) {
	if st == nil {
		// 恢复占位：parser 已报永不崩，如坏表回空表。
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
		// 排查看 Span/诊断，如 `local x=1` 错圈整句。
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
		// 排查看 Span/诊断，如 `local x=1` 错圈整句。
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

// checkLocalAssign checks `local a[: T] [= v]` (annotated vs inferred).
//
// 设计者视角：checkLocalAssign 是局部定义的“双轨”——有标注即验 IsAssignable（错即报
// 留 want），无标注即推（纯列表 Tuple→Array+ widenInferred 防过窄，余 widen）。
// 缺省尾值补 Nil，无值无标即 Nil。障碍是同层重定义（Lua 允但多为 bug）：报
// Redefinition（内置豁免），仍 bind 续流；表字面另描名义键（recordTableKeys）与
// 结构桩详见实现，排查看 Span。
//
//	标注?验:推 → 重定义查 → bind+打卡 → 表键/结构记
//
// 不变量：bind 恒执行。排查现场：推断过窄（如字面未展宽）先查 widenInferred。
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
			// 排查看 Span/诊断，如 `local x=1` 错圈整句。
			// 排查看 Span/诊断，如 `local x=1` 错圈整句。
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
				// 排查看 Span/诊断，如 `local x=1` 错圈整句。
				final = widenInferred(got)
			}
		} else {
			final = types.Nil
		}
		isConst := i < len(n.Attrs) && n.Attrs[i] == "const"
		if existing := c.cur().vars[name.Name]; existing != nil && !existing.IsBuiltin {
			// 排查看 Span/诊断，如 `local x=1` 错圈整句。
			// 排查看 Span/诊断，如 `local x=1` 错圈整句。
			c.errorf(diag.RedefinitionOfLocal, name.Span(), "redefinition of local '%s'", name.Name)
		}
		if i < len(n.Values) {
			if tl, ok := n.Values[i].(*ast.TableLit); ok {
				c.recordTableKeys(tl, want)
			}
		}
		bound := c.bindVar(name.Name, &Symbol{Name: name.Name, Kind: symVar, Type: final, Span: name.Span(), IsConst: isConst})
		c.recordDef(name.Span(), bound)
		// 排查看 Span/诊断，如 `local x=1` 错圈整句。
		// 排查看 Span/诊断，如 `local x=1` 错圈整句。
		if i < len(n.Values) {
			if tl, ok := n.Values[i].(*ast.TableLit); ok && c.ownerEntry(final) == nil {
				c.recordStructLiteral(symRoot(bound), tl)
			}
		}
	}
	if len(valTypes) > len(n.Names) && len(n.Names) > 0 {
		// 排查看 Span/诊断，如 `local x=1` 错圈整句。
	}
	// 检查Lua/Just，如 `lua` 例，排查看 Span。
	for i, a := range n.Attrs {
		if a == "close" && len(n.Values) == 0 && i < len(n.Names) {
			c.errorf(diag.CloseVarUninitialized, n.Names[i].Span(), "to-be-closed variable '%s' must be initialized", n.Names[i].Name)
		}
	}
}

// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：widen 是浅展宽——字面取 Base，余原样。深展宽用 widenInferred。
// 留此薄函供旧径复用。排查现场：展宽不彻底先查是否误调此浅版。
func widen(t types.Type) types.Type {
	if lit, ok := t.(*types.Literal); ok {
		return lit.Base
	}
	return t
}

// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：widenInferred 是推断的“防过窄网”——无标注 `local x=1` 应得 number
// 而非字面 1，故深展（字面→基，union/数组/元组/映射/对象/类参递归，ClassValue
// 直留）。障碍是对象字段内字面亦须展，否则 `local t={x="a"}` 的 x 被锁死。
// 候选：浅展 vs 深展；选深展。取舍：Class 浅拷 Args（Def/链不动）。排查现场：
// 推断仍字面先查是否绕过此处（如直接用 got）。

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

// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：isPureList 是 Tuple→Array 的“扳道”——纯列表才可作可增长数组推断
// （见 checkLocalAssign），含命名即对象。空表亦纯（调方按空对象/数组分流）。
// 排查现场：列表推成对象先查字段 Kind 是否混入命名。
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
// 排查看 Span/诊断，如 `local x=1` 错圈整句。
// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：checkImport 是 import 的“等价桥”——无标注即取模块型（any 兜底），
// 有标注即验模块型可赋标注（any 跳验）。重定义规同 local（内置豁免）。障碍是
// 路径未解：不报（经典模式），只取 any。取舍：与 require 同货架（requirePathType）。
// 排查现场：import 取 any 先查驱动 RequireTypes 键。
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

// 排查看 Span/诊断，如 `local x=1` 错圈整句。
// included). Bare `...` cannot cross into the deferred closure (it would
// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：checkDefer 是延迟调用的“直验”——按常验参，另查裸 `...`如 `...`，排查看 Span。
// 并指路存局部。障碍是 defer 体无变参可绑：`...` 会绑到不存在的闭包变参。取舍：
// 验后查（错误并存）。排查现场：defer 内 `...` 未报先查 hasBareVarArg 是否透嵌套函数
// （FuncLit 内即 false，属对）。
func (c *Checker) checkDefer(n *ast.DeferStmt) {
	c.checkExpr(n.Call)
	if hasBareVarArg(n.Call) {
		c.errorf(diag.BareVarargInTaskCall, n.Call.Span(), "cannot use '...' in a deferred call (copy it into a local first)")
	}
}

// hasBareVarArg reports a `...` outside nested function bodies.
//
// 设计者视角：hasBareVarArg 是变参逃逸的“嗅探”——遇 FuncLit 即止（内层 `...` 属内层），
// 余者递归（调用/方法/二目/表/cast/await 等）。defer/spawn 共用（跨闭包即错）。
// 障碍是 Paren/Await 包裹：须透视。取舍：纯读 AST。排查现场：`...` 漏报先查是否藏
// 在未覆盖的表达式形（如新语法分支缺失）。

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

// checkLocalFunc checks `local function f()` (signature first, then body).
//
// 设计者视角：checkLocalFunc 是局部函数的“先签名后体”——签名先行供递归自调，
// 重定义规同 local，bind 后打卡再验体。障碍是签名与体泛型环境各需压弹（func
// 内已管），此处不另压。排查现场：递归调用找不到先查是否体前未 bind。
func (c *Checker) checkLocalFunc(n *ast.LocalFuncDef) {
	ft := c.funcTypeFromLit(n.Func, n.Generics)
	if existing := c.cur().vars[n.Name.Name]; existing != nil && !existing.IsBuiltin {
		c.errorf(diag.RedefinitionOfLocal, n.Name.Span(), "redefinition of local '%s'", n.Name.Name)
	}
	sym := c.bindVar(n.Name.Name, &Symbol{Name: n.Name.Name, Kind: symFunc, Type: ft, Span: n.Span(), GenericNames: genericNames(n.Generics), FuncDecl: n.Func})
	c.recordDef(n.Name.Span(), sym)
	c.checkFuncBody(n.Func, n.Generics, ft)
}

// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：genericNames 是函数符号的“泛型名片”——只存名（约束/默认存 Func
// TypeParams），供悬停展示。薄抽取。排查现场：悬停泛型缺失先查此处与 TypeParams 是否同步。
func genericNames(gs []*ast.GenericParam) []string {
	var out []string
	for _, g := range gs {
		out = append(out, g.Name.Name)
	}
	return out
}

// checkGlobalFunc checks `function a.b:c()` (global or method definition).
//
// 设计者视角：checkGlobalFunc 容 Lua 的“补丁式定义”——单名全局：已存即更新类型
// （函数重定义合法），无即 bind；多段/方法：验基存在，对象基即记字段（单层跟踪，
// 深链从简）。体恒验。障碍是 `a.b.c` 深链：只记末字段于首基对象（近似）。取舍：
// 全局仍落当前层（模块多局部+返回，全局罕见）。排查现场：方法调用找不到先查基
// 对象字段是否在此记。
func (c *Checker) checkGlobalFunc(n *ast.FuncDefStmt) {
	ft := c.funcTypeFromLit(n.Func, n.Generics)
	if n.Name == nil {
		return
	}
	if len(n.Name.Prefix) == 1 && n.Name.Method == nil {
		name := n.Name.Prefix[0].Name
		if g := c.cur().lookupVar(name); g != nil {
			// 排查看 Span/诊断，如 `local x=1` 错圈整句。
			g.Type = ft
			c.recordDef(n.Name.Prefix[0].Span(), g)
		} else {
			// 定义，排查先看 Span/诊断。
			sym := c.bindVar(name, &Symbol{Name: name, Kind: symFunc, Type: ft, Span: n.Span(), GenericNames: genericNames(n.Generics), FuncDecl: n.Func})
			c.recordDef(n.Name.Prefix[0].Span(), sym)
		}
	} else {
		// 排查看 Span/诊断，如 `local x=1` 错圈整句。
		base := n.Name.Prefix[0].Name
		sym := c.cur().lookupVar(base)
		if sym == nil {
			c.errorf(diag.UndefinedTableInFuncDef, n.Name.Span(), "undefined table '%s' in function definition", base)
		} else {
			// 排查看 Span/诊断，如 `local x=1` 错圈整句。
			if obj, ok := sym.Type.(*types.Object); ok {
				fieldName := n.Name.Prefix[len(n.Name.Prefix)-1].Name
				if n.Name.Method != nil {
					fieldName = n.Name.Method.Name
				} else if len(n.Name.Prefix) > 1 {
					// 排查看 Span/诊断，如 `local x=1` 错圈整句。
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

// checkAssign checks `a, b.c, d[k] = vals` (per-target type and access).
//
// 设计者视角：checkAssign 是赋值的“逐靶验”——值先铺多（末位展开），靶分三式：
// Ident（查值册+const 门+打卡）、Field（lookup+门禁+名义/结构双记，表值直流另钉桩）、
// Index（indexResult+串键双记）。readonly 另拦。逐靶验 IsAssignable（错即报）。
// 表值对名义 want 另描键详见实现，排查看 Span。
// 缺值补 Nil、无值取 Any（`f()=...` 形）。取舍：错靶仍验余靶。图：
//
//	铺值 → 逐靶(查型+门禁+打卡) → 验赋 → 表键/结构记
//
// 排查现场：成员赋值未跳转先查双记是否命中，readonly 漏报先查标记透传。
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
					// 排查看 Span/诊断，如 `local x=1` 错圈整句。
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
				// 排查看 Span/诊断，如 `local x=1` 错圈整句。
				if _, ok := tgt.(*ast.Ident); ok && sym != nil && c.ownerEntry(sym.Type) == nil {
					c.recordStructLiteral(symRoot(sym), tl)
				}
			}
		}
		_ = sym
	}
}

// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：checkIf 是流敏感的“眼镜店”——条件先验，Then 开层+真窄，elseif 各验
// 各窄，Else 开层+假窄（只反首条件，elseif 链不反——近似）。发散翻转：Then 发散
// 无 Else 即会后施假窄（卫语句 `if x==nil then return end` 后 x 非 nil），Else 发散
// 即会后施真窄。障碍是发散判定（blockDiverges 只看末句，if 须双支发散）。取舍：
// elseif 后不翻（复杂度换精度）。图：
//
//	验cond → 真窄Then → [elseif]* → 假窄Else → 发散?会后窄
//
// 排查现场：卫语句后未窄先查 blockDiverges 是否识发散（如 return 包在 do 里已识）。
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
	// 排查看 Span/诊断，如 `local x=1` 错圈整句。
	// `if x == nil then return end; use(x as non-nil)`
	if thenDiv && n.Else == nil && len(n.Elseifs) == 0 {
		c.applyNarrowing(n.Cond, false)
	} else if n.Else != nil && elseDiv && !thenDiv && len(n.Elseifs) == 0 {
		c.applyNarrowing(n.Cond, true)
	}
}

// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：blockDiverges 是发散的“探针”——有 Ret 即散，末句 if 须 Then/Else/
// elseif 全散，Do 透视，while 保守假，余者假。障碍是 `do return end` 包裹与
// `if` 无 Else：前者透视，後者即不散。取舍：保守（宁假勿真，假只丢一次窄化机会）。
// 排查现场：卫语句翻转失效先查末句是否为 if/do 包裹。
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
	// 排查看 Span/诊断，如 `local x=1` 错圈整句。
	switch n := b.Stmts[len(b.Stmts)-1].(type) {
	case *ast.IfStmt:
		// 排查看 Span/诊断，如 `local x=1` 错圈整句。
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

// checkNumericFor checks `for i = s, e[, step]` (numeric bounds, scoped var).
//
// 设计者视角：checkNumericFor 是数值循环的“量程表”——起止验后须可赋 Number（any
// 放行，错报一次即停），步进照验，循环变量独层（默认 Number，标注另解）再验体。
// 障碍是步进 nil（`for i=1,10 do`）：Step 为 nil 即跳。取舍：变量不外泄。排查现场：
// 界误报先查 IsAssignable 方向（界→Number）。
func (c *Checker) checkNumericFor(n *ast.NumericFor) {
	s := c.checkExpr(n.Start)
	e := c.checkExpr(n.End)
	if n.Step != nil {
		c.checkExpr(n.Step)
	}
	// 排查看 Span/诊断，如 `local x=1` 错圈整句。
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

// checkGenericFor checks `for k[, v] in exprs` (permissive element types).
//
// 设计者视角：checkGenericFor 取宽容——迭代源照验（chan iter 含 async 亦可），
// 循环变量无标注恒 Any（有标注即解），独层验体。障碍是迭代协议多态（函数/表/通道）：
// 不精判元素型，宁 Any 不误报。取舍：exprTypes 验而不用（占位后续精化）。排查现场：
// 循环变量取到 Any 属预期（设计如此），非 bug。
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

// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：checkReturn 是返回的“对账”——值先铺多，顶层（retStack 空，模块出口）
// 只验不判；未标注（nil 哨兵）收 infer 备推断；void（空非 nil）多值即报；余者逐位
// IsAssignable（缺位补 Nil），表值另描键。障碍是 nil 哨兵与 void 空的 Go 同形：
// 靠是否 nil 辨（见 func.go）。取舍：逐位报首错即停？此处逐位各自可报（与调用单报错
// 不同）。图：
//
//	铺vals → 顶层?返 → nil?收 → void?判空 → 逐位验
//
// 排查现场：返回误报先查 retStack 顶是 nil 还是空。
func (c *Checker) checkReturn(r *ast.ReturnStmt) {
	vals := []types.Type{}
	if len(r.Exprs) > 0 {
		vals = c.checkExprListMulti(r.Exprs)
	}
	if len(c.retStack) == 0 {
		// 排查看 Span/诊断，如 `local x=1` 错圈整句。
		return
	}
	want := c.retStack[len(c.retStack)-1]
	if want == nil {
		// 排查看 Span/诊断，如 `local x=1` 错圈整句。
		if len(c.inferStack) > 0 {
			c.inferStack[len(c.inferStack)-1] = append(c.inferStack[len(c.inferStack)-1], vals...)
		}
		return
	}
	if len(want) == 0 && len(vals) == 0 {
		return
	}
	if len(want) == 0 && len(vals) > 0 {
		// 排查看 Span/诊断，如 `local x=1` 错圈整句。
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
