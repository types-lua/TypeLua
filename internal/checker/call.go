package checker

import (
	"github.com/typelua/typelua/internal/ast"
	"github.com/typelua/typelua/internal/diag"
	"github.com/typelua/typelua/internal/token"
	"github.com/typelua/typelua/internal/types"
)

// 调用两遍相亲：先 inferInto 反推泛型再 IsAssignable终验，多返回仅末参展开，extra 静默丢。
// 做 IsAssignable，避免先报错后 refine 的级联误报。多返回只在末参展开（Lua
// 语义，见 checkCallArgs）。障碍是显式实参（f<T>）与推断混用：必须先绑显式、
// 后补推断、缺省走 Default。候选：一遍边推边报 vs 两遍；选两遍，错误只报一次
// 详见实现，排查看 Span。
// sub 是否被第二遍覆盖，元数误报先看 required/optional/nilable 三态。

// ---------- calls & generics ----------

// lookupMethod 是方法查号台：`p:move(1)` 先查字段，any 兜底返 Any，无方法报错仍放行防级联。
//
// 设计者视角：lookupMethod 是方法调用的“查号台”——先 lookupField，命中即返；
// anyLike 兜底返 Any 继续。障碍是无方法时既要报错又不能断流：返 Any 让后续参数
// 照常检查，抑制级联。取舍：报错一次、类型放行。不变量：永不返 nil。排查现场：
// NoMethod 误报先查 ot 是否被提前 widen 成结构丢失（如类被拍成 Object）。
func (c *Checker) lookupMethod(ot types.Type, name string, sp token.Span) types.Type {
	if f, ok := lookupField(ot, name); ok {
		return f.Type
	}
	if isAnyLike(ot) {
		return types.Any
	}
	c.errorf(diag.NoMethod, sp, "type '%s' has no method '%s'", ot.String(), name)
	return types.Any
}

// lookupMethodField 是带密级查号台：多回 Access/Owner 供 private/protected 门禁，any 无密级即放行。
//
// 设计者视角：lookupMethodField 是查号台的“带密级版”——除类型外还带回 Access/
// Owner，供 checkAccess 做 private/protected 门禁。障碍是 any 下无密级：给空
// Field{Any} 即放行，避免对动态代码误拦。取舍：查找与鉴权分两步（先查后验），
// 调用方（expr.go 方法调用）显式验。不变量：失败亦返 Any 占位。排查现场：越权
// 未报先查 Owner 是否丢失（如经 Substitute 后 Def 指针漂移）。
func (c *Checker) lookupMethodField(ot types.Type, name string, sp token.Span) types.Field {
	if f, ok := lookupField(ot, name); ok {
		return f
	}
	if isAnyLike(ot) {
		return types.Field{Type: types.Any}
	}
	c.errorf(diag.NoMethod, sp, "type '%s' has no method '%s'", ot.String(), name)
	return types.Field{Type: types.Any}
}

// resolveTypeArgs 把 `f<T>(x)` 显式实参译成语义值，空列返 nil（下游以 len==0 判无显式）。
//
// 设计者视角：resolveTypeArgs 把调用点的显式类型实参从语法译为语义值，好比把
// 菜单上的字译成后厨的料。障碍是空列表应返 nil 而非空切片：下游 bindExplicitArgs
// 以 len==0 判“无显式”，空切片会误触分支。取舍：逐个 resolveType，错误各自报。
// 不变量：输入空即返 nil。排查现场：泛型显式失效先查 TypeArgs 是否在此被吞（空
// 与 nil 混淆）。
func (c *Checker) resolveTypeArgs(nodes []ast.TypeNode) []types.Type {
	if len(nodes) == 0 {
		return nil
	}
	out := make([]types.Type, len(nodes))
	for i, n := range nodes {
		out[i] = c.resolveType(n)
	}
	return out
}

// bindExplicitArgs 按位先占显式实参，推断只填空位，缺省走 Default，如 `f<string>(x)`。
//
// 设计者视角：bindExplicitArgs 是泛型的“占座制”——显式实按位先占（TS 式部分应用），
// 推断只填空位，缺省走 Default。障碍是三源冲突（显式/推断/默认）与约束校验时机：
// 必须在此先验显式约束，推断约束留待实例化后，否则报错位置漂移。候选：整体替换
// vs 逐位；选逐位 exSub，只换已占位，余量保持 GenericParam 供 inferInto。超量截断
// 并报错。不变量：sub 与返回 fn 同步占座。排查现场：部分显式后推断错乱，先查
// Default 回填是否用 Substitute(sub) 传递了已占位。
func (c *Checker) bindExplicitArgs(fn *types.Func, explicit []types.Type, callSpan token.Span, sub map[string]types.Type) *types.Func {
	if len(explicit) == 0 {
		return fn
	}
	if len(fn.TypeParams) == 0 {
		c.errorf(diag.GenericArgsInvalid, callSpan, "explicit type arguments given to non-generic function")
		return fn
	}
	if len(explicit) > len(fn.TypeParams) {
		c.errorf(diag.GenericArgsInvalid, callSpan, "expected %d type argument(s), got %d", len(fn.TypeParams), len(explicit))
		explicit = explicit[:len(fn.TypeParams)]
	}
	exSub := map[string]types.Type{}
	for i, gp := range fn.TypeParams {
		if i >= len(explicit) {
			break
		}
		a := explicit[i]
		if gp.Constraint != nil && !types.IsAssignable(a, gp.Constraint, c.opt.toTypes()) {
			c.errorf(diag.GenericArgsInvalid, callSpan, "type argument '%s' does not satisfy constraint '%s'", a.String(), gp.Constraint.String())
		}
		exSub[gp.Name] = a
		sub[gp.Name] = a
	}
	// 无显式未推断形参走 Default 缺省，如 `f<T=T0>`。
	for _, gp := range fn.TypeParams {
		if _, ok := sub[gp.Name]; !ok && gp.Default != nil {
			sub[gp.Name] = types.Substitute(gp.Default, sub)
		}
	}
	if out, ok := types.Substitute(fn, exSub).(*types.Func); ok {
		return out
	}
	return fn
}

// checkCall 是调用总装线：`f<T>(x)` 先绑显式再 infer，末位多返回展开，多余实参静默丢。
//
// 设计者视角：checkCall 是调用的“总装线”，顺序即语义，不可调换：any 短路→
// ClassValue 转实例化→非 Func 报 NotCallable→绑显式→算实参（末位多返回展开）→
// 记表键→查元数→inferInto 初推→终版 sub 复查（每调用只报一错）→代入返回。障碍
// 有三：Lua 多余实参合法（静默丢）、缺失形参补 nil、泛型初推不准；故分两遍，
// 第一遍只推不报，第二遍才报。图：
//
//	args → [初推 sub] → [复查 IsAssignable] → rets=Substitute(Returns, sub)
//
// 候选：一遍即报 vs 两遍；选两遍，换级联误报的根治。不变量：失败返 [Any] 不断流。
// 排查现场：参数误报先看初推是否被后实参 refine（应等第二遍），多返回丢失先看
// 是否非末位误展开。
func (c *Checker) checkCall(opSpan token.Span, fnT types.Type, args []ast.Expr, callSpan token.Span, explicit []types.Type) []types.Type {
	if isAnyLike(fnT) {
		for _, a := range args {
			c.checkExpr(a)
		}
		return []types.Type{types.Any}
	}
	// 类名直呼即实例化，如 `Point(1,2)` 走 __call。
	if cv, ok := fnT.(*types.ClassValue); ok {
		return c.checkClassInstantiate(cv, args, callSpan, explicit)
	}
	fn, ok := fnT.(*types.Func)
	if !ok {
		// 带 __call 表暂不可呼叫，v1 仅函数可呼。
		for _, a := range args {
			c.checkExpr(a)
		}
		c.errorf(diag.NotCallable, callSpan, "cannot call type '%s'", fnT.String())
		return []types.Type{types.Any}
	}
	// 泛型初推：形参 GenericParam 占位，如 `f(x:T)` 遇 number 即记。
	// Infer: map param name -> inferred type from args.
	sub := map[string]types.Type{}
	// 显式实参按位先占，余位再推。
	// 缺位回 Default，如 TS 偏应用。
	fn = c.bindExplicitArgs(fn, explicit, callSpan, sub)
	// 算实参：仅末位多返回展开，如 `f(g())` g 多值铺开。
	argTypes := c.checkCallArgs(args)
	// 内联表按形参记成员键，
	// 如 `f({x=1})` 供接口实现追踪。
	for i, a := range args {
		if tl, ok := a.(*ast.TableLit); ok && i < len(fn.Params) {
			c.recordTableKeys(tl, fn.Params[i].Type)
		}
	}
	// 元数只卡必填，运行时多余合法。
	req := 0
	for _, p := range fn.Params {
		if !p.Optional {
			req++
		}
	}
	if len(argTypes) < req && !fn.HasVarArg {
		// 缺实参且缺位非可选/非 nil 即报错，如少传必填。
		// 缺位可选或含 nil 即放行。
		missingOK := true
		for i := len(argTypes); i < len(fn.Params); i++ {
			if !fn.Params[i].Optional && !types.ContainsNil(fn.Params[i].Type) {
				missingOK = false
				break
			}
		}
		if !missingOK {
			c.errorf(diag.ArgCountMismatch, callSpan, "expected %d argument(s), got %d", req, len(argTypes))
		}
	}
	// 逐个验实参。
	for i, at := range argTypes {
		var pt types.Type
		if i < len(fn.Params) {
			pt = fn.Params[i].Type
		} else if fn.HasVarArg {
			if fn.VarArg != nil {
				pt = fn.VarArg
			} else {
				pt = types.Any
			}
		} else {
			// 多余实参静默丢，如 `f(1,2,3)` 调二参函数。
			continue
		}
		// 从该形参反推泛型。
		inferInto(pt, at, sub)
		if !types.IsAssignable(at, applySub(pt, sub), c.opt.toTypes()) {
			// 初推不准暂不报，待终版 sub 复查防级联。
			// 报错推迟到第二遍。
		}
	}
	// 第二遍用终版 sub 复查报错。
	for i, at := range argTypes {
		var pt types.Type
		if i < len(fn.Params) {
			pt = applySub(fn.Params[i].Type, sub)
		} else if fn.HasVarArg {
			if fn.VarArg != nil {
				pt = applySub(fn.VarArg, sub)
			} else {
				continue
			}
		} else {
			continue
		}
		if !types.IsAssignable(at, pt, c.opt.toTypes()) {
			c.errorf(diag.ArgTypeMismatch, args[i].Span(), "argument %d: type '%s' is not assignable to '%s'", i+1, at.String(), pt.String())
			break // one error per call to avoid cascades.
		}
	}
	// 代入返回，如 `-> T` 换实型。
	var rets []types.Type
	for _, r := range fn.Returns {
		rets = append(rets, applySub(r, sub))
	}
	// 约束定义期已验，此处 Unknown 照留。
	if len(rets) == 0 {
		return nil
	}
	return rets
}

// checkCallArgs 验实参：末位多返回展开，如 `f(g())` g 多值铺开。
//
// 设计者视角：checkCallArgs 复刻 Lua 的“末位展开”语义——f(g()) 中 g 的多返回
// 只在末参铺开，非末取首值。障碍是 VarArg 的多值建模：`...` 恒作单值（见 expr.go），
// 此处不特殊展开。取舍：空参返 nil 而非空切片，下游以 len 判元数。不变量：返回
// 长度≥参数长度仅当末位多返回。排查现场：参数个数对不上先查末位是否为 Call/Await
// 多返回。
func (c *Checker) checkCallArgs(args []ast.Expr) []types.Type {
	if len(args) == 0 {
		return nil
	}
	var out []types.Type
	for i, a := range args {
		if i == len(args)-1 {
			out = append(out, c.checkExprMulti(a)...)
		} else {
			out = append(out, c.checkExprSingle(a))
		}
	}
	return out
}

// inferInto 从形实对描泛型落点，如 `f<T>(x: T)` 见 number 即记 T=number。
//
// 设计者视角：inferInto 是泛型推断的“拓印机”——沿形参结构（GenericParam/Array/
// Map/Tuple/Object/Union/Conditional）往实参里描，把 T 的落点记进 sub。障碍是
// 冲突二次绑定（两参同推一 T 而实参不同）：直接覆盖会丢信息，故并为 union；
// Func 形参跳过深推（函数逆变复杂，保简单）。候选：深度统一 vs 浅推；选受控深推，
// Union 取首个可赋成员。any 实参亦记（后续约束处复查）。不变量：只增 sub，不报
// 错。排查现场：T 推成 union 膨胀，先查是否多位置推同一参；推不出先查形参是否
// 藏在 Func 里。
func inferInto(param, arg types.Type, sub map[string]types.Type) {
	switch p := param.(type) {
	case *types.GenericParam:
		if existing, ok := sub[p.Name]; ok {
			// 二绑冲突并为 union，如两处推 T 得 number/string 即并。
			if existing.String() != arg.String() {
				sub[p.Name] = types.NewUnion([]types.Type{existing, arg})
			}
			return
		}
		sub[p.Name] = arg
	case *types.InferParam:
		// 条件 infer 位同形参绑，如 `infer R`。
		if existing, ok := sub[p.Name]; ok {
			if existing.String() != arg.String() {
				sub[p.Name] = types.NewUnion([]types.Type{existing, arg})
			}
			return
		}
		sub[p.Name] = arg
	case *types.Conditional:
		// 沿 Check 模式深推，如 `Elem<T>` 见 `[1,2]`。
		inferInto(p.Check, arg, sub)
	case *types.Array:
		switch a := arg.(type) {
		case *types.Array:
			inferInto(p.Elem, a.Elem, sub)
		case *types.Tuple:
			// `{1,2,3}` 按位推，`T[]` 逐元收。
			for _, e := range a.Elems {
				inferInto(p.Elem, e, sub)
			}
		}
	case *types.Chan:
		if a, ok := arg.(*types.Chan); ok {
			inferInto(p.Elem, a.Elem, sub)
		}
	case *types.Map:
		if a, ok := arg.(*types.Map); ok {
			inferInto(p.Key, a.Key, sub)
			inferInto(p.Value, a.Value, sub)
		}
	case *types.Tuple:
		if a, ok := arg.(*types.Tuple); ok && len(a.Elems) == len(p.Elems) {
			for i := range p.Elems {
				inferInto(p.Elems[i], a.Elems[i], sub)
			}
		}
	case *types.Func:
		// 函数形参跳深推，保简单。
	case *types.Object:
		if a, ok := arg.(*types.Object); ok {
			for name, f := range p.Fields {
				if af, ok := a.Fields[name]; ok {
					inferInto(f.Type, af.Type, sub)
				}
			}
		}
	case *types.Union:
		// 实参命中某联合成员即往该成员推。
		for _, m := range p.Types {
			if types.IsAssignable(arg, m, types.Options{StrictNilChecks: true}) {
				inferInto(m, arg, sub)
				break
			}
		}
	}
}

// applySub 把推断 sub 代入类型，空 sub 即原样回。
//
// 设计者视角：applySub 是推断的“显影液”——把含 GenericParam 的形参/返回按 sub
// 换成实型。障碍是空 sub 高频出现（非泛型调用）：必须早返避开 Substitute 的
// 分配与递归。取舍：薄封装 types.Substitute，不在此验约束。不变量：空表恒返
// 原指针。排查现场：返回里残留 T，先查调用方是否忘调此步（如直接用了 fn.Returns）。
func applySub(t types.Type, sub map[string]types.Type) types.Type {
	if len(sub) == 0 {
		return t
	}
	return types.Substitute(t, sub)
}
