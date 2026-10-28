package types

// ---------- subtyping ----------

type Options struct {
	StrictNilChecks bool
}

// visit 记一次共归纳假设（from,to 指针身份）：`type N={next:N}` 回环即命中返 true，省去每步 String() 序列化。
//
// 设计者视角：子类型判定遇到 `type N = { next: N }` 这类递归类型会左右互问、
// 无限下探，必须用“共归纳假设”拦住回头路：第一次见 (from,to) 先记为 true 再
// 往下走，循环回来即命中假设、直接返回。障碍是判等键的代价：旧方案用
// `%p:String` 做 key，每步递归都要完整序列化类型，深链直接把判定拖垮。
// 候选：String 文本 vs 指针身份。选指针身份——Type 全是指针，接口相等即指针
// 相等，map 查找 O(1) 且零分配；代价是结构相同但指针不同的类型 miss 缓存重算
// 一次，但这永远 sound（假设只会把循环判为 true，不会误判为 false）。
// 不变量：seen 只增不减，沿递归向下传递；命中即返回 true。排查现场：若递归
// 类型栈溢出，检查是否绕过 assignable 直接递归；若误判为 true，检查 seen 是否
// 被跨顶层调用复用。
type visit struct {
	from Type
	to   Type
}

// IsAssignable 是对外闸口：`Cat` 能否赋给 `Animal`、实参能否进形参，全汇于此（每次新建假设集隔离）。
//
// 设计者视角：这是全包的对外闸口，好比海关查验——所有赋值、传参、返回、
// 泛型实参校验最终都汇到这里。它只做一件事：新建空假设集后委托 assignable，
// 保证每次顶层判定相互隔离。障碍是递归类型与延迟类型（Conditional/Mapped/
// IndexedAccess）：前者要防无限递归，后者要“能算就算、不能算先放行”。
// 取舍：延迟类型一律 permissive（返回 true），把错误留给实例化后复查，
// 避免在泛型定义处误报。不变量：nil/to/from 的空指针一律 true（缺省即放行）。
// 排查现场：判定意外 true，先看是否命中延迟放行分支；意外 false，先看
// StrictNilChecks 与 union 源/目标的顺序（源 union 必须先于目标 union）。
func IsAssignable(from, to Type, opt Options) bool {
	return assignable(from, to, opt, map[visit]bool{})
}

// assignable 是分诊台：按固定优先级分流（顺序即语义，错一位即误诊 `A|B <: A|B`）。
// 序：fast path（any/never/unknown/nil/void）→ 源 union 全过 → 目标 union 中其一
// → 目标交集全中 → 源交集中其一 → 延迟类型（Conditional/Mapped/IndexedAccess 能算则代入，否则放行 true）。
// 例：`nil` 能否进 `string` 只看 StrictNilChecks；`{next:N}` 回环靠 seen 拦。排查先看 fast path，再看 union 序。
func assignable(from, to Type, opt Options, seen map[visit]bool) bool {
	if from == nil || to == nil {
		return true
	}
	// Fast paths.
	if to.Kind() == KAny {
		return true
	}
	if from.Kind() == KAny {
		return true
	}
	if from.Kind() == KNever {
		return true
	}
	if to.Kind() == KUnknown {
		return true
	}
	if from.Kind() == KUnknown {
		return false
	}
	if to.Kind() == KNever {
		return from.Kind() == KNever
	}
	key := visit{from, to}
	if seen[key] {
		return true
	}
	seen[key] = true

	// Source union: every member must be assignable (must come before target-union
	// fast path, otherwise `A|B <: A|B` fails when checked member-wise).
	if u, ok := from.(*Union); ok {
		for _, m := range u.Types {
			if !assignable(m, to, opt, seen) {
				return false
			}
		}
		return true
	}
	// Target union: from must match one member (nil handling included).
	if u, ok := to.(*Union); ok {
		for _, m := range u.Types {
			if assignable(from, m, opt, seen) {
				return true
			}
		}
		// Special: single-member after filtering? handled above.
		return false
	}
	// Target intersection: must match all.
	if it, ok := to.(*Intersection); ok {
		for _, m := range it.Types {
			if !assignable(from, m, opt, seen) {
				return false
			}
		}
		return true
	}
	// Source intersection: match if any member matches.
	if it, ok := from.(*Intersection); ok {
		for _, m := range it.Types {
			if assignable(m, to, opt, seen) {
				return true
			}
		}
		return false
	}

	// Conditionals with a concrete check evaluate on the spot.
	if fc, ok := from.(*Conditional); ok {
		if r, ok := TryEvalConditional(fc, opt); ok {
			return assignable(r, to, opt, seen)
		}
	}
	if tc, ok := to.(*Conditional); ok {
		if r, ok := TryEvalConditional(tc, opt); ok {
			return assignable(from, r, opt, seen)
		}
	}
	// Still-deferred conditionals live in generic positions that cannot
	// be refuted until instantiation: stay permissive, like Unknown.
	if _, ok := from.(*Conditional); ok {
		return true
	}
	if _, ok := to.(*Conditional); ok {
		return true
	}
	// Deferred mapped and indexed-access types behave the same way.
	if fm, ok := from.(*Mapped); ok {
		if r, ok := TryEvalMapped(fm, opt); ok {
			return assignable(r, to, opt, seen)
		}
		return true
	}
	if tm, ok := to.(*Mapped); ok {
		if r, ok := TryEvalMapped(tm, opt); ok {
			return assignable(from, r, opt, seen)
		}
		return true
	}
	if fi, ok := from.(*IndexedAccess); ok {
		if r, ok, _ := TryEvalIndexed(fi.Obj, fi.Index, opt); ok {
			return assignable(r, to, opt, seen)
		}
		return true
	}
	if ti, ok := to.(*IndexedAccess); ok {
		if r, ok, _ := TryEvalIndexed(ti.Obj, ti.Index, opt); ok {
			return assignable(from, r, opt, seen)
		}
		return true
	}
	// Unbound `infer` placeholders only survive via misuse (reported as
	// TL3601 at the source); stay permissive here.
	if _, ok := from.(*InferParam); ok {
		return to.Kind() == KAny || to.Kind() == KUnknown || from == to || from.String() == to.String()
	}
	if _, ok := to.(*InferParam); ok {
		return true
	}

	// nil/void 格：`nil` 进 `string?` 看 StrictNilChecks，`nil` 进 `void` 恒过（如缺参补 nil）。
	if from.Kind() == KNil {
		if to.Kind() == KNil || to.Kind() == KVoid {
			return true
		}
		if !opt.StrictNilChecks {
			return true
		}
		return false
	}
	if to.Kind() == KNil {
		return from.Kind() == KNil
	}
	if to.Kind() == KVoid {
		return from.Kind() == KVoid || from.Kind() == KNil
	}
	if from.Kind() == KVoid {
		return to.Kind() == KVoid
	}

	// 字面量：`"red"` 进 `string` 靠 Base，进 `enum Color` 须命中成员（如 `"red"` 对 `Color.Red`）。
	if fl, ok := from.(*Literal); ok {
		if tl, ok := to.(*Literal); ok {
			return fl.Value == tl.Value
		}
		// Literal to base.
		if isBaseOf(fl.Base, to) {
			return true
		}
		// Literal string "red" to enum? handled via enum below (to is enum).
		if te, ok := to.(*Enum); ok {
			for _, m := range te.Members {
				if lit, ok := m.(*Literal); ok && lit.Value == fl.Value {
					return true
				}
			}
			return false
		}
		return false
	}
	if _, ok := to.(*Literal); ok {
		// Non-literal to literal: only identical literal matches (handled above).
		return false
	}

	// 原语格：`integer`/`float` 进 `number` 唯二放行，`string` 进 `number` 拦（如 `x: string=123` 报）。
	if isPrim(from) && isPrim(to) {
		return primAssignable(from.(*Primitive), to.(*Primitive))
	}
	// Integer/float literals? Already handled as Literal.

	// 泛型目标：`T extends Animal` 的槽须满足约束（如 `Cat` 进 `T` 先验 `Cat<:Animal`）。
	if gp, ok := to.(*GenericParam); ok {
		if gp.Constraint != nil {
			return assignable(from, gp.Constraint, opt, seen)
		}
		return true
	}
	if fg, ok := from.(*GenericParam); ok {
		// Same parameter is trivially assignable (constraints may print
		// differently per copy, so compare names, not strings).
		if tg, ok := to.(*GenericParam); ok && tg.Name == fg.Name {
			return true
		}
		// A bare type param is assignable where its constraint is.
		if fg.Constraint != nil {
			return assignable(fg.Constraint, to, opt, seen)
		}
		return from == to || from.String() == to.String()
	}

	// 枚举：同名才互通（如 `Color` 进 `Color` 过，进 `string` 须全员可进），`"red"` 进 `Color` 命中成员即过。
	if fe, ok := from.(*Enum); ok {
		if te, ok := to.(*Enum); ok {
			return fe.Name == te.Name
		}
		// Enum to underlying/base: all members must be assignable.
		for _, m := range fe.Members {
			if !assignable(m, to, opt, seen) {
				return false
			}
		}
		return true
	}
	if te, ok := to.(*Enum); ok {
		// Source must be one of the member literals (or the enum itself).
		for _, m := range te.Members {
			if assignable(from, m, opt, seen) {
				return true
			}
		}
		return false
	}

	// 类实例：名义只认族谱（如 `Dog` 进 `Animal` 沿 Base 爬，进 `table` 恒过，结构不足即拦）。
	if fc, ok := from.(*Class); ok {
		switch tt := to.(type) {
		case *Class:
			return classAssignable(fc, tt, opt, seen)
		case *Object:
			return classSatisfiesObject(fc, tt, opt, seen)
		case *Primitive:
			return tt.K == KTable
		}
		return false
	}
	if _, ok := to.(*Class); ok {
		return false
	}
	// 类值（typeof）：同 Def 才互通（如 `typeof(Dog)` 进 `typeof(Dog)` 过，进 `typeof(Cat)` 拦）。
	if fv, ok := from.(*ClassValue); ok {
		if tv, ok := to.(*ClassValue); ok {
			return fv.Class != nil && tv.Class != nil &&
				fv.Class.Def != nil && fv.Class.Def == tv.Class.Def &&
				classArgsAssignable(fv.Class.Args, tv.Class.Args, opt, seen)
		}
		return false
	}
	if _, ok := to.(*ClassValue); ok {
		return false
	}

	// 通道不变：`chan<Cat>` 进 `chan<Animal>` 须双向可赋（如单向通即拦，any 例外）。
	if fc, ok := from.(*Chan); ok {
		if tc, ok := to.(*Chan); ok {
			return assignable(fc.Elem, tc.Elem, opt, seen) && assignable(tc.Elem, fc.Elem, opt, seen)
		}
		return false
	}
	if _, ok := to.(*Chan); ok {
		return false
	}

	// 数组：`integer[]` 进 `table` 直放，进 `map<integer,T>` 按位验，进对象看 objectAcceptsArray。
	if fa, ok := from.(*Array); ok {
		switch tt := to.(type) {
		case *Array:
			return assignable(fa.Elem, tt.Elem, opt, seen)
		case *Primitive:
			return tt.K == KTable
		case *Object:
			return objectAcceptsArray(tt, fa, opt, seen)
		case *Map:
			// 数组当 map<integer, elem> 用（如 `{1,2,3}` 进 `map<integer,string>` 按位校验）。
			return assignable(Integer, tt.Key, opt, seen) && assignable(fa.Elem, tt.Value, opt, seen)
		}
		return false
	}
	if ta, ok := to.(*Array); ok {
		if fa, ok := from.(*Tuple); ok {
			for _, e := range fa.Elems {
				if !assignable(e, ta.Elem, opt, seen) {
					return false
				}
			}
			return true
		}
		if fo, ok := from.(*Object); ok {
			// `{}` is a valid empty array at runtime.
			return len(fo.Fields) == 0 && len(fo.Indexers) == 0
		}
		return false
	}

	// 映射：`map<string,integer>` 进 `table` 直放，进对象须索引器覆盖（如全可选才松）。
	if fm, ok := from.(*Map); ok {
		switch tt := to.(type) {
		case *Map:
			return assignable(fm.Key, tt.Key, opt, seen) && assignable(fm.Value, tt.Value, opt, seen)
		case *Primitive:
			return tt.K == KTable
		case *Object:
			// map to object: object indexers/fields must be satisfied by map's value?
			// Conservative: only if object has compatible indexer.
			for _, ix := range tt.Indexers {
				if !assignable(fm.Key, ix.Key, opt, seen) || !assignable(fm.Value, ix.Value, opt, seen) {
					return false
				}
			}
			for _, f := range tt.Fields {
				if f.Optional {
					continue
				}
				// Required field must be covered by map value? Need key compat: skip strict check.
				_ = f
			}
			return len(tt.Indexers) > 0 || len(tt.Fields) == 0
		}
		return false
	}

	// 元组：`[integer,string]` 定长逐位验，进数组按 elem 并验（如 `[1,"a"]` 进 `any[]`）。
	if ft, ok := from.(*Tuple); ok {
		switch tt := to.(type) {
		case *Tuple:
			if len(ft.Elems) != len(tt.Elems) {
				return false
			}
			for i := range ft.Elems {
				if !assignable(ft.Elems[i], tt.Elems[i], opt, seen) {
					return false
				}
			}
			return true
		case *Array:
			for _, e := range ft.Elems {
				if !assignable(e, tt.Elem, opt, seen) {
					return false
				}
			}
			return true
		case *Primitive:
			return tt.K == KTable
		case *Object:
			return objectAcceptsArray(tt, &Array{Elem: unionOf(ft.Elems)}, opt, seen)
		}
		return false
	}
	if _, ok := to.(*Tuple); ok {
		return false
	}

	// 函数：`(x: Cat) => void` 进 `(x: Animal) => void` 靠参数逆变，进 `function` 原语直放。
	if ff, ok := from.(*Func); ok {
		switch tt := to.(type) {
		case *Func:
			return funcAssignable(ff, tt, opt, seen)
		case *Primitive:
			return tt.K == KFunction
		}
		return false
	}
	if _, ok := to.(*Func); ok {
		return false
	}

	// 对象：`{a=1}` 进 `{a: number}` 逐字段验，进 `map<string,number>` 须全员可进 Value。
	if fo, ok := from.(*Object); ok {
		switch tt := to.(type) {
		case *Object:
			return objectAssignable(fo, tt, opt, seen)
		case *Primitive:
			return tt.K == KTable
		case *Map:
			// object to map: all fields must fit value type, keys fit key type.
			strKey := String
			// If map key is number/integer, object with only array-like? conservative false.
			for name, f := range fo.Fields {
				_ = name
				if !assignable(f.Type, tt.Value, opt, seen) {
					return false
				}
				if !assignable(strKey, tt.Key, opt, seen) {
					// Allow string|number keys etc.
					if !assignable(strKey, tt.Key, opt, seen) {
						return false
					}
				}
			}
			return true
		}
		return false
	}
	if _, ok := to.(*Object); ok {
		return false
	}

	// Ref 正常已被消解（如泛型占位残留）：按名+实参逐位比（如 `Box<T>` 同名不同参即拦）。
	if fr, ok := from.(*Ref); ok {
		if tr, ok := to.(*Ref); ok {
			if fr.Name != tr.Name || len(fr.Args) != len(tr.Args) {
				return false
			}
			for i := range fr.Args {
				if !assignable(fr.Args[i], tr.Args[i], opt, seen) {
					return false
				}
			}
			return true
		}
		return false
	}

	if from == to {
		return true
	}
	return from.String() == to.String()
}

// isPrim 判原语（*Primitive 才真）：` "red"` 字面量外形像 string 但不是原语，须拦在 primAssignable 外。
//
// 设计者视角：原语判定每天被调用数万次，必须是 O(1) 类型断言，不能走
// Kind switch 或 String()。障碍是 Literal/Enum 外形像原语但不是原语，
// 必须用类型断言精确拦截。排查现场：若字面量误入 primAssignable，检查此处。
func isPrim(t Type) bool {
	_, ok := t.(*Primitive)
	return ok
}

// isBaseOf 判断 base 是否为 to 的基类（字面量 <: 基类规则的内核）。
//
// 设计者视角：字面量 `"red"` 能赋给 string，本质是“字面量的 Base 可赋给目标”。
// 这里复用 assignable 但强制 StrictNilChecks=true，避免宽松 nil 检查污染
// 字面量判定。候选：直接 Kind 相等 vs 递归 assignable；选后者，以便 integer
// 字面量经 Base 走 number 格（integer <: number）。排查现场：若字面量该放行
// 却被拒，检查 Base 是否为 nil 或 Kind 是否对齐。
func isBaseOf(base, to Type) bool {
	if base == nil {
		return false
	}
	return assignable(base, to, Options{StrictNilChecks: true}, map[visit]bool{})
}

// primAssignable 判定原语间的可赋值性（含 integer/float <: number 格）。
//
// 设计者视角：原语格很小但很硬：同 Kind 必通；integer/float 到 number 是唯二
// 的跨格放行（Lua 数字语义），其余一律 false。障碍是 any/unknown/never 已在
// fast path 拦截，这里只剩“硬原语”，不能再放宽，否则 `string <: number` 会
// 漏网。排查现场：若数字格误判，检查 KInteger/KFloat/KNumber 三分支。
func primAssignable(from, to *Primitive) bool {
	if from.K == to.K {
		return true
	}
	// integer/float <: number
	if (from.K == KInteger || from.K == KFloat) && to.K == KNumber {
		return true
	}
	return false
}

// classArgsAssignable 判泛型实参协变：`Box<Cat>` 能否进 `Box<Animal>` 看 Cat<:Animal，长度先对齐。
//
// 设计者视角：泛型类实参取协变（`Box<Cat> <: Box<Animal>` 当 Cat <: Animal），
// 因为 TypeLua 的类字段多为只读/方法返回协变可 sound；不变只留给 Chan。
// 障碍是实参数量不一致（不同实例化维度）必须先判长度，否则逐位比较越界。
// 候选：协变 vs 不变；选协变，让子类实例能流入父类泛型槽。排查现场：若泛型类
// 误判，检查 Args 长度与 Def 身份是否先对齐（见 classAssignable）。
func classArgsAssignable(from, to []Type, opt Options, seen map[visit]bool) bool {
	if len(from) != len(to) {
		return false
	}
	for i := range from {
		if !assignable(from[i], to[i], opt, seen) {
			return false
		}
	}
	return true
}

// classAssignable 只认族谱不认长相：`class C extends B` 的实例沿 Base 链上爬，撞同 Def 再比 Args 协变。
//
// 设计者视角：名义子类型只认“族谱”不认“长相”：结构再像也不是一家人，族谱
// 对上才是。障碍是菱形/环形继承会导致 Base 链成环，朴素 while 会打转；
// 故用 visited 按 *Class 指针判环。候选：按 Name 比 vs 按 Def 指针比；
// 选 Def 指针（Name 会撞衫，不同包同名类不可互换），Name 仅作 Def==nil 的
// 降级。找到同 Def 祖先后再比 Args 协变。排查现场：若子类实例赋给父类失败，
// 检查 Def 指针是否同一声明、Args 协变是否通过。
func classAssignable(from, to *Class, opt Options, seen map[visit]bool) bool {
	visited := map[*Class]bool{}
	for c := from; c != nil; c = c.Base {
		if visited[c] {
			return false
		}
		visited[c] = true
		if c.Def != nil && to.Def != nil && c.Def == to.Def {
			return classArgsAssignable(c.Args, to.Args, opt, seen)
		}
		if c.Def == nil && to.Def == nil && c.Name != "" && c.Name == to.Name {
			return classArgsAssignable(c.Args, to.Args, opt, seen)
		}
	}
	return false
}

// classObjectView 脱制服看身材：把 Class 的 public 成员快照为 Object，复用结构判定（如 private 不 leaking）。
//
// 设计者视角：类与接口相遇时要“脱下制服看身材”：把 Class 的 public 成员
// 快照为 *Object，再复用 objectAssignable 的结构化逻辑，避免为 Class-vs-Object
// 另写一套字段遍历。障碍是 private/protected 不能 leaking 到接口，
// 必须在此过滤；Field 的 Owner 统一置 public 视图。取舍：每次现做快照
// （小 map 分配）换判定逻辑单源。排查现场：若私有成员误参与接口检查，
// 检查此处的 Access 过滤。
func classObjectView(c *Class) *Object {
	o := &Object{Fields: map[string]Field{}}
	for n, f := range c.Fields {
		if f.Access != Public {
			continue
		}
		o.Fields[n] = Field{Type: f.Type, Optional: f.Optional, Readonly: f.Readonly, Access: Public}
	}
	return o
}

// classSatisfiesObject 是摆渡口：`class C` 能否赋给 `interface I{foo(): void}` 只看 public 结构，不看 Def。
//
// 设计者视角：这是名义与结构两世界的摆渡口：`class C` 能否赋给 `interface I`
// 只看 public 成员结构，不看 Def 身份。直接委托 classObjectView+objectAssignable，
// 让接口一致性只有一套实现。排查现场：若类明明有方法却判不兼容，检查方法
// 是否被标为 private 或 ObjectView 快照丢失。
func classSatisfiesObject(from *Class, to *Object, opt Options, seen map[visit]bool) bool {
	return objectAssignable(classObjectView(from), to, opt, seen)
}

// unionOf 把多返回并成 Union：`f(): (integer, string)` 缺位补 nil，空切片退化为 Never 防非法空并。
//
// 设计者视角：很多位置要“把一堆类型并起来”：元组转数组、函数多返回补 nil。
// 直接调 NewUnion 即得范式化（展平/去重/排序），空切片退化为 Never 而非空
// Union，避免产生“零成员并集”这种非法值。排查现场：若空元组转出非 Never，
// 检查此处 len==0 分支。
func unionOf(elems []Type) Type {
	if len(elems) == 0 {
		return Never
	}
	return NewUnion(elems)
}

// objectAcceptsArray 判数组能否进对象：`{}` 空对空即过，`{[1]: T}` 须目标无必填且索引器吞下 integer->elem。
//
// 设计者视角：Lua 里 `{}` 既是空对象也是空数组，`{[1]: T}` 又可兼作数组视图，
// 所以数组到对象的判定必须宽容：目标若无必填字段、且索引器接受 integer->elem，
// 即放行。障碍是“必填字段”与“索引器缺失”要分先后：先拦必填，再看索引器，
// 最后空对空才 true。排查现场：若数组误入对象失败，检查目标是否有隐藏必填
// 字段或索引器 Key 是否不接受 Integer。
func objectAcceptsArray(obj *Object, arr *Array, opt Options, seen map[visit]bool) bool {
	// Array satisfies object if object has no required named fields and indexer accepts integer->elem.
	for _, f := range obj.Fields {
		if !f.Optional {
			return false
		}
	}
	if len(obj.Indexers) == 0 {
		return len(obj.Fields) == 0
	}
	for _, ix := range obj.Indexers {
		if !assignable(Integer, ix.Key, opt, seen) {
			return false
		}
		if !assignable(arr.Elem, ix.Value, opt, seen) {
			return false
		}
	}
	return true
}

// objectAssignable 宽进严出：源多字段无妨（如 `{a=1,b=2}` 进 `{a: number}`），目标必填缺一即拦。
//
// 设计者视角：对象是“宽进严出”的结构类型：源多字段无妨，目标必填缺一不可。
// 障碍有三：空表字面量 `{}` 的特殊放行、源索引器对目标字段的覆盖、目标索引器
// 被源字段“众筹”满足。候选：严格字段一一对应 vs 宽松子集；选宽松子集，
// 贴近 TS/Lua 的结构化习惯。不变量：空源只看目标是否全可选；索引器不满足
// 即 false。排查现场：先看必填字段缺谁，再看索引器 Key/Value 双向是否皆通。
func objectAssignable(from, to *Object, opt Options, seen map[visit]bool) bool {
	// An empty table literal satisfies anything without required fields.
	if len(from.Fields) == 0 && len(from.Indexers) == 0 {
		for _, tf := range to.Fields {
			if !tf.Optional {
				return false
			}
		}
		return true
	}
	// Every required field in `to` must exist compatibly in `from`.
	for name, tf := range to.Fields {
		ff, ok := from.Fields[name]
		if !ok {
			// Try indexers of from.
			covered := false
			for _, ix := range from.Indexers {
				if assignable(String, ix.Key, opt, seen) && assignable(ix.Value, tf.Type, opt, seen) {
					covered = true
					break
				}
			}
			if tf.Optional {
				continue
			}
			if covered {
				continue
			}
			return false
		}
		if !assignable(ff.Type, tf.Type, opt, seen) {
			return false
		}
	}
	// Every indexer in `to` must be satisfied.
	for _, tix := range to.Indexers {
		satisfied := false
		for _, fix := range from.Indexers {
			if assignable(fix.Key, tix.Key, opt, seen) && assignable(fix.Value, tix.Value, opt, seen) {
				satisfied = true
				break
			}
		}
		if satisfied {
			continue
		}
		// Named fields of from can satisfy a string indexer if all fit.
		if tix.Key.Kind() == KString || tix.Key.Kind() == KAny {
			allFit := len(from.Fields) > 0
			for _, f := range from.Fields {
				if !assignable(f.Type, tix.Value, opt, seen) {
					allFit = false
					break
				}
			}
			if allFit {
				continue
			}
		}
		return false
	}
	return true
}

// funcAssignable 判函数：参数逆变（传的须被接住）、返回协变，多传丢弃、少传补 nil（如 `f(a,b)` 调单参函数）。
//
// 设计者视角：函数是“最挑剔又最随和”的类型：参数要逆变（调用方传的必须被接住）、
// 返回要协变（我给的必须够用），但 Lua 多传少传都不炸——多余实参静默丢弃，
// 缺失形参补 nil。障碍是 vararg 与可选参数把元数判定扯成四象限：源/目标各有
// 无 vararg。取舍：元数只拦“源必填而目标给不出”，其余放行；重叠位置逐位逆变，
// 超出部分按 vararg 补。返回缺省按 nil 补（Lua 语义）。排查现场：若函数误判，
// 先数必填（reqFrom/reqTo），再看重叠参数逆变，最后看返回协变。
func funcAssignable(from, to *Func, opt Options, seen map[visit]bool) bool {
	// Arity: source must accept at least as many args as target requires.
	// Count required target params.
	reqTo := 0
	for _, p := range to.Params {
		if !p.Optional {
			reqTo++
		}
	}
	maxFrom := len(from.Params)
	if !from.HasVarArg && len(to.Params) > maxFrom {
		// Target may pass more args than source accepts: check extras are optional? No, source fixed.
		// Allow extra target params only if source has vararg (handled) else fail when target passes more.
		// Actually assignability: target callable with N args must be callable on source.
		// If target has more params (even optional), source must accept them or have vararg.
		if len(to.Params) > len(from.Params) {
			return false
		}
	}
	if len(from.Params) < reqTo && !from.HasVarArg {
		// Source requires more? Wait: source params < target required => source can't handle target's required args? No.
		// Example: source (a: number) target (a: number, b: string): target caller passes 2 args, source takes 1 -> extra ignored in Lua! Lua allows extra args.
		// In Lua, extra args are silently dropped. So arity is flexible: fewer source params is OK.
		// Only fail if source requires more args than target guarantees.
	}
	reqFrom := 0
	for _, p := range from.Params {
		if !p.Optional {
			reqFrom++
		}
	}
	// Target must supply at least reqFrom args (its required count, or its total if no vararg?).
	// If target has vararg, it can supply arbitrarily many.
	maxTo := len(to.Params)
	if !to.HasVarArg && reqFrom > maxTo {
		return false
	}
	// Contravariant params for overlapping positions.
	n := len(to.Params)
	if len(from.Params) < n {
		n = len(from.Params)
	}
	for i := 0; i < n; i++ {
		// to.Param <: from.Param
		if !assignable(to.Params[i].Type, from.Params[i].Type, opt, seen) {
			return false
		}
	}
	// If target has extra params beyond source, source must have vararg compatible.
	if len(to.Params) > len(from.Params) {
		if !from.HasVarArg {
			// Lua drops extras, so OK as long as extras are optional? Actually caller through target type passes them, source ignores. Safe.
			// So OK.
		} else {
			for i := len(from.Params); i < len(to.Params); i++ {
				if from.VarArg == nil {
					continue
				}
				if !assignable(to.Params[i].Type, from.VarArg, opt, seen) {
					return false
				}
			}
		}
	}
	// If source has extra required params beyond target, target caller won't supply them -> fail unless source extras optional/vararg-fillable.
	if len(from.Params) > len(to.Params) {
		for i := len(to.Params); i < len(from.Params); i++ {
			if !from.Params[i].Optional {
				if !to.HasVarArg {
					return false
				}
				if to.VarArg == nil {
					return false
				}
				if !assignable(to.VarArg, from.Params[i].Type, opt, seen) {
					return false
				}
			}
		}
	}
	// Covariant returns: source returns must cover target returns.
	if len(to.Returns) == 0 {
		return true
	}
	if len(from.Returns) == 0 {
		// Source returns void but target expects values: only OK if target expects void/nil? Already len>0, so fail unless target allows nil?
		// void ~ nil: treat missing as nil.
		for _, tr := range to.Returns {
			if !assignable(Nil, tr, opt, seen) {
				return false
			}
		}
		return true
	}
	for i, tr := range to.Returns {
		var fr Type = Nil // missing extras are nil in Lua.
		if i < len(from.Returns) {
			fr = from.Returns[i]
		} else if from.HasVarArg && from.VarArg != nil {
			fr = from.VarArg
		}
		if !assignable(fr, tr, opt, seen) {
			return false
		}
	}
	return true
}
