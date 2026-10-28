package checker

import (
	"github.com/typelua/typelua/internal/ast"
	"github.com/typelua/typelua/internal/diag"
	"github.com/typelua/typelua/internal/token"
	"github.com/typelua/typelua/internal/types"
)

// 设计者视角：未标注返回值用 nil 哨兵区分"推断模式"与"void 契约"；
// 作用域与泛型参数栈成对 push/pop，保证嵌套函数不泄漏。
// 函数体：签名+体，泛型栈管 `<T>`，Span 含体尾。
//
// 设计者视角：函数是“签名与体分离”——funcTypeFromLit 只算签名（含泛型占位），
// checkFuncBody 开层验体；未标注返回用 nil 哨兵区分推断与 void（见 retStack）。
// 障碍是嵌套函数泄漏泛型/变参/返回：tparam/值层/vararg/ret/infer/async 六栈成对
// …（略，详见实现；排查先看 Span/诊断）
// 展平（void 即空），泛型约束/默认就地解。障碍是签名内前向泛型：必须在压栈后解，
// 否则 T 查不到。取舍：未标注返回给 [Any]（可赋）而非 nil（体另用哨兵）。不变量：
// 进出 tparam 等长。排查现场：签名泛型丢失先查压栈顺序，变参元型错先查 Array 退元。
func (c *Checker) funcTypeFromLit(fn *ast.FuncLit, generics []*ast.GenericParam) *types.Func {
	// 排查看 Span/诊断，如 `local x=1` 错圈整句。
	c.pushTParams(generics)
	var params []types.FuncParam
	for _, p := range fn.Params {
		var t types.Type = types.Any
		if p.Type != nil {
			t = c.resolveType(p.Type)
		} else if c.opt.NoImplicitAny {
			c.errorf(diag.ImplicitAnyParam, p.Span(), "parameter '%s' implicitly has 'any' type", p.Name.Name)
		}
		params = append(params, types.FuncParam{Name: p.Name.Name, Type: t})
	}
	var va types.Type
	if fn.HasVarArg {
		if fn.VarArgType != nil {
			va = c.resolveType(fn.VarArgType)
			// `...: T[]`? If annotated as array, keep elem? Convention: `...rest: string[]` means each vararg is string? Or whole is array?
			// Our parser: `...rest: string[]` type is array. For checking `...` uses, treat vararg element as elem of array if array.
			if arr, ok := va.(*types.Array); ok {
				va = arr.Elem
			}
		} else {
			va = types.Any
		}
	}
	var rets []types.Type
	if fn.ReturnType != nil {
		rt := c.resolveType(fn.ReturnType)
		rets = flattenReturns(rt)
		if rets == nil {
			rets = []types.Type{}
		}
	} else {
		// Unannotated: permissive `any` single return for assignability;
		// 排查看 Span/诊断，如 `local x=1` 错圈整句。
		rets = []types.Type{types.Any}
	}
	tps := make([]*types.GenericParam, len(generics))
	for i, g := range generics {
		tp := &types.GenericParam{Name: g.Name.Name}
		if g.Constraint != nil {
			tp.Constraint = c.resolveType(g.Constraint)
		}
		if g.Default != nil {
			tp.Default = c.resolveType(g.Default)
		}
		tps[i] = tp
	}
	c.popTParams()
	return &types.Func{Params: params, HasVarArg: fn.HasVarArg, VarArg: va, Returns: rets, TypeParams: tps, IsAsync: fn.IsAsync}
}

// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：flattenReturns 是返回标注的“拆包机”——Tuple 铺为多返回，void 化空，
// 其余包单。障碍是 void 与无标注皆 len==0：此处不区分（调用方以 ReturnType==nil
// 另判），只保形状。nil 输入即 nil。排查现场：多返回少一位先查 Tuple 是否未铺。
func flattenReturns(t types.Type) []types.Type {
	if t == nil {
		return nil
	}
	if tup, ok := t.(*types.Tuple); ok {
		return tup.Elems
	}
	if p, ok := t.(*types.Primitive); ok && p.K == types.KVoid {
		return nil
	}
	return []types.Type{t}
}

// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：checkFuncBody 是函数体的“无菌室标准间”——tparam→值层→label/goto→
// 形参→self（方法）→具名变参→vararg→ret（未标注置 nil 哨兵）→infer→async，验体
// 验 goto 后倒序弹。障碍是 Go 的 nil/空切片难辨（void vs 推断）：借 ReturnType==nil
// 显式置 nil 哨兵。候选：签名体合一 vs 分离；选分离（签名先行供递归调用）。图：
//
//	压六栈 → checkBlock → 验goto → 弹六栈
//
// 不变量：各类栈进出等长。排查现场：返回误报先查哨兵，`...` 越界先查 vararg 标记。
func (c *Checker) checkFuncBody(fn *ast.FuncLit, generics []*ast.GenericParam, ft *types.Func) {
	c.pushTParams(generics)
	c.push() // value scope
	// 排查看 Span/诊断，如 `local x=1` 错圈整句。
	c.labelStack = append(c.labelStack, map[string]token.Span{})
	c.gotoList = append(c.gotoList, nil)
	// 排查看 Span/诊断，如 `local x=1` 错圈整句。
	for i, p := range fn.Params {
		var t types.Type = types.Any
		if i < len(ft.Params) {
			t = ft.Params[i].Type
		}
		c.recordDef(p.Name.Span(), c.bindVar(p.Name.Name, &Symbol{Name: p.Name.Name, Kind: symParam, Type: t, Span: p.Name.Span()}))
	}
	if fn.IsMethod {
		if _, ok := c.cur().vars["self"]; !ok {
			c.cur().vars["self"] = &Symbol{Name: "self", Kind: symParam, Type: types.Any, Span: fn.Span()}
		}
	}
	if fn.HasVarArg {
		// `...` name binding if present.
		if fn.VarArgName != nil {
			// 排查看 Span/诊断，如 `local x=1` 错圈整句。
			var restT types.Type = types.Any
			if ft.VarArg != nil {
				restT = &types.Array{Elem: ft.VarArg}
			} else {
				restT = &types.Array{Elem: types.Any}
			}
			c.recordDef(fn.VarArgName.Span(), c.bindVar(fn.VarArgName.Name, &Symbol{Name: fn.VarArgName.Name, Kind: symParam, Type: restT, Span: fn.VarArgName.Span()}))
		}
		c.varargStack = append(c.varargStack, ft.VarArg)
		if ft.VarArg == nil {
			c.varargStack[len(c.varargStack)-1] = types.Any
		}
	} else {
		c.varargStack = append(c.varargStack, nil) // marker: no vararg
	}
	c.retStack = append(c.retStack, ft.Returns)
	c.asyncStack = append(c.asyncStack, fn.IsAsync)
	// If unannotated (ft.Returns == nil because no annotation vs void?), distinguish: fn.ReturnType == nil => infer mode (nil sentinel).
	// 排查看 Span/诊断，如 `local x=1` 错圈整句。
	// Convention: annotated void => empty slice non-nil? Go nil vs empty: both len 0. Use fn.ReturnType to decide.
	isAnnotated := fn.ReturnType != nil
	if !isAnnotated {
		c.retStack[len(c.retStack)-1] = nil
	}
	c.inferStack = append(c.inferStack, nil)
	c.checkBlock(fn.Body)
	// 校验，排查先看 Span/诊断。
	gotos := c.gotoList[len(c.gotoList)-1]
	labels := c.labelStack[len(c.labelStack)-1]
	for _, g := range gotos {
		if _, ok := labels[g.name]; !ok {
			c.errorf(diag.UndefinedLabel, g.span, "undefined label '%s'", g.name)
		}
	}
	c.gotoList = c.gotoList[:len(c.gotoList)-1]
	c.labelStack = c.labelStack[:len(c.labelStack)-1]
	c.inferStack = c.inferStack[:len(c.inferStack)-1]
	c.retStack = c.retStack[:len(c.retStack)-1]
	c.asyncStack = c.asyncStack[:len(c.asyncStack)-1]
	c.varargStack = c.varargStack[:len(c.varargStack)-1]
	c.pop()
	c.popTParams()
	_ = isAnnotated
}

// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：pushTParams 是类型参数的“开层”——约束/默认就地 resolve 后入栈顶
// map。障碍是约束内再引泛型：此时外层已在栈，逐层向外可解。取舍：拷贝存
// GenericParam（防别名）。不变量：与 popTParams 成对。排查现场：T 找不到先查
// 是否忘 push（如签名外解）。
func (c *Checker) pushTParams(gs []*ast.GenericParam) {
	m := map[string]*types.GenericParam{}
	for _, g := range gs {
		var con types.Type
		var def types.Type
		if g.Constraint != nil {
			con = c.resolveType(g.Constraint)
		}
		if g.Default != nil {
			def = c.resolveType(g.Default)
		}
		m[g.Name.Name] = &types.GenericParam{Name: g.Name.Name, Constraint: con, Default: def}
	}
	c.tparamStack = append(c.tparamStack, m)
}

// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：popTParams 与 push 成对关层，防泛型泄漏到外层函数。障碍是 defer
// 顺序错一位即串味；故签名与体各用 defer 保配对。不变量：栈非空（调用方保证）。
// 排查现场：外层见到内层 T，先查 pop 是否漏调。
func (c *Checker) popTParams() {
	c.tparamStack = c.tparamStack[:len(c.tparamStack)-1]
}

// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：lookupTParam 是泛型的“就近查”——自顶向下扫 tparamStack，遮蔽自然
// 生效（内层 T 盖外层）。障碍是值层与类型层双轨：此处只查类型轨，值名同名不扰。
// 取舍：线性扫（层浅）。排查现场：T 绑错层先查内外重名遮蔽。
func (c *Checker) lookupTParam(name string) *types.GenericParam {
	for i := len(c.tparamStack) - 1; i >= 0; i-- {
		if v, ok := c.tparamStack[i][name]; ok {
			return v
		}
	}
	return nil
}
