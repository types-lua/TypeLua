package checker

import (
	"github.com/typelua/typelua/internal/ast"
	"github.com/typelua/typelua/internal/token"
	"github.com/typelua/typelua/internal/types"
)

// Function types and bodies, plus generic parameter scopes.

// ---------- function types & bodies ----------

func (c *Checker) funcTypeFromLit(fn *ast.FuncLit, generics []*ast.GenericParam) *types.Func {
	// Push tparams for param/return resolution.
	c.pushTParams(generics)
	var params []types.FuncParam
	for _, p := range fn.Params {
		var t types.Type = types.Any
		if p.Type != nil {
			t = c.resolveType(p.Type)
		} else if c.opt.NoImplicitAny {
			c.errorf("TL3040", p.Span(), "parameter '%s' implicitly has 'any' type", p.Name.Name)
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
		// body checking still skips (see checkFuncBody retStack nil).
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

func (c *Checker) checkFuncBody(fn *ast.FuncLit, generics []*ast.GenericParam, ft *types.Func) {
	c.pushTParams(generics)
	c.push() // value scope
	// Labels for goto validation.
	c.labelStack = append(c.labelStack, map[string]token.Span{})
	c.gotoList = append(c.gotoList, nil)
	// Params.
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
			// Named rest collects array.
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
	// Our funcTypeFromLit returns nil slice when no annotation; void annotation returns empty non-nil? Actually returns nil too for void (len 0). Need to distinguish.
	// Convention: annotated void => empty slice non-nil? Go nil vs empty: both len 0. Use fn.ReturnType to decide.
	isAnnotated := fn.ReturnType != nil
	if !isAnnotated {
		c.retStack[len(c.retStack)-1] = nil
	}
	c.inferStack = append(c.inferStack, nil)
	c.checkBlock(fn.Body)
	// Validate gotos.
	gotos := c.gotoList[len(c.gotoList)-1]
	labels := c.labelStack[len(c.labelStack)-1]
	for _, g := range gotos {
		if _, ok := labels[g.name]; !ok {
			c.errorf("TL3050", g.span, "undefined label '%s'", g.name)
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

func (c *Checker) popTParams() {
	c.tparamStack = c.tparamStack[:len(c.tparamStack)-1]
}

func (c *Checker) lookupTParam(name string) *types.GenericParam {
	for i := len(c.tparamStack) - 1; i >= 0; i-- {
		if v, ok := c.tparamStack[i][name]; ok {
			return v
		}
	}
	return nil
}
