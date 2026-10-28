package checker

import (
	"strconv"
	"strings"

	"github.com/typelua/typelua/internal/ast"
	"github.com/typelua/typelua/internal/diag"
	"github.com/typelua/typelua/internal/token"
	"github.com/typelua/typelua/internal/types"
)

// 设计者视角：resolveTypeWithSub 是类型语法到 types.Type 的唯一入口；
// generic substitution 通过 sub 表传播，conditional/mapped/indexed access
// 尽量在具体类型上求值，其余保留为延迟节点。
// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：类型解析是语法到语义的“唯一关口”——resolveTypeWithSub 按节点分流，
// sub 表携泛型实参下行；conditional/mapped/indexed 具体可算即算详见实现，排查看 Span。
// …（略，详见实现；排查先看 Span/诊断）
// 设计者视角：resolveType 是对外便门——空 sub 启程，逻辑全在 WithSub。顶层无泛型
// 语境，sub 空即对。排查现场：泛型内解类型误用此处（丢 sub）会把 T 解成形参拷贝，
// 应调 WithSub。
func (c *Checker) resolveType(n ast.TypeNode) types.Type {
	return c.resolveTypeWithSub(n, map[string]types.Type{})
}

// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：见上关口注释——nil 即 Any（恢复），Paren 透视，Optional 升 nil|T，
// Tuple/Union/Intersection 逐员，Table/Func 另函，Typeof/Keyof/Mapped/Indexed/
// Conditional/Infer 各有门禁（infer 限 extends 内）。障碍是未知节点：报
// UnsupportedTypeNode 后 Any 续流。取舍：分流序固定（Named 最频首判）。不变量：
// 永不返 nil。排查现场：某语法无型先查 switch 是否缺分支。
func (c *Checker) resolveTypeWithSub(n ast.TypeNode, sub map[string]types.Type) types.Type {
	if n == nil {
		// 恢复占位：parser 已报永不崩，如坏表回空表。
		return types.Any
	}
	switch t := n.(type) {
	case *ast.NamedType:
		return c.resolveNamed(t, sub)
	case *ast.LiteralType:
		return c.resolveLiteral(t)
	case *ast.ArrayType:
		return &types.Array{Elem: c.resolveTypeWithSub(t.Elem, sub)}
	case *ast.UnionType:
		var ms []types.Type
		for _, m := range t.Types {
			ms = append(ms, c.resolveTypeWithSub(m, sub))
		}
		return types.NewUnion(ms)
	case *ast.IntersectionType:
		var ms []types.Type
		for _, m := range t.Types {
			ms = append(ms, c.resolveTypeWithSub(m, sub))
		}
		return types.NewIntersection(ms)
	case *ast.OptionalType:
		e := c.resolveTypeWithSub(t.Elem, sub)
		return types.NewUnion([]types.Type{e, types.Nil})
	case *ast.ParenType:
		return c.resolveTypeWithSub(t.Elem, sub)
	case *ast.TupleType:
		var es []types.Type
		for _, e := range t.Elems {
			es = append(es, c.resolveTypeWithSub(e, sub))
		}
		return &types.Tuple{Elems: es}
	case *ast.TableType:
		return c.resolveTableType(t, sub)
	case *ast.FuncType:
		return c.resolveFuncType(t, sub)
	case *ast.TypeofType:
		return c.resolveTypeof(t)
	case *ast.KeyofType:
		e := c.resolveTypeWithSub(t.Elem, sub)
		return c.resolveKeyof(e, t.Span())
	case *ast.MappedType:
		return c.resolveMapped(t, sub)
	case *ast.IndexedAccessType:
		return c.resolveIndexedAccess(t, sub)
	case *ast.InferType:
		if c.extendsDepth == 0 {
			c.errorf(diag.InferOutsideExtends, t.Span(), "'infer' is only allowed in the extends clause of a conditional type")
			return types.Any
		}
		return &types.InferParam{Name: t.Name.Name}
	case *ast.ConditionalType:
		return c.resolveConditional(t, sub)
	default:
		c.errorf(diag.UnsupportedTypeNode, n.Span(), "unsupported type node %T", n)
		return types.Any
	}
}

// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：resolveNamed 是命名分流的“五道闸”——原语（无参裸名）→限定名拒
// （v1 不支模块限定）→`...T` 剩标记→sub/tparam 泛型→map/Array/chan 特殊→用户
// Entry（类镜面短路+patch，余走 resolveTypeEntryWithArgs）。障碍是类构建中自引：
// 镜面详见实现，排查看 Span。
// 候选：复用通用实例化 vs 手工镜面；选手工（递归不断）。不变量：用户径恒打卡
// recordTypeUse。排查现场：递归类悬空先查镜面判据，泛型 T 解错先查 sub/tparam 序。
func (c *Checker) resolveNamed(t *ast.NamedType, sub map[string]types.Type) types.Type {
	// 排查看 Span/诊断，如 `local x=1` 错圈整句。
	if len(t.Args) == 0 {
		switch t.Name {
		case "any":
			return types.Any
		case "unknown":
			return types.Unknown
		case "never":
			return types.Never
		case "void":
			return types.Void
		case "nil":
			return types.Nil
		case "boolean":
			return types.Boolean
		case "number":
			return types.Number
		case "integer":
			return types.Integer
		case "float":
			return types.Float
		case "string":
			return types.String
		case "table":
			return types.Table
		case "thread":
			return types.Thread
		case "userdata":
			return types.Userdata
		case "cdata":
			return types.Cdata
		case "ctype":
			return types.Ctype
		case "function":
			return types.Function
		}
	}
	// 排查看 Span/诊断，如 `local x=1` 错圈整句。
	if strings.Contains(t.Name, ".") && t.Name != "..." {
		c.errorf(diag.QualifiedTypeUnsupported, t.Span(), "qualified type '%s' is not supported", t.Name)
		return types.Any
	}
	// `...T` rest marker (tuple patterns, vararg positions).
	if t.Name == "..." {
		if len(t.Args) != 1 {
			c.errorf(diag.BuiltinTypeArgCount, t.Span(), "'...' expects 1 type argument, got %d", len(t.Args))
			return types.Any
		}
		return &types.Ref{Name: "...", Args: []types.Type{c.resolveTypeWithSub(t.Args[0], sub)}}
	}
	// 泛型详见实现，排查看 Span。
	if s, ok := sub[t.Name]; ok && len(t.Args) == 0 {
		return s
	}
	if gp := c.lookupTParam(t.Name); gp != nil && len(t.Args) == 0 {
		if s, ok := sub[t.Name]; ok {
			return s
		}
		// 返回，排查先看 Span/诊断。
		return &types.GenericParam{Name: gp.Name, Constraint: gp.Constraint, Default: gp.Default}
	}
	// 排查看 Span/诊断，如 `local x=1` 错圈整句。
	if t.Name == "map" || t.Name == "table" {
		if len(t.Args) == 0 {
			return types.Table
		}
		if len(t.Args) != 2 {
			c.errorf(diag.BuiltinTypeArgCount, t.Span(), "'%s' expects 2 type arguments, got %d", t.Name, len(t.Args))
			return types.Table
		}
		return &types.Map{Key: c.resolveTypeWithSub(t.Args[0], sub), Value: c.resolveTypeWithSub(t.Args[1], sub)}
	}
	if t.Name == "Array" || t.Name == "array" {
		if len(t.Args) != 1 {
			c.errorf(diag.BuiltinTypeArgCount, t.Span(), "'Array' expects 1 type argument, got %d", len(t.Args))
			return types.Any
		}
		return &types.Array{Elem: c.resolveTypeWithSub(t.Args[0], sub)}
	}
	if t.Name == "chan" {
		if len(t.Args) != 1 {
			c.errorf(diag.BuiltinTypeArgCount, t.Span(), "'chan' expects 1 type argument, got %d (use 'chan<T>')", len(t.Args))
			return types.Any
		}
		return &types.Chan{Elem: c.resolveTypeWithSub(t.Args[0], sub)}
	}
	// 排查看 Span/诊断，如 `local x=1` 错圈整句。
	e := c.cur().lookupType(t.Name)
	if e == nil {
		c.errorf(diag.UndefinedType, t.Span(), "undefined type '%s'", t.Name)
		return types.Any
	}
	c.recordTypeUse(t.Span(), e)
	// 排查看 Span/诊断，如 `local x=1` 错圈整句。
	// shell so recursive types (e.g. `next: Box<T> | nil`) keep working.
	// 排查看 Span/诊断，如 `local x=1` 错圈整句。
	if e.classDecl != nil && e.resolving && e.classShell != nil {
		var sargs []types.Type
		for _, a := range t.Args {
			sargs = append(sargs, c.resolveTypeWithSub(a, sub))
		}
		if isIdentityClassArgs(e, sargs) {
			return e.classShell
		}
		inst := &types.Class{Def: e.classDef, Name: e.name, Args: sargs, Params: classParamNames(e.params), Fields: map[string]types.ClassField{}}
		sub2 := c.classSubFromTypes(e, sargs, t.Span())
		e.classPatches = append(e.classPatches, func() {
			if full, ok := types.Substitute(e.classShell, sub2).(*types.Class); ok {
				inst.Fields = full.Fields
				inst.Base = full.Base
			}
		})
		return inst
	}
	var args []types.Type
	for _, a := range t.Args {
		args = append(args, c.resolveTypeWithSub(a, sub))
	}
	return c.resolveTypeEntryWithArgs(e, args, t.Span())
}

// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：resolveLiteral 是字面标注的“直译”——true/false→Boolean 字面，nil→Nil，
// 引号/长括号→String 字面，余者→Number 字面（整数性交判定）。Trim 消空格。排查现场：
// 字面基错（如整数标成 Number）先查此处 Base 选择。
func (c *Checker) resolveLiteral(t *ast.LiteralType) types.Type {
	v := strings.TrimSpace(t.Value)
	if v == "true" || v == "false" {
		return &types.Literal{Value: v, Base: types.Boolean}
	}
	if v == "nil" {
		return types.Nil
	}
	if strings.HasPrefix(v, "\"") || strings.HasPrefix(v, "'") || strings.HasPrefix(v, "[") {
		return &types.Literal{Value: v, Base: types.String}
	}
	// 排查看 Span/诊断，如 `local x=1` 错圈整句。
	return &types.Literal{Value: v, Base: types.Number}
}

// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：resolveTableType 是表型的“装配”——索引器直装，方法简写取 MethodSig，
// 余取 Type，optional/readonly 照搬。表型不引入泛型（不压栈）。障碍是方法简写与
// 字段同名：以后者 MethodSig 为准。排查现场：表方法类型错先查 IsMethod 分支。
func (c *Checker) resolveTableType(t *ast.TableType, sub map[string]types.Type) types.Type {
	// 排查看 Span/诊断，如 `local x=1` 错圈整句。
	obj := &types.Object{Fields: map[string]types.Field{}}
	for _, f := range t.Fields {
		if f.IsIndexer {
			obj.Indexers = append(obj.Indexers, types.Indexer{Key: c.resolveTypeWithSub(f.IndexKey, sub), Value: c.resolveTypeWithSub(f.IndexVal, sub)})
			continue
		}
		var ft types.Type
		if f.IsMethod && f.MethodSig != nil {
			ft = c.resolveTypeWithSub(f.MethodSig, sub)
		} else {
			ft = c.resolveTypeWithSub(f.Type, sub)
		}
		obj.Fields[f.Name] = types.Field{Type: ft, Optional: f.Optional, Readonly: f.Readonly}
	}
	return obj
}

// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：resolveFuncType 是函数型的“小全套”——tparam 先压（签名内泛型可见），
// 形参无名即空串（占位），IsVarArg 转 vararg（`...: T[]` 退元），返回逐个解，
// 泛型约束/默认就地解。障碍是 Params 与 VarArgType 双通道：两处皆收再合（防解析
// 差异）。取舍：缺省 Any。排查现场：函数型泛型漏先查压栈，变参元错先查退元。
func (c *Checker) resolveFuncType(t *ast.FuncType, sub map[string]types.Type) types.Type {
	c.pushTParams(t.Generics)
	defer c.popTParams()
	var ps []types.FuncParam
	for _, p := range t.Params {
		var pt types.Type = types.Any
		if p.Type != nil {
			pt = c.resolveTypeWithSub(p.Type, sub)
		}
		name := ""
		if p.Name != nil {
			name = p.Name.Name
		}
		if p.IsVarArg {
			// 变参详见实现，排查看 Span。
			continue
		}
		ps = append(ps, types.FuncParam{Name: name, Type: pt, Optional: p.Optional})
	}
	var va types.Type
	hasVA := t.HasVarArg
	if t.VarArgType != nil {
		va = c.resolveTypeWithSub(t.VarArgType, sub)
		hasVA = true
		if arr, ok := va.(*types.Array); ok {
			va = arr.Elem
		}
	}
	// 排查看 Span/诊断，如 `local x=1` 错圈整句。
	for _, p := range t.Params {
		if p.IsVarArg {
			hasVA = true
			if p.Type != nil {
				va = c.resolveTypeWithSub(p.Type, sub)
				if arr, ok := va.(*types.Array); ok {
					va = arr.Elem
				}
			} else if va == nil {
				va = types.Any
			}
		}
	}
	var rs []types.Type
	for _, r := range t.Returns {
		rs = append(rs, c.resolveTypeWithSub(r, sub))
	}
	tps := make([]*types.GenericParam, len(t.Generics))
	for i, g := range t.Generics {
		tp := &types.GenericParam{Name: g.Name.Name}
		if g.Constraint != nil {
			tp.Constraint = c.resolveTypeWithSub(g.Constraint, sub)
		}
		if g.Default != nil {
			tp.Default = c.resolveTypeWithSub(g.Default, sub)
		}
		tps[i] = tp
	}
	return &types.Func{Params: ps, HasVarArg: hasVA, VarArg: va, Returns: rs, TypeParams: tps}
}

// resolveConditional resolves `Check extends Extends ? True : False`.
// 排查看 Span/诊断，如 `local x=1` 错圈整句。
// 排查看 Span/诊断，如 `local x=1` 错圈整句。
// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：条件型是“TS 式分配”——Check/Extends 先解（extends 内开 extendsDepth
// 放 infer），infer 名绑 InferParam 仅入真支 sub，真/假支各解，裸参详见实现，排查看 Span。
// 标 Distributive，能算详见实现，排查看 Span。
// （TS 同款），假支用原 sub。取舍：延迟一律 permissive（判定放行）。图：
//
//	Check → Extends(infer开) → 真支(含infer)/假支 → TryEval?即值:延迟
//
// 排查现场：条件恒延迟先查是否泛型未实例化，infer 漏先查 collectInferNames。

func (c *Checker) resolveConditional(n *ast.ConditionalType, sub map[string]types.Type) types.Type {
	check := c.resolveTypeWithSub(n.Check, sub)
	c.extendsDepth++
	ext := c.resolveTypeWithSub(n.Extends, sub)
	c.extendsDepth--
	// `infer U` names bind in the true branch only (like TS).
	sub2 := make(map[string]types.Type, len(sub)+2)
	for k, v := range sub {
		sub2[k] = v
	}
	for name := range collectInferNames(n.Extends, nil) {
		sub2[name] = &types.InferParam{Name: name}
	}
	trueB := c.resolveTypeWithSub(n.True, sub2)
	falseB := c.resolveTypeWithSub(n.False, sub)
	distributive := isNakedTypeParam(n.Check, c, sub)
	cond := &types.Conditional{Check: check, Extends: ext, TrueBranch: trueB, FalseBranch: falseB, Distributive: distributive}
	if r, ok := types.TryEvalConditional(cond, c.opt.toTypes()); ok {
		return r
	}
	return cond
}

// isNakedTypeParam reports `T` (bare parameter reference, no args).
//
// 设计者视角：isNakedTypeParam 辨分配条件——裸 T（无参、非限定、命中 sub 或 tparam）
// 才分配，`T[]`/`A.T` 皆非裸。障碍是 sub 与 tparam 双源：任中即裸。排查现场：
// 条件该分配未分配先查 Check 是否带参。
func isNakedTypeParam(n ast.TypeNode, c *Checker, sub map[string]types.Type) bool {
	nt, ok := n.(*ast.NamedType)
	if !ok || len(nt.Args) > 0 || strings.Contains(nt.Name, ".") {
		return false
	}
	if _, ok := sub[nt.Name]; ok {
		return true
	}
	return c.lookupTParam(nt.Name) != nil
}

// resolveMapped resolves `{ [K in Keys]?: V }`, expanding concrete
// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：映射型是“批量印章”——约束先解，键变量压栈后解值，同态基详见实现，排查看 Span。
// 无错才取（保静默），能展（TryEval）即展，具体不可展才报，否则留延迟。障碍是
// 定义处泛型未填（约束为 Any/Unknown）：此为常态，不报（defer）。候选： Halk 全展
// vs 具体展；选具体展。取舍：诊断数快照（nd）保同态重解静默。排查现场：映射恒延迟
// 先查约束是否泛型，误报先查 IsConcreteType 判据。

func (c *Checker) resolveMapped(n *ast.MappedType, sub map[string]types.Type) types.Type {
	nd := len(c.diags)
	constraint := c.resolveTypeWithSub(n.Constraint, sub)
	// 排查看 Span/诊断，如 `local x=1` 错圈整句。
	c.pushTParams([]*ast.GenericParam{{Name: n.Param}})
	value := c.resolveTypeWithSub(n.Value, sub)
	c.popTParams()
	m := &types.Mapped{Param: n.Param.Name, Constraint: constraint, Optional: n.Optional, OptMinus: n.OptMinus, Readonly: n.Readonly, RoMinus: n.RoMinus, Value: value}
	// Homomorphic base: `{[K in keyof T]...}` inherits T's field modifiers
	// unless an explicit `?`/`-?`/`readonly`/`-readonly` overrides them.
	// 排查看 Span/诊断，如 `local x=1` 错圈整句。
	if kn, ok := n.Constraint.(*ast.KeyofType); ok && len(c.diags) == nd {
		if base := c.resolveTypeWithSub(kn.Elem, sub); base != nil {
			if o, ok := base.(*types.Object); ok {
				m.Homomorphic = o
			}
		}
	}
	if r, ok := types.TryEvalMapped(m, c.opt.toTypes()); ok {
		return r
	}
	// 排查看 Span/诊断，如 `local x=1` 错圈整句。
	// 排查看 Span/诊断，如 `local x=1` 错圈整句。
	// 排查看 Span/诊断，如 `local x=1` 错圈整句。
	if types.IsConcreteType(constraint) && !isAnyLike(constraint) {
		c.errorf(diag.CannotMapOverType, n.Span(), "cannot map over type '%s'", constraint.String())
		return types.Any
	}
	return m
}

// resolveIndexedAccess resolves `T[K]`.
//
// 设计者视角：resolveIndexedAccess 是类型级索引——Obj/Index 各解，能算详见实现，排查看 Span。
// 即取（含 errStr 报错），否留延迟节点。障碍是非法索引的报错与延迟之分：可判定
// 即报 IndexedAccessInvalid，泛型未定则留。排查现场：`T[K]` 恒延迟先查 Obj 是否泛型。
func (c *Checker) resolveIndexedAccess(n *ast.IndexedAccessType, sub map[string]types.Type) types.Type {
	obj := c.resolveTypeWithSub(n.Obj, sub)
	index := c.resolveTypeWithSub(n.Index, sub)
	if r, ok, errStr := types.TryEvalIndexed(obj, index, c.opt.toTypes()); ok {
		if errStr != "" {
			c.errorf(diag.IndexedAccessInvalid, n.Span(), "%s", errStr)
			return types.Any
		}
		return r
	}
	return &types.IndexedAccess{Obj: obj, Index: index}
}

// resolveTypeof resolves a `typeof(expr)` query (identifier fast path).
//
// 设计者视角：resolveTypeof 是值到型的“取镜”——Ident 直查值册（无即报），余者
// checkExpr 现算。障碍是前向引用：用点类型（定义前为 Any/窄型）即当时所见。
// 取舍：不记引用打卡（类型查询非值使用）。排查现场：typeof 取到 Any 先查变量是否先用后定。
func (c *Checker) resolveTypeof(t *ast.TypeofType) types.Type {
	if id, ok := t.Expr.(*ast.Ident); ok {
		if s := c.cur().lookupVar(id.Name); s != nil {
			return s.Type
		}
		c.errorf(diag.TypeofUndefinedVariable, t.Span(), "typeof: undefined variable '%s'", id.Name)
		return types.Any
	}
	// 排查看 Span/诊断，如 `local x=1` 错圈整句。
	return c.checkExpr(t.Expr)
}

// resolveKeyof resolves `keyof T` over objects/tuples/arrays/maps/classes.
//
// 设计者视角：resolveKeyof 是键的“点名册”——Object 列字段字面+索引键，Tuple 列
// 数字序，Array 返 Number，Map 返 Key，Class 限 public，空即 Never，any 即 Any，
// 余者报 KeyofUnsupported。障碍是私有成员：类须滤（公才可键）。取舍：字面 Quote
// 包（与窄化 norm 配）。排查现场：keyof 缺键先查字段是否 optional（仍列）或私有。
func (c *Checker) resolveKeyof(e types.Type, sp token.Span) types.Type {
	switch x := e.(type) {
	case *types.Object:
		var ms []types.Type
		for name := range x.Fields {
			ms = append(ms, &types.Literal{Value: strconv.Quote(name), Base: types.String})
		}
		for _, ix := range x.Indexers {
			// 排查看 Span/诊断，如 `local x=1` 错圈整句。
			ms = append(ms, ix.Key)
		}
		if len(ms) == 0 {
			return types.Never
		}
		return types.NewUnion(ms)
	case *types.Tuple:
		var ms []types.Type
		for i := range x.Elems {
			ms = append(ms, &types.Literal{Value: strconv.Itoa(i + 1), Base: types.Number})
		}
		return types.NewUnion(ms)
	case *types.Array:
		return types.Number
	case *types.Map:
		return x.Key
	case *types.Class:
		var ms []types.Type
		for name, f := range x.Fields {
			if f.Access != types.Public {
				continue
			}
			ms = append(ms, &types.Literal{Value: strconv.Quote(name), Base: types.String})
		}
		if len(ms) == 0 {
			return types.Never
		}
		return types.NewUnion(ms)
	default:
		if isAnyLike(e) {
			return types.Any
		}
		c.errorf(diag.KeyofUnsupported, sp, "keyof not supported for '%s'", e.String())
		return types.Any
	}
}

// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：resolveTypeEntry 是旧径的“薄别名”——泛型维度恒空（非泛型登记时调，
// 见 typedecl.go）。sub 参数实未用（历史遗留），统一走 WithArgs。排查现场：
// 泛型 entry 误调此处会丢实参（应调 WithArgs）。
func (c *Checker) resolveTypeEntry(e *typeEntry, sub map[string]types.Type) types.Type {
	return c.resolveTypeEntryWithArgs(e, nil, token.Span{})
}
