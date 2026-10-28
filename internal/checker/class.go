package checker

import (
	"strings"

	"github.com/typelua/typelua/internal/ast"
	"github.com/typelua/typelua/internal/diag"
	"github.com/typelua/typelua/internal/token"
	"github.com/typelua/typelua/internal/types"
)

// 设计者视角：类分两遍——buildClassDef 只算签名（壳 + 继承 + override），
// checkClassBodies 再检查字段初始化与方法体；壳对象先占位支持递归 self 引用。
// 类两遍盖楼：签名壳+继承+override，壳占位撑递归。
//
// 设计者视角：类是“两遍盖楼”——buildClassDef 只算签名（壳+继承+override），
// checkClassBodies 再验初始化与方法体（见 classcheck.go）。障碍是递归自引用与
// 菱形继承：壳详见实现，排查看 Span。
// …（略，详见实现；排查先看 Span/诊断）
// 的 Params 与 Args 对齐。障碍是 ast 与 types 两套表示：此处只抽名，不碰约束/
// 默认（约束在 buildClassDef 另解）。不变量：顺序与声明一致，空即非泛型。
// 排查现场：实例维度错位先查此处与 Args 是否同序。
func classParamNames(gs []*ast.GenericParam) []string {
	var out []string
	for _, g := range gs {
		out = append(out, g.Name.Name)
	}
	return out
}

// 排查看 Span/诊断，如 `local x=1` 错圈整句。
// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：isIdentityClassArgs 是递归自引用的“照镜子”——`next: Box<T>` 在
// Box<T> 构建中出现时，实参恰为自身形参（逐位 GenericParam 同名），应直接绑
// 壳（见 resolveNamed），而非另起实例+patch。障碍是指针 vs 名字：实参经 resolve
// 后是新 GenericParam 拷贝，指针不等，只能比 Name。候选：恒走 patch vs 镜面短路；
// 选短路，省一次回填且指针更稳。不变量：空参与空形参互认（非泛型自引）。排查现场：
// 递归类成员悬空先查此处是否误判非镜面（如 T 被 Substitute 成具体型）。
func isIdentityClassArgs(e *typeEntry, sargs []types.Type) bool {
	if len(sargs) != len(e.params) {
		return len(sargs) == 0 && len(e.params) == 0
	}
	for i, p := range e.params {
		gp, ok := sargs[i].(*types.GenericParam)
		if !ok || gp.Name != p.Name.Name {
			return false
		}
	}
	return true
}

// classSubFromTypes builds the param->arg substitution with arity,
// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：classSubFromTypes 是类实例化的“配料表”——按位配对，缺省走 Default，
// 缺省亦无则填 Unknown，约束不满足即报 TypeArgConstraint。障碍是超量实参：截断
// 前必须先报 TypeArgCount，否则静默吞错。候选：与函数 bindExplicitArgs 合并 vs
// 分设；选分设（类无推断，只有显式+默认）。不变量：返回 map 恒含全部形参键。
// 排查现场：泛型类约束误报先查 Constraint 是否在 tparam 污染下解析（应用定义态）。
func (c *Checker) classSubFromTypes(e *typeEntry, tys []types.Type, span token.Span) map[string]types.Type {
	sub := map[string]types.Type{}
	if len(tys) > len(e.params) {
		c.errorf(diag.TypeArgCount, span, "type '%s' expects %d argument(s), got %d", e.name, len(e.params), len(tys))
	}
	for i, p := range e.params {
		var a types.Type
		if i < len(tys) {
			a = tys[i]
		} else if p.Default != nil {
			a = c.resolveType(p.Default)
		} else {
			a = types.Unknown
		}
		if p.Constraint != nil {
			con := c.resolveType(p.Constraint)
			if !types.IsAssignable(a, con, c.opt.toTypes()) {
				c.errorf(diag.TypeArgConstraint, span, "type argument '%s' does not satisfy constraint '%s'", a.String(), con.String())
			}
		}
		sub[p.Name.Name] = a
	}
	return sub
}

// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：classSubFromNodes 是语法到配料表的“翻译机”——先逐个 resolveType
// 再委托 classSubFromTypes。障碍是 Args 中的前向/递归引用必须在调用方 tparam
// 环境下解，此处不另压栈。取舍：薄封装，错误归于目标 Span。排查现场：extends
// 实参解析错位先查调用方是否已 pushTParams。
func (c *Checker) classSubFromNodes(e *typeEntry, nodes []ast.TypeNode, span token.Span) map[string]types.Type {
	var tys []types.Type
	for _, n := range nodes {
		tys = append(tys, c.resolveType(n))
	}
	return c.classSubFromTypes(e, tys, span)
}

// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：currentClass 是 self/super 门禁的“查岗哨”——栈顶即当前方法所属类，
// 空栈即不在类中。障碍是嵌套函数：闭包内仍见栈顶，故 super 另用 retDepth 限入口层
// （见 classcheck.go）。取舍：只读栈顶，不拷贝。不变量：nil 即顶层/普通函数。
// 排查现场：self 可用性误判先查 classStack 压弹是否配对（方法体入口/出口）。
func (c *Checker) currentClass() *classCtx {
	if len(c.classStack) == 0 {
		return nil
	}
	return c.classStack[len(c.classStack)-1]
}

// 排查看 Span/诊断，如 `local x=1` 错圈整句。
// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：checkAccess 是成员的“门禁闸”——public/无 Owner 恒放行；private 仅
// 同 Def；protected 放行同 Def 与子类链（见 isSubclassOf）。障碍是类外静态访问与
// 实例访问混用：此处只判身份不判调用形式，形式问题另报（如 StaticViaInstance）。
// 取舍：失败即报 AccessViolation 并返 false，调用方以 Any 续流。不变量：owner==nil
// 恒 true。排查现场：越权漏报先查 Owner 是否在 Substitute 后丢失，误报先查 cur
// 是否为嵌套类（以栈顶为准）。
func (c *Checker) checkAccess(acc types.Access, owner *types.ClassDef, sp token.Span, what string) bool {
	if acc == types.Public || owner == nil {
		return true
	}
	cur := c.currentClass()
	if cur == nil {
		c.errorf(diag.AccessViolation, sp, "%s is %s and cannot be accessed outside the class body", what, acc)
		return false
	}
	if acc == types.Private {
		if cur.def == owner {
			return true
		}
		c.errorf(diag.AccessViolation, sp, "%s is private to class '%s'", what, owner.Name)
		return false
	}
	if cur.def == owner || isSubclassOf(cur.inst, owner) {
		return true
	}
	c.errorf(diag.AccessViolation, sp, "%s is protected and cannot be accessed here", what)
	return false
}

// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：isSubclassOf 是族谱的“查家谱”——沿 Base 上溯比 Def 指针。障碍是
// 恶意环继承详见实现，排查看 Span。
// 判环，命中即 false。候选：Name 比 vs Def 比；选 Def（撞名类不可互认）。不变量：
// 纯读，无诊断。排查现场：protected 误拦先查 inst 的 Base 链是否在 shell 回填前
// 被快照（半截链）。
func isSubclassOf(inst *types.Class, owner *types.ClassDef) bool {
	seen := map[*types.Class]bool{}
	for cl := inst; cl != nil; cl = cl.Base {
		if seen[cl] {
			return false
		}
		seen[cl] = true
		if cl.Def == owner {
			return true
		}
	}
	return false
}

// 排查看 Span/诊断，如 `local x=1` 错圈整句。
// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：ensureClassDef 是类的“一次性模具间”——首调建 Def+shell 并调
// buildClassDef，重入详见实现，排查看 Span。
// e.classBuild 缓存保证 once，resolving 守卫防重入，patches 在建成后统一回放
// （非镜面自引用的延迟回填，见 resolveNamed）。候选：eager 全量 vs 懒按需；
// 选懒，类多而用少时省时。不变量：成功后 classBuild 非 nil 且 patches 清空。
// 排查现场：cyclic 误报先查 resolving 是否因早返未复位，成员半截先查 patch 是否丢失。
func (c *Checker) ensureClassDef(e *typeEntry) *classBuild {
	if e.classBuild != nil {
		return e.classBuild
	}
	if e.classDecl == nil || e.resolving {
		return nil
	}
	e.resolving = true
	if e.classDef == nil {
		e.classDef = &types.ClassDef{Name: e.name}
	}
	b := c.buildClassDef(e)
	e.resolving = false
	for _, p := range e.classPatches {
		p()
	}
	e.classPatches = nil
	if b != nil {
		e.classBuild = b
	}
	return b
}

// resolveBaseRef resolves an `extends` clause to an instantiated base
// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：resolveBaseRef 是继承的“提亲”——extends 只认裸类名（拒 a.B 限定），
// 查到 Entry 后 ensureClassDef 取模，再按 Args 实例化出 inst/val 双件。障碍是
// 非类基（alias 到对象）与自环：前者报 ExtendsNotClass，后者留给调用方链 walk
// 定谳，此处不抢报。候选：复用 resolveType vs 手工分流；选手工（要同时拿 inst
// 与 val，resolveType 只给其一）。不变量：失败返 (nil,nil)。排查现场：基类静态
// 丢失先查 val 是否 Substitute 遗漏，基类泛型错乱先查 Args 解析环境。
func (c *Checker) resolveBaseRef(node ast.TypeNode, selfName string) (*types.Class, *types.ClassValue) {
	nt, ok := node.(*ast.NamedType)
	if !ok || strings.Contains(nt.Name, ".") {
		c.errorf(diag.ExtendsNotClass, node.Span(), "extends must name a class (e.g. `extends Animal`)")
		return nil, nil
	}
	e := c.cur().lookupType(nt.Name)
	if e == nil || e.classDecl == nil {
		bt := c.resolveType(node)
		if _, ok := bt.(*types.Class); !ok && !isAnyLike(bt) {
			c.errorf(diag.ExtendsNotClass, node.Span(), "class can only extend another class, got '%s'", bt.String())
		}
		return nil, nil
	}
	def := c.ensureClassDef(e)
	if def == nil {
		c.errorf(diag.CyclicInheritanceRef, node.Span(), "cyclic inheritance involving '%s'", nt.Name)
		return nil, nil
	}
	sub := c.classSubFromNodes(e, nt.Args, node.Span())
	inst, _ := types.Substitute(def.inst, sub).(*types.Class)
	val, _ := types.Substitute(def.val, sub).(*types.ClassValue)
	if inst.Def == e.classDef && e.name == selfName && len(nt.Args) == 0 {
		// 排查看 Span/诊断，如 `local x=1` 错圈整句。
		// 排查看 Span/诊断，如 `local x=1` 错圈整句。
	}
	return inst, val
}

// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：classMemberSig 是一遍的“施工卡”——字段记声明型（nil 表待推断，
// 二遍回填），方法/构造记 *Func。障碍是字段无标注时一遍无型可记：用 nil 哨兵
// 区分“待推”与“any”，二遍按初始化 widen 回填（见 checkClassBodies）。不变量：
// 每首声明恰一卡（重复声明另卡丢弃）。排查现场：字段类型悬空先查二遍是否跳过
// （Declared 文件无体）。
type classMemberSig struct {
	member    *ast.ClassMember
	fieldType types.Type // fields: declared type (nil => infer in pass 2)
	funcType  *types.Func
}

// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：astVisibility 是可见性的“翻译章”——Private/Protected 直译，其余
// （含缺省）归 Public。障碍是零值 Visibility 必须有归宿：归 Public 最宽松，
// 防构造期遗漏把成员误锁。排查现场：成员意外不可见先查 parser 是否透传 Visibility。
func astVisibility(v ast.Visibility) types.Access {
	switch v {
	case ast.Private:
		return types.Private
	case ast.Protected:
		return types.Protected
	}
	return types.Public
}

// 排查看 Span/诊断，如 `local x=1` 错圈整句。
// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：buildClassDef 是一遍的“结构封顶”——按序：备泛型环境→立 shell 占位→
// 查重与保留字→解基类并环检→抄基成员（滤 private）→逐成员算签名→验 override/
// 抽象/Implements→合成 ctor。障碍有三：递归自引（靠 shell+patch）、重复成员
// （首声明生效）、非法修饰（ctor static 等）。图：
//
//	shell(空) → 继承填充 → 自有覆盖 → override 校验 → val/inst 封口
//
// 候选：一遍全包 vs 签名体分离；选分离（体在 checkClassBodies）。不变量：shell
// 指针稳定，原地填充。排查现场：继承成员缺失先查 private 过滤，override 误报
// 先查 IsAssignable 方向（子 <: 基）。
func (c *Checker) buildClassDef(e *typeEntry) *classBuild {
	n := e.classDecl
	def := e.classDef
	c.pushTParams(n.Generics)
	defer c.popTParams()

	shell := &types.Class{Def: def, Name: n.Name.Name, Params: classParamNames(n.Generics), Fields: map[string]types.ClassField{}, IsAbstract: n.Abstract}
	// 排查看 Span/诊断，如 `local x=1` 错圈整句。
	// (`Box.new(v)` inside `static wrap<U>`) tracks instantiations.
	for _, pn := range shell.Params {
		shell.Args = append(shell.Args, &types.GenericParam{Name: pn})
	}
	// Resolved declarations for explicit-argument checking on `new`.
	for _, g := range n.Generics {
		tp := &types.GenericParam{Name: g.Name.Name}
		if g.Constraint != nil {
			tp.Constraint = c.resolveType(g.Constraint)
		}
		if g.Default != nil {
			tp.Default = c.resolveType(g.Default)
		}
		shell.TypeParams = append(shell.TypeParams, tp)
	}
	e.classShell = shell

	// 排查看 Span/诊断，如 `local x=1` 错圈整句。
	seen := map[string]bool{}
	processed := map[string]bool{}
	var ctorMember *ast.ClassMember
	for _, m := range n.Members {
		if seen[m.Name.Name] {
			c.errorf(diag.DuplicateClassMember, m.Span(), "duplicate class member '%s'", m.Name.Name)
			continue
		}
		seen[m.Name.Name] = true
		if m.Kind == ast.ClassConstructor {
			if ctorMember != nil {
				c.errorf(diag.DuplicateConstructor, m.Span(), "duplicate constructor")
				continue
			}
			ctorMember = m
		}
		if m.Name.Name == "new" || m.Name.Name == "__index" {
			c.errorf(diag.InvalidMemberName, m.Name.Span(), "'%s' is reserved by the class runtime", m.Name.Name)
			continue
		}
		if m.Kind == ast.ClassField && m.Name.Name == "constructor" {
			c.errorf(diag.InvalidMemberName, m.Name.Span(), "'constructor' must be declared as a method")
			continue
		}
		if m.Kind == ast.ClassConstructor && m.IsStatic {
			c.errorf(diag.ModifierNotAllowed, m.Span(), "constructor cannot be 'static'")
		}
		if m.Kind == ast.ClassConstructor && m.IsReadonly {
			c.errorf(diag.ModifierNotAllowed, m.Span(), "constructor cannot be 'readonly'")
		}
		if m.Kind == ast.ClassConstructor && m.IsAbstract {
			c.errorf(diag.ModifierNotAllowed, m.Span(), "constructor cannot be 'abstract'")
		}
		if m.Kind == ast.ClassMethod && m.IsReadonly {
			c.errorf(diag.ModifierNotAllowed, m.Span(), "methods cannot be 'readonly'")
		}
		if m.IsAbstract && m.Kind == ast.ClassField {
			// 排查看 Span/诊断，如 `local x=1` 错圈整句。
		} else if m.IsAbstract && !n.Abstract && !n.Declared {
			c.errorf(diag.AbstractMemberInvalid, m.Span(), "abstract member '%s' requires an 'abstract class'", m.Name.Name)
		}
		if m.IsAbstract && m.Visibility == ast.Private {
			c.errorf(diag.AbstractMemberInvalid, m.Span(), "abstract member '%s' cannot be private", m.Name.Name)
		}
		processed[m.Name.Name] = true
	}

	// 排查看 Span/诊断，如 `local x=1` 错圈整句。
	var baseInst *types.Class
	var baseVal *types.ClassValue
	if n.Extends != nil {
		baseInst, baseVal = c.resolveBaseRef(n.Extends, n.Name.Name)
		// 排查看 Span/诊断，如 `local x=1` 错圈整句。
		// 排查看 Span/诊断，如 `local x=1` 错圈整句。
		seenChain := map[*types.Class]bool{shell: true}
		for b := baseInst; b != nil; b = b.Base {
			if seenChain[b] {
				c.errorf(diag.CyclicInheritance, n.Extends.Span(), "cyclic inheritance involving class '%s'", n.Name.Name)
				baseInst, baseVal = nil, nil
				break
			}
			seenChain[b] = true
			if b.Def == def {
				c.errorf(diag.CyclicInheritance, n.Extends.Span(), "class '%s' cannot extend itself", n.Name.Name)
				baseInst, baseVal = nil, nil
				break
			}
		}
	}
	shell.Base = baseInst
	if baseInst != nil {
		for name, f := range baseInst.Fields {
			if f.Access == types.Private {
				continue
			}
			shell.Fields[name] = f
		}
	}
	if baseVal != nil && baseVal.CtorAccess == types.Private {
		c.errorf(diag.ExtendsPrivateCtor, n.Extends.Span(), "cannot extend class '%s' with a private constructor", baseInst.Name)
	}

	statics := map[string]types.ClassField{}
	if baseVal != nil {
		for name, f := range baseVal.Statics {
			if f.Access == types.Private {
				continue
			}
			statics[name] = f
		}
	}

	sigs := map[*ast.ClassMember]*classMemberSig{}
	ownInstance := map[string]types.ClassField{}
	ownStatics := map[string]types.ClassField{}

	memberFunc := func(m *ast.ClassMember) *types.Func {
		c.pushTParams(m.Generics)
		defer c.popTParams()
		var params []types.FuncParam
		for _, p := range m.Params {
			var t types.Type = types.Any
			if p.Type != nil {
				t = c.resolveType(p.Type)
			} else if c.opt.NoImplicitAny {
				c.errorf(diag.ImplicitAnyParam, p.Span(), "parameter '%s' implicitly has 'any' type", p.Name.Name)
			}
			params = append(params, types.FuncParam{Name: p.Name.Name, Type: t})
		}
		var va types.Type
		if m.HasVarArg {
			if m.VarArgType != nil {
				va = c.resolveType(m.VarArgType)
				if arr, ok := va.(*types.Array); ok {
					va = arr.Elem
				}
			} else {
				va = types.Any
			}
		}
		var rets []types.Type
		if m.ReturnType != nil {
			rets = flattenReturns(c.resolveType(m.ReturnType))
			if rets == nil {
				rets = []types.Type{}
			}
		} else {
			rets = []types.Type{types.Any}
		}
		tps := make([]*types.GenericParam, len(m.Generics))
		for i, g := range m.Generics {
			tp := &types.GenericParam{Name: g.Name.Name}
			if g.Constraint != nil {
				tp.Constraint = c.resolveType(g.Constraint)
			}
			if g.Default != nil {
				tp.Default = c.resolveType(g.Default)
			}
			tps[i] = tp
		}
		return &types.Func{Params: params, HasVarArg: m.HasVarArg, VarArg: va, Returns: rets, TypeParams: tps, IsAsync: m.IsAsync}
	}

	for _, m := range n.Members {
		if !processed[m.Name.Name] {
			continue
		}
		// 排查看 Span/诊断，如 `local x=1` 错圈整句。
		if _, done := sigs[m]; done {
			continue
		}
		// 排查看 Span/诊断，如 `local x=1` 错圈整句。
		first := true
		for _, o := range n.Members {
			if o == m {
				break
			}
			if o.Name.Name == m.Name.Name {
				first = false
				break
			}
		}
		if !first {
			continue
		}
		acc := astVisibility(m.Visibility)
		switch m.Kind {
		case ast.ClassField:
			var ft types.Type
			if m.FieldType != nil {
				ft = c.resolveType(m.FieldType)
			}
			cf := types.ClassField{Type: ft, Readonly: m.IsReadonly, Access: acc, Owner: def}
			sigs[m] = &classMemberSig{member: m, fieldType: ft}
			if m.IsStatic {
				statics[m.Name.Name] = cf
				ownStatics[m.Name.Name] = cf
			} else {
				shell.Fields[m.Name.Name] = cf
				ownInstance[m.Name.Name] = cf
			}
		case ast.ClassMethod:
			ft := memberFunc(m)
			cf := types.ClassField{Type: ft, Access: acc, IsMethod: true, Abstract: m.IsAbstract, Owner: def}
			sigs[m] = &classMemberSig{member: m, funcType: ft}
			if m.IsStatic {
				statics[m.Name.Name] = cf
				ownStatics[m.Name.Name] = cf
			} else {
				shell.Fields[m.Name.Name] = cf
				ownInstance[m.Name.Name] = cf
			}
		case ast.ClassConstructor:
			sigs[m] = &classMemberSig{member: m, funcType: memberFunc(m)}
		}
	}

	// 覆盖详见实现，排查看 Span。
	if baseInst != nil {
		for name, sub := range ownInstance {
			if bf, ok := baseInst.Fields[name]; ok && bf.Access != types.Private {
				checkMemberOverride(c, ownMemberSpan(n, name, false), name, sub, bf)
			}
		}
	}
	if baseVal != nil {
		for name, sub := range ownStatics {
			if bf, ok := baseVal.Statics[name]; ok && bf.Access != types.Private {
				checkMemberOverride(c, ownMemberSpan(n, name, true), name, sub, bf)
			}
		}
	}

	// 排查看 Span/诊断，如 `local x=1` 错圈整句。
	// 排查看 Span/诊断，如 `local x=1` 错圈整句。
	// 排查看 Span/诊断，如 `local x=1` 错圈整句。
	if !n.Abstract && !n.Declared && baseInst != nil {
		for name, bf := range baseInst.Fields {
			if !bf.Abstract || bf.Access == types.Private {
				continue
			}
			sub, ok := ownInstance[name]
			if !ok || sub.Abstract {
				c.errorf(diag.UnimplementedAbstract, n.Name.Span(), "class '%s' must implement abstract member '%s'", n.Name.Name, name)
			}
		}
	}

	// 排查看 Span/诊断，如 `local x=1` 错圈整句。
	for _, impl := range n.Implements {
		t := c.resolveType(impl)
		io, ok := t.(*types.Object)
		if !ok {
			if !isAnyLike(t) {
				c.errorf(diag.ImplementsNotSatisfied, impl.Span(), "class can only implement an interface or object type, got '%s'", t.String())
			}
			continue
		}
		if !types.IsAssignable(shell.ObjectView(), io, c.opt.toTypes()) {
			c.errorf(diag.ImplementsNotSatisfied, impl.Span(), "class '%s' does not implement '%s'", n.Name.Name, t.String())
		}
	}

	// Constructor value (always present so `Base.constructor` exists).
	var ctorFunc *types.Func
	ctorAccess := types.Public
	if ctorMember != nil {
		ctorFunc = sigs[ctorMember].funcType
		ctorAccess = astVisibility(ctorMember.Visibility)
		// 构造详见实现，排查看 Span。
		ctorFunc = &types.Func{Params: ctorFunc.Params, HasVarArg: ctorFunc.HasVarArg, VarArg: ctorFunc.VarArg}
	} else if n.Declared {
		ctorFunc = &types.Func{HasVarArg: true, VarArg: types.Any}
	} else if baseVal != nil && baseVal.Constructor != nil {
		ctorFunc = baseVal.Constructor
		ctorAccess = baseVal.CtorAccess
	} else {
		ctorFunc = &types.Func{}
	}

	val := &types.ClassValue{Class: shell, Constructor: ctorFunc, CtorAccess: ctorAccess, Statics: statics, Abstract: n.Abstract}
	return &classBuild{inst: shell, val: val, sigs: sigs}
}

// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：accessRank 把可见性压成可比整数，override 只许“提格”不许“降格”。
// 障碍是 Access 为字符串式枚举，直接比不可序；转 int 后一判即得。不变量：
// Public=2 最大，零值/Private=0 最小。排查现场：放宽误报先查此处映射是否漂移。
func accessRank(a types.Access) int {
	switch a {
	case types.Public:
		return 2
	case types.Protected:
		return 1
	}
	return 0
}

// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：checkMemberOverride 是继承的“验货台”——三关：种类（方法对方法，
// 字段对字段）、类型（子 <: 基，IsAssignable）、可见性（不降格，accessRank 比）。
// 障碍是字段 Type 可能 nil（待推断）：nil 即跳过类型关，留待二遍。候选：严格不变
// vs 协变放行；选 IsAssignable（子可赋基）贴近方法返回协变习惯。不变量：纯校验，
// 不改 shell。排查现场：override 误报先查方向是否写反（sub→base），种类误报先查
// IsMethod 标记。
func checkMemberOverride(c *Checker, sp token.Span, name string, sub, base types.ClassField) {
	kind := "field"
	if sub.IsMethod {
		kind = "method"
	}
	if sub.IsMethod != base.IsMethod {
		c.errorf(diag.OverrideMismatch, sp, "cannot override %s '%s' with a %s", fieldKind(base), name, kind)
		return
	}
	if sub.Type != nil && base.Type != nil && !types.IsAssignable(sub.Type, base.Type, c.opt.toTypes()) {
		c.errorf(diag.OverrideMismatch, sp, "%s '%s' is incompatible with the base declaration", kind, name)
	}
	if accessRank(sub.Access) < accessRank(base.Access) {
		c.errorf(diag.OverrideMismatch, sp, "%s '%s' cannot reduce visibility to %s", kind, name, sub.Access)
	}
}

// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：ownMemberSpan 是诊断的“指路牌”——override 报错要指到子类自有成员
// 而非基类，需按名+静态性回找首声明。障碍是构造器无名成员：直接跳过（Kind 排除），
// 找不到则退守类 Span 保有坐标。取舍：线性扫（成员数小）。不变量：永不返零值
// 恐慌。排查现场：报错行号漂到类名，先查 IsStatic 是否传反。
func ownMemberSpan(n *ast.ClassDecl, name string, isStatic bool) token.Span {
	for _, m := range n.Members {
		if m.Name.Name == name && m.IsStatic == isStatic && m.Kind != ast.ClassConstructor {
			return m.Span()
		}
	}
	return n.Span()
}

// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：fieldKind 是诊断文案的“量词”——override 报错要说清是方法还是字段。
// 单凭 IsMethod 一判即得，无需查类型。排查现场：文案种类写反先查 IsMethod 标记
// 是否在 buildClassDef 置错。
func fieldKind(f types.ClassField) string {
	if f.IsMethod {
		return "method"
	}
	return "field"
}
