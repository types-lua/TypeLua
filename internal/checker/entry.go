package checker

import (
	"strconv"
	"strings"

	"github.com/typelua/typelua/internal/ast"
	"github.com/typelua/typelua/internal/diag"
	"github.com/typelua/typelua/internal/token"
	"github.com/typelua/typelua/internal/types"
)

// 设计者视角：类型实例化三段式——先占位（inflight shell），再填充
//（build），最后回填壳保持指针稳定；maxTypeDepth 防止无限展开。

// Type-entry instantiation with memoization and recursive shells.
//
// 设计者视角：实例化是“三段占位法”——泛型按实参键（typesKey）memo，非泛型按 cached，
// 递归先给 inflight 壳（nil 占位→sh 共享），建成后 fillShell 回填保指针稳；
// 自喂养展开（L<L<T>>）靠 maxTypeDepth（64）+depthReported（每 entry 单报）截流。
// 障碍是互递归与无限展：前者壳解，后者深截。图：
//
//	查缓存?中 → inflight?壳 → 占位nil → build → fillShell回填 → 入缓存
//
// 取舍：键为 String 拼（可读），深截放行 Any。排查现场：递归悬空先查壳回填，
// 无限展先查 typeDepth 计数。

// maxTypeDepth caps generic instantiation nesting (see typeDepth).
//
// 设计者视角：maxTypeDepth=64 是自喂养展开的“熔断丝”——正常嵌套远不及此，
// `L<L<T>>` 式无限长键到此即熔（报 TypeRecursionLimit 后返 Any）。与 inflight
// 壳（防环）互补：壳防真环， depth 防假深。取舍：每 entry 只报一次（depthReported）。
// 排查现场：深嵌误熔先查是否自喂养键膨胀。
const maxTypeDepth = 64

// resolveTypeEntryWithArgs instantiates an entry (enum/class/alias/interface) with memo and shells.
//
// 设计者视角：见上三段法——enum（无参，resolving 防环，一次 buildEnum 缓存）、
// class（定义态 once，placeholders 保留，调点 Substitute）、泛型别名/接口（配 sub、
// 验元数/约束、键缓存、inflight 壳）、非泛型（cached+空键壳）。障碍是默认/未知填充
// （缺省走 Default 否则 Unknown）与约束校验位。取舍：错即报、型即 Any 续流。图见
// 文件头。排查现场：实例错先查分支（enum/class/泛/非泛）是否走对。
func (c *Checker) resolveTypeEntryWithArgs(e *typeEntry, args []types.Type, useSpan token.Span) types.Type {
	// Enum: no params.
	if e.enumDecl != nil {
		if e.enumType != nil {
			return e.enumType
		}
		if e.resolving {
			c.errorf(diag.TypeRecursionLimit, useSpan, "cyclic enum '%s'", e.name)
			return types.Any
		}
		e.resolving = true
		et := c.buildEnum(e.enumDecl)
		e.enumType = et
		e.resolving = false
		return et
	}
	// Classes: definition is built once (generic placeholders intact);
	// every reference substitutes arguments (defaults/unknown filled).
	if e.classDecl != nil {
		def := c.ensureClassDef(e)
		if def == nil {
			return types.Any
		}
		if len(e.params) == 0 {
			if len(args) > 0 {
				c.errorf(diag.TypeArgCount, useSpan, "type '%s' takes no type arguments, got %d", e.name, len(args))
			}
			return def.inst
		}
		sub := c.classSubFromTypes(e, args, useSpan)
		if inst, ok := types.Substitute(def.inst, sub).(*types.Class); ok {
			return inst
		}
		return def.inst
	}
	// Build substitution for generic params.
	sub := map[string]types.Type{}
	if len(e.params) > 0 {
		c.typeDepth++
		if c.typeDepth > maxTypeDepth {
			c.typeDepth--
			if !c.depthReported[e] {
				if c.depthReported == nil {
					c.depthReported = map[*typeEntry]bool{}
				}
				c.depthReported[e] = true
				c.errorf(diag.TypeRecursionLimit, useSpan, "type instantiation too deep (possible infinite expansion of '%s')", e.name)
			}
			return types.Any
		}
		defer func() { c.typeDepth-- }()
		if len(args) > len(e.params) {
			c.errorf(diag.TypeArgCount, useSpan, "type '%s' expects %d argument(s), got %d", e.name, len(e.params), len(args))
		}
		for i, p := range e.params {
			var a types.Type
			if i < len(args) {
				a = args[i]
			} else {
				// Default or unknown.
				if p.Default != nil {
					a = c.resolveType(p.Default)
				} else {
					a = types.Unknown
				}
			}
			// Check constraint.
			if p.Constraint != nil {
				con := c.resolveType(p.Constraint)
				if !types.IsAssignable(a, con, c.opt.toTypes()) {
					c.errorf(diag.TypeArgConstraint, useSpan, "type argument '%s' does not satisfy constraint '%s'", a.String(), con.String())
				}
			}
			sub[p.Name.Name] = a
		}
		// Memoize.
		key := typesKey(args)
		if cached, ok := e.cacheInst[key]; ok {
			return cached
		}
		if _, busy := e.inflight[key]; busy {
			// Recursive reference: share an in-progress shell, filled
			// when the outer build completes (mutual recursion included).
			sh := &types.Object{Name: e.name, Fields: map[string]types.Field{}}
			e.inflight[key] = sh
			return sh
		}
		if e.inflight == nil {
			e.inflight = map[string]*types.Object{}
		}
		e.inflight[key] = nil
		e.resolving = true
		var res types.Type
		if e.alias != nil {
			res = c.resolveTypeWithSub(e.alias.Type, sub)
		} else if e.iface != nil {
			res = c.buildInterface(e.iface, sub)
		} else {
			res = types.Any
		}
		e.resolving = false
		res = c.fillShell(e, key, res, useSpan)
		e.cacheInst[key] = res
		return res
	}
	// Non-generic: cache.
	if e.cached != nil {
		return e.cached
	}
	if _, busy := e.inflight[""]; busy {
		sh := &types.Object{Name: e.name, Fields: map[string]types.Field{}}
		e.inflight[""] = sh
		return sh
	}
	if e.inflight == nil {
		e.inflight = map[string]*types.Object{}
	}
	e.inflight[""] = nil
	e.resolving = true
	var res types.Type
	if e.alias != nil {
		res = c.resolveTypeWithSub(e.alias.Type, sub)
	} else if e.iface != nil {
		res = c.buildInterface(e.iface, sub)
	} else {
		res = types.Any
	}
	e.resolving = false
	res = c.fillShell(e, "", res, useSpan)
	e.cached = res
	return res
}

// fillShell completes a recursive build: if a cycle created a shell for key
// during the build, the finished object is copied into it (preserving the
// shared pointer) and the shell is returned. Direct self-aliases
// (`type A = A`) and non-object results keep the TL3106 error.
//
// 设计者视角：fillShell 是壳的“交房”——建中若有人领了壳（inflight[key]=sh），
// 建成即把成品字段/索引器拷入壳并返壳（指针稳，递归引用不断）；无壳即返成品。
// `type A=A`（成品即壳）与非 Object 成品报环（TL3106）。障碍是删键时机：先取后删，
// 防重入误判。取舍：只回填 Object（类另有 patch 系）。排查现场：递归成员空先查
// 壳是否未回填字段。

func (c *Checker) fillShell(e *typeEntry, key string, res types.Type, useSpan token.Span) types.Type {
	sh, ok := e.inflight[key]
	delete(e.inflight, key)
	if !ok || sh == nil {
		return res
	}
	if res == types.Type(sh) {
		c.errorf(diag.TypeRecursionLimit, useSpan, "cyclic type alias '%s'", e.name)
		return res
	}
	robj, ok := res.(*types.Object)
	if !ok {
		c.errorf(diag.TypeRecursionLimit, useSpan, "cyclic type alias '%s'", e.name)
		return res
	}
	for k, v := range robj.Fields {
		sh.Fields[k] = v
	}
	sh.Indexers = robj.Indexers
	return sh
}

// typesKey builds a memo key from instantiation args (String join).
//
// 设计者视角：typesKey 是实例缓存的“货号”——实参 String 以 `|` 拼。障碍是 `|` 碰
// 撞（类型名含 `|` 极罕）：可读优先于完美。候选：指针拼 vs 文本拼；选文本（跨实例
// 同构共享）。排查现场：缓存未命中致重复构建，先查 String 是否稳定（如字面拼写）。
func typesKey(args []types.Type) string {
	ps := make([]string, len(args))
	for i, a := range args {
		ps[i] = a.String()
	}
	return strings.Join(ps, "|")
}

// buildInterface builds an object from an interface decl (extends merge plus fields).
//
// 设计者视角：buildInterface 是接口的“合板”——先合 extends 基（Object 字段+索引器
// 并入，any 跳，非对象报 InterfaceExtendsNonObject），再逐字段解（方法简写已为
// FuncType）并查重（DuplicateField）。sub 下行（泛型实例化）。障碍是菱形 extends
// 重字段：后者覆盖（源序）。取舍：名义 Object 带名（供 ownerEntry 回查）。排查现场：
// 接口缺基字段先查基是否为 Object。
func (c *Checker) buildInterface(n *ast.InterfaceDecl, sub map[string]types.Type) types.Type {
	obj := &types.Object{Name: n.Name.Name, Fields: map[string]types.Field{}}
	// Extends: merge base fields.
	for _, b := range n.Extends {
		bt := c.resolveTypeWithSub(b, sub)
		if bo, ok := bt.(*types.Object); ok {
			for k, v := range bo.Fields {
				obj.Fields[k] = v
			}
			obj.Indexers = append(obj.Indexers, bo.Indexers...)
		} else if isAnyLike(bt) {
			continue
		} else {
			c.errorf(diag.InterfaceExtendsNonObject, b.Span(), "interface can only extend object types, got '%s'", bt.String())
		}
	}
	for _, f := range n.Fields {
		var ft types.Type
		// Method shorthand fields store FuncType in Type already.
		ft = c.resolveTypeWithSub(f.Type, sub)
		if _, exists := obj.Fields[f.Name.Name]; exists {
			c.errorf(diag.DuplicateField, f.Span(), "duplicate field '%s'", f.Name.Name)
		}
		obj.Fields[f.Name.Name] = types.Field{Type: ft, Optional: f.Optional, Readonly: f.Readonly}
	}
	return obj
}

// buildEnum builds an enum type with literal members and duplicate checks.
//
// 设计者视角：buildEnum 是枚举的“铸币”——无值即自增整数（ mixed 串后禁自增），
// 串/数/布/负数字面各铸（数字 `.eEpP` 辨浮整），值串判重（DuplicateEnumValue），
// 名判重，底（underlying）首定、异质并为 union。障碍是混合枚举（串后数字须显值）：
// 报 EnumMemberNeedsValue。取舍：字面值即语义（窄化可用）。图：
//
//	逐成员铸字面 → 值/名查重 → 底合并 → Enum
//
// 排查现场：自增值跳号先查显式整数回填 autoNum，底不对先查混合并 union。
func (c *Checker) buildEnum(n *ast.EnumDecl) *types.Enum {
	members := map[string]types.Type{}
	var underlying types.Type
	autoNum := 0
	autoAllowed := true
	seenVals := map[string]string{} // value string -> member name
	for _, m := range n.Members {
		var mt types.Type
		if m.Value == nil {
			if !autoAllowed {
				c.errorf(diag.EnumMemberNeedsValue, m.Span(), "enum member '%s' must have an explicit value (mixed string enum)", m.Name.Name)
				mt = types.Any
			} else {
				mt = &types.Literal{Value: strconv.Itoa(autoNum), Base: types.Integer}
				autoNum++
			}
		} else {
			switch v := m.Value.(type) {
			case *ast.StringLit:
				mt = &types.Literal{Value: v.Literal, Base: types.String}
				autoAllowed = false
			case *ast.NumberLit:
				// Determine integer vs float.
				base := types.Type(types.Integer)
				if strings.ContainsAny(v.Literal, ".eEpP") {
					base = types.Float
				}
				mt = &types.Literal{Value: v.Literal, Base: base}
				// Update auto counter if integer.
				if iv, err := strconv.Atoi(strings.ReplaceAll(v.Literal, "_", "")); err == nil {
					autoNum = iv + 1
				} else {
					autoAllowed = false
				}
			case *ast.BoolLit:
				mt = &types.Literal{Value: strconv.FormatBool(v.Value), Base: types.Boolean}
				autoAllowed = false
			case *ast.UnOpExpr:
				// Negative number: -N
				if num, ok := v.X.(*ast.NumberLit); ok {
					base := types.Type(types.Integer)
					if strings.ContainsAny(num.Literal, ".eEpP") {
						base = types.Float
					}
					mt = &types.Literal{Value: "-" + num.Literal, Base: base}
				} else {
					c.errorf(diag.EnumMemberNotLiteral, m.Span(), "enum member '%s' must be a literal", m.Name.Name)
					mt = types.Any
				}
			default:
				c.errorf(diag.EnumMemberNotLiteral, m.Span(), "enum member '%s' must be a literal", m.Name.Name)
				mt = types.Any
			}
		}
		if prev, ok := seenVals[mt.String()]; ok {
			c.errorf(diag.DuplicateEnumValue, m.Span(), "duplicate enum value for '%s' (already used by '%s')", m.Name.Name, prev)
		} else {
			seenVals[mt.String()] = m.Name.Name
		}
		if _, ok := members[m.Name.Name]; ok {
			c.errorf(diag.DuplicateEnumMember, m.Span(), "duplicate enum member '%s'", m.Name.Name)
		}
		members[m.Name.Name] = mt
		if lit, ok := mt.(*types.Literal); ok {
			if underlying == nil {
				underlying = lit.Base
			} else if underlying.String() != lit.Base.String() {
				// Mixed: underlying becomes union.
				underlying = types.NewUnion([]types.Type{underlying, lit.Base})
			}
		}
	}
	if underlying == nil {
		underlying = types.Integer
	}
	return &types.Enum{Name: n.Name.Name, Underlying: underlying, Members: members}
}
