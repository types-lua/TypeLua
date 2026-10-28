package checker

import (
	"strconv"
	"strings"

	"github.com/typelua/typelua/internal/ast"
	"github.com/typelua/typelua/internal/diag"
	"github.com/typelua/typelua/internal/token"
	"github.com/typelua/typelua/internal/types"
)

// 设计者视角：表达式检查返回类型切片以承载多返回；table 字面量按"纯列表
// -> Tuple，含命名 -> Object"分流；索引/字段访问统一走 lookupField。

// Expression checking, table literals, and operators.
//
// 设计者视角：表达式是“单值与多值双轨”——checkExpr/checkExprSingle 走单值，
// checkExprMulti 承载多返回（调用/await 末位展开）。表字面按“纯列表→Tuple，
// 含命名→Object”分流（见 checkTable），字段/索引统一走 lookupField/indexResult。
// 障碍是 Lua 多值只在末位展开、其余取首值：checkExprListMulti/checkCallArgs 双处
// 复刻此语义。取舍：遇错填 Any 续流，级联不炸。不变量：checkExpr 永不返 nil 切片
// 空即 Nil。排查现场：多值丢失先查是否非末位误展开，表类型错先查纯列表判据。

// ---------- expressions ----------

// checkExprListMulti checks expression lists with last-position multi expansion.
//
// 设计者视角：checkExprListMulti 是赋值/返回/传参的“铺轨器”——前者取首值，末者铺
// 多值，复刻 Lua 语义。障碍是空列表：返 nil 而非空切片，下游以 len 判缺省补 nil。
// 取舍：薄循环委托单/多两版。排查现场：尾值多返回丢失先查是否误调 Single 版。
func (c *Checker) checkExprListMulti(exprs []ast.Expr) []types.Type {
	if len(exprs) == 0 {
		return nil
	}
	var out []types.Type
	for i, e := range exprs {
		if i == len(exprs)-1 {
			out = append(out, c.checkExprMulti(e)...)
		} else {
			out = append(out, c.checkExprSingle(e))
		}
	}
	return out
}

// checkExprSingle returns the first value of an expression.
//
// 设计者视角：checkExprSingle 是多值到单值的“取首器”——非末位、条件、算术等单值
// 语境统一经此。障碍是零值（无返回函数）：空切片须退 Nil 而非 panic。取舍：
// 调 Multi 后取 [0]，逻辑单源。不变量：永不返 nil。排查现场：取到 Nil 意外先查
// 被调是否 void。
func (c *Checker) checkExprSingle(e ast.Expr) types.Type {
	ms := c.checkExprMulti(e)
	if len(ms) == 0 {
		return types.Nil
	}
	return ms[0]
}

// checkExpr is the single-value entry (delegates to checkExprSingle).
//
// 设计者视角：checkExpr 是最常用的“便民门”——多数语境只要首值。薄委托保调用点
// 简洁，门禁与展开藏于 Multi/One。排查现场：需多值却调此处（如传参）会丢值，
// 应换 ListMulti/ExprMulti。
func (c *Checker) checkExpr(e ast.Expr) types.Type {
	return c.checkExprSingle(e)
}

// checkExprMulti expands multi-return calls on the last position.
//
// 设计者视角：checkExprMulti 是多值分流的“总盘”——Call/MethodCall/VarArg/Await
// 各有展开，其余包单值。障碍有四：require 点名（驱动模块型）、error 标记（发射器
// errdefer）、Point.new/super/makeChan 特判——顺序即优先级，挪一位即误判（如
// super 被当普通调用）。候选：各特判散落 vs 集中；选集中，调用语义一处可览。
// 图：
//
//	require? → error标记? → new实例? → super? → makeChan? → 通用 call
//
// 不变量：失败分支亦验参（错误不吞）。排查现场：特判未命中先查顺序与遮蔽
// （如 require 被局部遮蔽即走通用）。

func (c *Checker) checkExprMulti(e ast.Expr) []types.Type {
	switch n := e.(type) {
	case *ast.CallExpr:
		// `require("dotted.path")` resolves through the driver's module
		// types (any when unresolved or shadowed by a local).
		if id, ok := n.Func.(*ast.Ident); ok && id.Name == "require" && len(n.Args) == 1 {
			if lit, ok := n.Args[0].(*ast.StringLit); ok && !isShadowedRequire(c) {
				c.checkExpr(n.Func) // record the builtin use
				return []types.Type{c.requirePathType(lit)}
			}
		}
		// Mark unshadowed `error(...)` calls so the emitter can run
		// errdefers around them (normal checking continues).
		if id, ok := n.Func.(*ast.Ident); ok && id.Name == "error" {
			if s := c.cur().lookupVar("error"); s == nil || s.IsBuiltin {
				n.ErrorCall = true
			}
		}
		// `Point.new(...)` gets instantiation checks (abstract/private).
		if fe, ok := n.Func.(*ast.FieldExpr); ok && fe.Field.Name == "new" {
			if id, ok := fe.Obj.(*ast.Ident); ok {
				if s := c.cur().lookupVar(id.Name); s != nil {
					if cv, ok := s.Type.(*types.ClassValue); ok {
						c.recordUse(id.Span(), s)
						return c.checkClassInstantiate(cv, n.Args, n.Span(), c.resolveTypeArgs(n.TypeArgs))
					}
				}
			}
		}
		if id, ok := n.Func.(*ast.Ident); ok && id.Name == "super" && c.cur().lookupVar("super") == nil {
			return c.checkSuperCall(n)
		}
		// `makeChan<T>(...)` requires an explicit T (cap carries no T).
		if id, ok := n.Func.(*ast.Ident); ok && id.Name == "makeChan" && len(n.TypeArgs) == 0 {
			if s := c.cur().lookupVar("makeChan"); s != nil && s.IsBuiltin {
				c.recordUse(id.Span(), s)
				for _, a := range n.Args {
					c.checkExpr(a)
				}
				if len(n.Args) > 1 {
					c.errorf(diag.MakeChanInvalid, n.Span(), "'makeChan' expects at most 1 argument (capacity), got %d", len(n.Args))
				} else if len(n.Args) == 1 {
					if at := c.checkExprSingle(n.Args[0]); !isAnyLike(at) && !isIntegerLike(at) && !isNumberLike(at) {
						c.errorf(diag.MakeChanInvalid, n.Args[0].Span(), "'makeChan' capacity must be a number, got '%s'", at.String())
					}
				}
				c.errorf(diag.MakeChanInvalid, n.Span(), "'makeChan' requires an explicit type argument ('makeChan<T>(cap?)')")
				return []types.Type{&types.Chan{Elem: types.Any}}
			}
		}
		fnT := c.checkExpr(n.Func)
		c.checkAsyncCall(fnT, n.Span(), calleeName(n.Func))
		return c.checkCall(n.Func.Span(), fnT, n.Args, n.Span(), c.resolveTypeArgs(n.TypeArgs))
	case *ast.MethodCallExpr:
		if id, ok := n.Obj.(*ast.Ident); ok && id.Name == "super" && c.cur().lookupVar("super") == nil {
			return c.checkSuperMethod(n)
		}
		// `Point:new(...)` routes to instantiation checks.
		if id, ok := n.Obj.(*ast.Ident); ok && n.Method.Name == "new" {
			if s := c.cur().lookupVar(id.Name); s != nil {
				if cv, ok := s.Type.(*types.ClassValue); ok {
					c.recordUse(id.Span(), s)
					return c.checkClassInstantiate(cv, n.Args, n.Span(), c.resolveTypeArgs(n.TypeArgs))
				}
			}
		}
		ot := c.checkExpr(n.Obj)
		mf := c.lookupMethodField(ot, n.Method.Name, n.Span())
		if !c.checkAccess(mf.Access, mf.Owner, n.Span(), "method '"+n.Method.Name+"'") {
			return []types.Type{types.Any}
		}
		if _, ok := lookupField(ot, n.Method.Name); ok {
			c.recordMemberUse(n.Method.Span(), ot, n.Method.Name)
		}
		c.checkAsyncCall(mf.Type, n.Span(), n.Method.Name)
		return c.checkCall(n.Span(), mf.Type, n.Args, n.Span(), c.resolveTypeArgs(n.TypeArgs))
	case *ast.VarArg:
		vt := c.currentVarArg()
		if vt == nil {
			c.errorf(diag.InvalidExpression, n.Span(), "'...' used outside vararg function")
			return []types.Type{types.Any}
		}
		// `...` expands to multiple values of vt. Represent as single for non-last? For multi, return [vt] (one-or-more modeled as one).
		// To keep sound, return single vt; callers in multi context treat as one. (Full variadic multi is unbounded; model as one.)
		return []types.Type{vt}
	case *ast.AwaitExpr:
		return c.checkAwaitMulti(n)
	default:
		return []types.Type{c.checkExprOne(e)}
	}
}

// currentVarArg returns the enclosing vararg element type, if any.
//
// 设计者视角：currentVarArg 是 `...` 的“查岗”——栈顶即当前函数变参元型，空栈或
// nil 标记即无参函数。障碍是嵌套函数：内层非变参必须遮外层（栈顶为准）。取舍：
// 只读不报，报错留给调用方。排查现场：`...` 误放行先查 varargStack 压弹（函数
// 是否按 HasVarArg 入栈）。
func (c *Checker) currentVarArg() types.Type {
	if len(c.varargStack) == 0 {
		return nil
	}
	return c.varargStack[len(c.varargStack)-1]
}

// checkExprOne checks single-value expression forms (ident/literal/table/index/ops/casts).
//
// 设计者视角：checkExprOne 是单值形的“分诊台”——Ident 查值册（self/super 特判），
// 字面量直构 Literal，表走 checkTable，索引/字段走 indexResult/lookupField（含
// 可见性门禁与 LSP 打卡），双/单目走 checkBinOp/checkUnOp，cast 双向可赋即放，
// `!` 剥 nil。障碍是恢复节点（nil Expr）：parser 已报，此处返 Any 不崩。取舍：
// Call/Method 在此不可达（Multi 已分流），留断言兜底。图：
//
//	Ident→字面→表/函数→索引/字段→运算→cast/!/await单值
//
// 不变量：永不返 nil（兜底 Any）。排查现场：字段误报先查 lookupField 与门禁顺序。
func (c *Checker) checkExprOne(e ast.Expr) types.Type {
	if e == nil {
		// Recovery artifact (parser already reported); never crash.
		return types.Any
	}
	switch n := e.(type) {
	case *ast.Ident:
		if s := c.cur().lookupVar(n.Name); s != nil {
			c.recordUse(n.Span(), s)
			return s.Type
		}
		if n.Name == "self" {
			c.errorf(diag.UndefinedVariableExpr, n.Span(), "'self' is only available inside methods")
			return types.Any
		}
		if n.Name == "super" {
			c.errorf(diag.SuperOutsideDerivedMethod, n.Span(), "'super' can only be used inside methods of a derived class")
			return types.Any
		}
		c.errorf(diag.UndefinedVariableExpr, n.Span(), "undefined variable '%s'", n.Name)
		return types.Any
	case *ast.NumberLit:
		base := types.Type(types.Integer)
		if strings.ContainsAny(n.Literal, ".eEpP") {
			// Hex integers with p exponent are floats? Keep float.
			base = types.Float
		}
		return &types.Literal{Value: n.Literal, Base: base}
	case *ast.StringLit:
		return &types.Literal{Value: n.Literal, Base: types.String}
	case *ast.BoolLit:
		return &types.Literal{Value: strconv.FormatBool(n.Value), Base: types.Boolean}
	case *ast.NilLit:
		return types.Nil
	case *ast.VarArg:
		vt := c.currentVarArg()
		if vt == nil {
			c.errorf(diag.InvalidExpression, n.Span(), "'...' used outside vararg function")
			return types.Any
		}
		return vt
	case *ast.ParenExpr:
		return c.checkExpr(n.E)
	case *ast.TableLit:
		return c.checkTable(n)
	case *ast.FuncLit:
		ft := c.funcTypeFromLit(n, n.Generics)
		c.checkFuncBody(n, n.Generics, ft)
		return ft
	case *ast.IndexExpr:
		ot := c.checkExpr(n.Obj)
		kt := c.checkExpr(n.Key)
		if r := indexResult(ot, kt); r != nil {
			c.recordStringIndex(n.Key, ot)
			c.recordStructIndex(n.Key, n.Obj)
			return r
		}
		if isAnyLike(ot) {
			return types.Any
		}
		c.errorf(diag.CannotIndex, n.Span(), "cannot index type '%s'", ot.String())
		return types.Any
	case *ast.FieldExpr:
		ot := c.checkExpr(n.Obj)
		if f, ok := lookupField(ot, n.Field.Name); ok {
			if !c.checkAccess(f.Access, f.Owner, n.Span(), "field '"+n.Field.Name+"'") {
				return types.Any
			}
			c.recordMemberUse(n.Field.Span(), ot, n.Field.Name)
			if root := structRoot(c, n.Obj); root != nil {
				c.recordStructUse(n.Field.Span(), root, n.Field.Name)
			}
			if f.Optional {
				return types.NewUnion([]types.Type{f.Type, types.Nil})
			}
			return f.Type
		}
		if isAnyLike(ot) {
			return types.Any
		}
		// Enum member access: Color.Red
		if en, ok := ot.(*types.Enum); ok {
			_ = en
		}
		// If obj is an enum value object (runtime table), fields were defined.
		c.errorf(diag.NoField, n.Span(), "type '%s' has no field '%s'", ot.String(), n.Field.Name)
		return types.Any
	case *ast.CallExpr:
		return c.checkExprSingle(n) // unreachable (handled in multi) but keep.
	case *ast.MethodCallExpr:
		return c.checkExprSingle(n)
	case *ast.BinOpExpr:
		return c.checkBinOp(n)
	case *ast.UnOpExpr:
		return c.checkUnOp(n)
	case *ast.CastExpr:
		srcT := c.checkExpr(n.E)
		dstT := c.resolveType(n.T)
		if !types.IsAssignable(srcT, dstT, c.opt.toTypes()) && !types.IsAssignable(dstT, srcT, c.opt.toTypes()) {
			c.errorf(diag.SuspiciousConversion, n.Span(), "conversion from '%s' to '%s' may be a mistake", srcT.String(), dstT.String())
		}
		return dstT
	case *ast.NonNullExpr:
		t := c.checkExpr(n.E)
		stripped, hadNil := types.RemoveNil(t)
		if !hadNil && !isAnyLike(t) {
			// Warn? `!` on non-nullable is unnecessary but not an error. Keep silent to avoid noise? Emit warning as error for now? Skip.
		}
		if hadNil {
			// stripped could be Never if it was just nil: `nil!` => never (likely bug).
			if stripped.Kind() == types.KNever {
				c.errorf(diag.NonNullOnNil, n.Span(), "non-null assertion on 'nil'")
				return types.Never
			}
			return stripped
		}
		return t
	case *ast.AwaitExpr:
		return c.checkAwait(n)
	}
	c.errorf(diag.InvalidExpression, e.Span(), "unsupported expression %T", e)
	return types.Any
}

// checkTable checks a table literal (pure list as Tuple, else Object).
//
// 设计者视角：checkTable 是表字面的“分拣线”——ExprField 归列，Named/Indexed 归
// 字段/索引器；无命名即纯列表（空表为空 Object，否则 Tuple 保序，见调用方转 Array），
// 有命名即 Object（列部并为 integer 索引器）。障碍是 `["k"]=v` 与 `[1]=v` 混写：
// 前者经 literalKey 升为命名字段，后者走索引器。候选：纯列表直给 Array vs Tuple；
// 选 Tuple（保位置供 [string,number] 拟合，推断时再 widen 为 Array）。不变量：
// 字段名冲突后者覆盖（ collected 先后即源序）。排查现场：元组拟合失败先查此处
// 是否误判含命名。
func (c *Checker) checkTable(n *ast.TableLit) types.Type {
	// Collect.
	var listElems []types.Type
	fields := map[string]types.Field{}
	var indexers []types.Indexer
	hasNamed := false
	for _, f := range n.Fields {
		switch f.Kind {
		case ast.ExprField:
			listElems = append(listElems, c.checkExpr(f.Val))
		case ast.NamedField:
			hasNamed = true
			fields[f.Key.Name] = types.Field{Type: c.checkExpr(f.Val)}
		case ast.IndexedField:
			// If key is string/number literal, treat as named.
			if lit := literalKey(f.KeyX); lit != nil {
				fields[*lit] = types.Field{Type: c.checkExpr(f.Val)}
				hasNamed = true
			} else {
				kt := c.checkExpr(f.KeyX)
				vt := c.checkExpr(f.Val)
				indexers = append(indexers, types.Indexer{Key: kt, Value: vt})
				hasNamed = true
			}
		}
	}
	if !hasNamed {
		if len(listElems) == 0 {
			return &types.Object{Fields: map[string]types.Field{}}
		}
		// Pure list: keep positional info as Tuple so `{"a", 1}` fits `[string, number]`.
		// Unannotated inference converts to Array (see checkLocalAssign).
		return &types.Tuple{Elems: listElems}
	}
	// Mixed or pure named: object with optional array indexer for list part.
	obj := &types.Object{Fields: fields, Indexers: indexers}
	if len(listElems) > 0 {
		elem := types.NewUnion(listElems)
		obj.Indexers = append(obj.Indexers, types.Indexer{Key: types.Integer, Value: elem})
	}
	return obj
}

// literalKey extracts a static key from a table key expression, if any.
//
// 设计者视角：literalKey 是表键的“静态嗅探”——字符串经 unquote/parse 取内容，
// 数字取原文；其余（变量/调用）返 nil 走索引器。障碍是长括号串与转义：先
// unquoteKey 保源忠实，再 parse 兜底。取舍：best-effort，嗅不到即动态。排查现场：
// `["a"]=1` 未进字段先查引号剥离是否失败。
func literalKey(e ast.Expr) *string {
	switch n := e.(type) {
	case *ast.StringLit:
		if s, _, _, ok := unquoteKey(n.Literal); ok {
			return &s
		}
		s, err := parseStringLiteral(n.Literal)
		if err == nil {
			return &s
		}
		// Fallback: raw.
		raw := strings.Trim(n.Literal, `"'`)
		return &raw
	case *ast.NumberLit:
		s := n.Literal
		return &s
	}
	return nil
}

// unquoteKey splits a string literal into content plus delimiter widths.
// Handles "...", '...', and long brackets [[...]], [=[...]=] (any level).
// Escapes stay raw (source-faithful, matching literalKey).
//
// 设计者视角：unquoteKey 是引号的“卡尺”——除内容外还量出来龙去脉的 open/close
// 宽，供 stringKeyName 切内 Span（重命名不碰引号）。障碍是长括号多等号层级：
// 逐字数 `=` 再拼关符，后缀校验防半截。转义保持 raw（源忠实）。不变量：ok 即
// 内容切片有效。排查现场：成员重命名带上引号，先查 open/close 是否算错。

func unquoteKey(lit string) (string, int, int, bool) {
	if len(lit) >= 2 {
		q := lit[0]
		if (q == '"' || q == '\'') && lit[len(lit)-1] == q {
			return lit[1 : len(lit)-1], 1, 1, true
		}
		if q == '[' {
			i := 1
			for i < len(lit) && lit[i] == '=' {
				i++
			}
			if i < len(lit) && lit[i] == '[' {
				open := i + 1
				close := "]" + strings.Repeat("=", i-1) + "]"
				if strings.HasSuffix(lit, close) && len(lit) >= open+len(close) {
					return lit[open : len(lit)-len(close)], open, len(close), true
				}
			}
		}
	}
	return "", 0, 0, false
}

// stringKeyName splits a quoted `"name"` literal into its member name and
// inner span (quotes excluded, so rename edits preserve them).
// Long strings and non-literals report false.
//
// 设计者视角：stringKeyName 是 `obj["field"]` 的“LSP 卡口”——把字面键切出成员名
// 与去引号内 Span，索引器式访问亦能记成员。障碍是长串与空内容：前者 Span 难保
// 引号语义，后者切无可切，一律 false 跳过。取舍：只认可切引号串。不变量：
// 内 Span 恒在外 Span 内。排查现场：字符串索引跳转缺失先查此处是否 false。

func stringKeyName(sl *ast.StringLit) (string, token.Span, bool) {
	if sl == nil {
		return "", token.Span{}, false
	}
	name, open, close, ok := unquoteKey(sl.Literal)
	if !ok {
		return "", token.Span{}, false
	}
	inner := sl.Span()
	inner.Start.Offset += open
	inner.Start.Column += open
	inner.End.Offset -= close
	inner.End.Column -= close
	if inner.End.Offset <= inner.Start.Offset {
		return "", token.Span{}, false
	}
	return name, inner, true
}

// parseStringLiteral best-effort unquotes a string literal (raw fallback).
//
// 设计者视角：parseStringLiteral 是“宽容脱壳”——双/单引去壳，长串尽量去括号，
// 失败即返原文。障碍是 Lua 转义与 Go 引号语义不同：不求全等，只求键比对可用
// （精比对另有 normStrLit）。取舍：永不报错（err 恒 nil 通道）。排查现场：键名
// 含转义对不上，先查此处与 norm 的双轨是否一致。
func parseStringLiteral(lit string) (string, error) {
	if len(lit) >= 2 && (lit[0] == '"' || lit[0] == '\'') {
		// Use strconv-ish unquoting for simple cases; Lua escapes differ, so best-effort.
		inner := lit[1 : len(lit)-1]
		return inner, nil
	}
	if strings.HasPrefix(lit, "[") {
		// Long string: strip brackets.
		start := strings.Index(lit, "[")
		_ = start
		return lit, nil
	}
	return lit, nil
}

// recordTableKeys records named keys of a table literal checked against a
// nominal object type (interface implementations), recursing into nested
// literals with the member's declared type.
//
// 设计者视角：recordTableKeys 是内联接口实现的“描图”——表字面按形参/返回的名义
// Object 类型逐键打卡（Named 与 ["k"] 双式），嵌套表携成员声明型递归。障碍是
// 匿名 Object 无 Entry：必须经 ownerEntry 回查到声明才记，否则结构表互相串。
// 候选：全记 vs 声明才记；选后者，防误合并。不变量：Index 为 nil 即返。排查现场：
// 内联实现跳转缺失先查 want 是否为具名 Object。

func (c *Checker) recordTableKeys(lit *ast.TableLit, want types.Type) {
	if c.Index == nil || lit == nil {
		return
	}
	obj, ok := want.(*types.Object)
	if !ok || obj.Name == "" {
		return
	}
	e := c.cur().lookupType(obj.Name)
	if e == nil {
		return
	}
	for _, f := range lit.Fields {
		var name string
		var sp token.Span
		var val ast.Expr
		switch f.Kind {
		case ast.NamedField:
			if f.Key == nil {
				continue
			}
			name, sp, val = f.Key.Name, f.Key.Span(), f.Val
		case ast.IndexedField:
			sl, ok := f.KeyX.(*ast.StringLit)
			if !ok {
				continue
			}
			nm, inner, ok := stringKeyName(sl)
			if !ok {
				continue
			}
			name, sp, val = nm, inner, f.Val
		default:
			continue
		}
		mf, ok := lookupNamedField(obj, name)
		if !ok {
			continue
		}
		c.Index.Uses = append(c.Index.Uses, SymUse{Span: sp,
			Member: &MemberRef{Owner: e, OwnerName: e.name, Name: name}})
		if sub, ok := val.(*ast.TableLit); ok {
			c.recordTableKeys(sub, mf.Type)
		}
	}
}

// checkBinOp checks binary operators with Lua numeric/string semantics.
//
// 设计者视角：checkBinOp 是二目运算的“验算尺”——算术要数字（整数保整数，除/^
// 恒 Number）、位运算要整数、连接要可连、等值恒 Boolean、大小比要可比、and/or
// 并为 union。障碍是 anyLike 的传染：任一端 any 即放行，防动态代码刷屏。候选：
// 精确值流 vs 类型级；选类型级（字面 Base 参与）。不变量：失败亦返合理结果型
// 续流。排查现场：位运算误报先查 isIntegerLike（含 enum/union 透视）。
func (c *Checker) checkBinOp(n *ast.BinOpExpr) types.Type {
	l := c.checkExpr(n.L)
	r := c.checkExpr(n.R)
	anyLike := isAnyLike(l) || isAnyLike(r)
	switch n.Op {
	case token.Plus, token.Minus, token.Star, token.Slash, token.DoubleSlash, token.Percent, token.Caret:
		if !anyLike {
			if !isNumberLike(l) || !isNumberLike(r) {
				c.errorf(diag.ArithmeticOnNonNumber, n.Span(), "arithmetic on non-numbers ('%s', '%s')", l.String(), r.String())
			}
		}
		// Integer preservation: if both integer and op is not / ^, result integer.
		if isIntegerLike(l) && isIntegerLike(r) && n.Op != token.Slash && n.Op != token.Caret {
			return types.Integer
		}
		return types.Number
	case token.Amp, token.Pipe, token.Tilde, token.ShiftL, token.ShiftR:
		if !anyLike && (!isIntegerLike(l) || !isIntegerLike(r)) {
			c.errorf(diag.BitwiseRequiresInteger, n.Span(), "bitwise operation requires integers ('%s', '%s')", l.String(), r.String())
		}
		return types.Integer
	case token.DotDot:
		if !anyLike && (!isConcatable(l) || !isConcatable(r)) {
			c.errorf(diag.ConcatRequiresString, n.Span(), "concatenation requires strings/numbers ('%s', '%s')", l.String(), r.String())
		}
		return types.String
	case token.Eq, token.Ne:
		return types.Boolean
	case token.Lt, token.Le, token.Gt, token.Ge:
		if !anyLike && !isComparable(l, r) {
			c.errorf(diag.IncomparableTypes, n.Span(), "comparison of incompatible types ('%s', '%s')", l.String(), r.String())
		}
		return types.Boolean
	case token.And, token.Or:
		// `and`/`or`: result is union (simplified).
		if n.Op == token.Or {
			// a or b: if a is falsy, b. Type = NonFalsy(a) | b. Simplify to union minus false/nil? Keep union.
			return types.NewUnion([]types.Type{l, r})
		}
		return types.NewUnion([]types.Type{l, r})
	}
	c.errorf(diag.UnsupportedOperator, n.Span(), "unsupported operator '%s'", n.Op)
	return types.Any
}

// checkUnOp checks unary operators (not/len/minus/bitwise-not).
//
// 设计者视角：checkUnOp 与 BinOp 对偶——not 恒 Boolean，# 要 lengthable（串/表/
// 数组…），- 要数字（整数保整数），~ 要整数。障碍同 any 放行。取舍：错即报、
// 型照返。排查现场：# 误报先查 isLengthable 的 union 全员判据。
func (c *Checker) checkUnOp(n *ast.UnOpExpr) types.Type {
	x := c.checkExpr(n.X)
	switch n.Op {
	case token.Not:
		return types.Boolean
	case token.Hash:
		if !isAnyLike(x) && !isLengthable(x) {
			c.errorf(diag.LengthOnNonLengthable, n.Span(), "length operator on '%s'", x.String())
		}
		return types.Integer
	case token.Minus:
		if !isAnyLike(x) && !isNumberLike(x) {
			c.errorf(diag.UnaryMinusOnNonNumber, n.Span(), "unary minus on '%s'", x.String())
		}
		if isIntegerLike(x) {
			return types.Integer
		}
		return types.Number
	case token.Tilde:
		if !isAnyLike(x) && !isIntegerLike(x) {
			c.errorf(diag.BitwiseNotOnNonInteger, n.Span(), "bitwise not on '%s'", x.String())
		}
		return types.Integer
	}
	return types.Any
}
