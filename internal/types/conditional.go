package types

// Conditional 是 `Check extends Extends ? TrueBranch : FalseBranch`。
//
// 为什么“先保持延迟”：只要 Check 仍含未实例化的类型参数，条件类型就不能求值，
// 否则会把泛型函数的条件结果过早固化。Distributive 镜像 TS：裸类型参数的
// 检查会按联合成员分发（`T extends U ? X : Y` 对 `A|B` 展开为
// `(A extends U?X:Y) | (B extends U?X:Y)`）。
//
// 物理布局：
//
//	├── Check        Type   // 被检查的类型
//	├── Extends      Type   // 约束/模式（可含 infer）
//	├── TrueBranch   Type   // 命中分支
//	├── FalseBranch  Type   // 未命中分支
//	└── Distributive bool   // 是否按联合成员分发
type Conditional struct {
	Check        Type
	Extends      Type
	TrueBranch   Type
	FalseBranch  Type
	Distributive bool
}

// Kind 返回 Conditional 的类型分类标签。
//
// 设计者视角：Conditional 是独立 struct，分类恒定 KConditional；它代表“还没
// 算完的 if”，必须与算完的 Union/Object 区分，否则 assignability 会把它当
// 普通类型比对而非求值后比对。排查现场：无。
func (t *Conditional) Kind() Kind { return KConditional }

// String 把 Conditional 渲染为规范文本。
//
// 设计者视角：打印 `Check extends Extends ? True : False` 还原源码形状，
// 四路都经 typeString 转发 seen，保证嵌套条件与循环对象可终止。文本即缓存键
// 的一部分，Distributive 不打印（分发是求值策略而非类型身份）。排查现场：
// 若长条件链打印爆栈，检查是否绕过 typeString。
func (t *Conditional) String() string { return t.stringSeen(map[Type]bool{}) }

// stringSeen 用 visited 集合渲染 Conditional，四路经 typeString 转发 seen。
//
// 设计者视角：Conditional 自身不入 seen（条件类型不结构自引用），四路转发即
// 可；真正的循环仍由 Object 层折叠。排查现场：若嵌套条件打印爆栈，检查四路
// 是否皆走 typeString。
func (t *Conditional) stringSeen(seen map[Type]bool) string {
	return typeString(t.Check, seen) + " extends " + typeString(t.Extends, seen) + " ? " + typeString(t.TrueBranch, seen) + " : " + typeString(t.FalseBranch, seen)
}

// TryEvalConditional 在 Check 为具体类型时求值 c，否则原样返回 false。
//
// 为什么返回 bool 而非直接求值：让调用方区分“已求值”与“仍延迟”，避免把
// 未实例化的条件类型误当成最终类型；opt 控制 nil 严格性。
//
// 设计者视角：它是条件类型的“闸门”，好比微波炉门没关好就不启动——Check 里
// 还含 GenericParam/InferParam/Ref 就坚决不算，原样退回 (c,false)，
// 调用方详见实现，排查看 Span。
// 把泛型函数的条件结果固化（如 `T extends string ? A : B` 在 T 未定时算成 B）。
// 不变量：返回 true 时 Type 必为求值后类型，false 时必为原 c。排查现场：
// 该算没算，先查 IsConcreteType 是否误判含参；不该算却算了，查守门是否被绕过。
func TryEvalConditional(c *Conditional, opt Options) (Type, bool) {
	if !IsConcreteType(c.Check) {
		return c, false
	}
	return EvalConditional(c.Check, c.Extends, c.TrueBranch, c.FalseBranch, c.Distributive, opt), true
}

// EvalConditional 用已知的具体 check 类型求值条件类型（含分发）。
//
// 为什么拆出 EvalConditional：TryEvalConditional 负责守门（具体性），求值
// 逻辑集中于此，便于 subst/assign 复用；distributive 在此按联合分发。
//
// 设计者视角：它是“已开门后的执行间”：调用前 Check 必具体（调用方保证），
// 这里只管算。Distributive 为真且 Check 为 Union 时，按成员逐个 evalOne 再
// NewUnion 合并——镜像 TS 的裸类型参数分发；非分发则整体一算。不变量：输入
// check 具体，输出必具体（或 NewUnion 范式化）。排查现场：若分发结果少成员，
// 检查 NewUnion 是否合并了重复分支；若非分发误分发，检查 Distributive 标志。
func EvalConditional(check, extends, trueB, falseB Type, distributive bool, opt Options) Type {
	if distributive {
		if u, ok := check.(*Union); ok {
			rs := make([]Type, 0, len(u.Types))
			for _, m := range u.Types {
				rs = append(rs, evalOne(m, extends, trueB, falseB, opt))
			}
			return NewUnion(rs)
		}
	}
	return evalOne(check, extends, trueB, falseB, opt)
}

// evalOne 单次求值：match 成功则代入 bindings 取 TrueBranch，否则取 FalseBranch。
//
// 设计者视角：它是分发的最小步——一次只判一个具体 check，好比分诊单只能看
// 一个病人。bindings 是 infer 在模式中捕获的棋子：无捕获直接返 trueB，有捕获
// 则 Substitute(trueB, bindings) 把棋子落盘。候选：先 Substitute 再 match vs
// 先 match 再 Substitute；选后者，避免无命中时白做替换。排查现场：若 infer
// 分支结果不对，先打印 bindings，再查 Substitute 是否漏键。
func evalOne(check, extends, trueB, falseB Type, opt Options) Type {
	bindings := map[string]Type{}
	if matchInfer(check, extends, bindings, map[visit]bool{}, opt) {
		if len(bindings) == 0 {
			return trueB
		}
		return Substitute(trueB, bindings)
	}
	return falseB
}

// 排查看 Span/诊断，如 `local x=1` 错圈整句。
// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：matchInfer 是“模式猎手”——拿具体 source 去套含 infer 的 pattern
// （如 `T extends { x: infer U }`），命中即把 U 绑到实际类型。障碍有三：
// 递归类型互问死循环（用 seen 共归纳拦）、union 相交的量词方向（源 union 要
// 全员命中、模式 union 只需其一）、冲突绑定的合并（同名 infer 多次命中取
// union）。候选：失败回滚 bindings vs 试探拷贝；对模式 union 用 copyBindings
// 试探，命中才回填，避免污染。取舍：any/unknown 源一律 true（动态类型不拦
// 路），裸 GenericParam 只认同名。排查现场：先看 seen 是否透传，再看 union/
// intersection 的量词方向，最后打印 bindings 看 infer 冲突合并。
func matchInfer(source, pattern Type, bindings map[string]Type, seen map[visit]bool, opt Options) bool {
	if source == nil || pattern == nil {
		return source == nil && pattern == nil
	}
	// `infer U` binds the whole source (joining conflicts as unions).
	if ip, ok := pattern.(*InferParam); ok {
		if prev, ok := bindings[ip.Name]; ok {
			if prev == source {
				return true
			}
			if prev.String() != source.String() {
				bindings[ip.Name] = NewUnion([]Type{prev, source})
			}
			return true
		}
		bindings[ip.Name] = source
		return true
	}
	// 排查看 Span/诊断，如 `local x=1` 错圈整句。
	if pattern.Kind() == KAny {
		return true
	}
	if pattern.Kind() == KUnknown {
		return true
	}
	if source.Kind() == KAny || source.Kind() == KUnknown {
		return true
	}
	if pattern.Kind() == KNever {
		return source.Kind() == KNever
	}
	if source.Kind() == KNever {
		return true
	}
	key := visit{source, pattern}
	if seen[key] {
		return true
	}
	seen[key] = true

	// 排查看 Span/诊断，如 `local x=1` 错圈整句。
	// 排查看 Span/诊断，如 `local x=1` 错圈整句。
	if _, ok := pattern.(*GenericParam); ok {
		// 排查看 Span/诊断，如 `local x=1` 错圈整句。
		// 排查看 Span/诊断，如 `local x=1` 错圈整句。
		if sg, ok := source.(*GenericParam); ok && sg.Name == pattern.(*GenericParam).Name {
			return true
		}
		return source.String() == pattern.String()
	}
	// 排查看 Span/诊断，如 `local x=1` 错圈整句。
	if pc, ok := pattern.(*Conditional); ok {
		if r, ok := TryEvalConditional(pc, opt); ok {
			return matchInfer(source, r, bindings, seen, opt)
		}
		return false
	}
	// 排查看 Span/诊断，如 `local x=1` 错圈整句。
	if pm, ok := pattern.(*Mapped); ok {
		if r, ok := TryEvalMapped(pm, opt); ok {
			return matchInfer(source, r, bindings, seen, opt)
		}
		return false
	}
	if pi, ok := pattern.(*IndexedAccess); ok {
		if r, ok, _ := TryEvalIndexed(pi.Obj, pi.Index, opt); ok {
			return matchInfer(source, r, bindings, seen, opt)
		}
		return false
	}

	// 排查看 Span/诊断，如 `local x=1` 错圈整句。
	if sl, ok := source.(*Literal); ok {
		if pl, ok := pattern.(*Literal); ok {
			return sl.Value == pl.Value
		}
		return matchInfer(sl.Base, pattern, bindings, seen, opt)
	}
	if _, ok := pattern.(*Literal); ok {
		return false
	}

	// 排查看 Span/诊断，如 `local x=1` 错圈整句。
	if isPrim(source) && isPrim(pattern) {
		return primAssignable(source.(*Primitive), pattern.(*Primitive))
	}
	if isPrim(pattern) {
		return false
	}

	// 排查看 Span/诊断，如 `local x=1` 错圈整句。
	if su, ok := source.(*Union); ok {
		for _, m := range su.Types {
			if !matchInfer(m, pattern, bindings, seen, opt) {
				return false
			}
		}
		return true
	}
	if pu, ok := pattern.(*Union); ok {
		for _, m := range pu.Types {
			// 排查看 Span/诊断，如 `local x=1` 错圈整句。
			probe := copyBindings(bindings)
			if matchInfer(source, m, probe, seen, opt) {
				for k, v := range probe {
					bindings[k] = v
				}
				return true
			}
		}
		return false
	}

	// 排查看 Span/诊断，如 `local x=1` 错圈整句。
	if si, ok := source.(*Intersection); ok {
		for _, m := range si.Types {
			if matchInfer(m, pattern, bindings, seen, opt) {
				return true
			}
		}
		return false
	}
	if pi, ok := pattern.(*Intersection); ok {
		for _, m := range pi.Types {
			if !matchInfer(source, m, bindings, seen, opt) {
				return false
			}
		}
		return true
	}

	// 排查看 Span/诊断，如 `local x=1` 错圈整句。
	if pattern.Kind() == KNil || pattern.Kind() == KVoid {
		return source.Kind() == KNil || source.Kind() == KVoid
	}
	if source.Kind() == KNil || source.Kind() == KVoid {
		return false
	}

	// 排查看 Span/诊断，如 `local x=1` 错圈整句。
	if pa, ok := pattern.(*Array); ok {
		switch s := source.(type) {
		case *Array:
			return matchInfer(s.Elem, pa.Elem, bindings, seen, opt)
		case *Tuple:
			for _, e := range s.Elems {
				if !matchInfer(e, pa.Elem, bindings, seen, opt) {
					return false
				}
			}
			return true
		}
		return false
	}

	// Tuples: positional, with trailing `...Rest` matching the remainder.
	if pt, ok := pattern.(*Tuple); ok {
		switch s := source.(type) {
		case *Tuple:
			return matchTupleElems(s.Elems, pt.Elems, bindings, seen, opt)
		case *Array:
			for _, pe := range pt.Elems {
				if isRestElem(pe) {
					continue
				}
				if !matchInfer(s.Elem, restTarget(pe), bindings, seen, opt) {
					return false
				}
			}
			return true
		}
		return false
	}

	// 排查看 Span/诊断，如 `local x=1` 错圈整句。
	if pf, ok := pattern.(*Func); ok {
		sf, ok := source.(*Func)
		if !ok {
			return false
		}
		n := len(pf.Params)
		if len(sf.Params) < n {
			n = len(sf.Params)
		}
		for i := 0; i < n; i++ {
			if !matchInfer(sf.Params[i].Type, pf.Params[i].Type, bindings, seen, opt) {
				return false
			}
		}
		if len(pf.Returns) == 0 {
			return true
		}
		if len(sf.Returns) == 0 {
			if sf.HasVarArg && sf.VarArg != nil {
				for _, pr := range pf.Returns {
					if !matchInfer(sf.VarArg, pr, bindings, seen, opt) {
						return false
					}
				}
				return true
			}
			return everyNil(pf.Returns)
		}
		for i, pr := range pf.Returns {
			var actual Type = Nil
			if i < len(sf.Returns) {
				actual = sf.Returns[i]
			} else if sf.HasVarArg && sf.VarArg != nil {
				actual = sf.VarArg
			}
			if !matchInfer(actual, pr, bindings, seen, opt) {
				return false
			}
		}
		return true
	}

	// 排查看 Span/诊断，如 `local x=1` 错圈整句。
	if po, ok := pattern.(*Object); ok {
		return matchObjectFields(source, po.Fields, bindings, seen, opt)
	}
	if pm, ok := pattern.(*Map); ok {
		sm, ok := source.(*Map)
		if !ok {
			return false
		}
		return matchInfer(sm.Key, pm.Key, bindings, seen, opt) &&
			matchInfer(sm.Value, pm.Value, bindings, seen, opt)
	}
	if _, ok := pattern.(*Class); ok {
		// 排查看 Span/诊断，如 `local x=1` 错圈整句。
		// 排查看 Span/诊断，如 `local x=1` 错圈整句。
		sc, ok := source.(*Class)
		if !ok {
			return false
		}
		return matchClassPattern(sc, pattern.(*Class), bindings, seen, opt)
	}

	// 枚举详见实现，排查看 Span。
	if pe, ok := pattern.(*Enum); ok {
		for _, m := range pe.Members {
			if matchInfer(source, m, bindings, seen, opt) {
				return true
			}
		}
		return false
	}

	if source == pattern {
		return true
	}
	return source.String() == pattern.String()
}

// copyBindings 拷贝 infer 绑定表，供模式 union 的试探匹配。
//
// 设计者视角：模式 `A | B` 要逐个试——试 A 时的 infer 不能污染试 B 的起点，
// 否则 A 的半成品会泄漏。候选：全局回滚 vs 每次拷贝；选拷贝，分支数小而
// 逻辑稳，空表时仍给空 map（避免 nil 写 panic）。排查现场：若第二分支看到
// 第一分支的绑定，检查是否漏调 copyBindings。
func copyBindings(b map[string]Type) map[string]Type {
	if len(b) == 0 {
		return map[string]Type{}
	}
	out := make(map[string]Type, len(b))
	for k, v := range b {
		out[k] = v
	}
	return out
}

// everyNil 判断返回列表是否全为 nil（缺省返回的 Lua 语义守门）。
//
// 设计者视角：Lua 缺返回即补 nil，函数匹配时“源无返回、目标要值”只在目标全
// nil 才放行。障碍是空列表与含 void 的边界：这里只认 KNil，void 另行处理。
// 排查现场：若无返回函数误配，检查目标返回是否含非 nil。
func everyNil(rs []Type) bool {
	for _, r := range rs {
		if r.Kind() != KNil {
			return false
		}
	}
	return true
}

// isRestElem recognizes the `...T` tuple pattern element.
//
// 设计者视角：元组模式的尾巴 `...Rest` 在语法层是 Ref{"...", [T]}，语义层是
// “吃掉剩余全部”。必须用 Name=="..." + 单实参精确识别，避免把普通命名 Ref
// 误当 rest。排查现场：若 rest 未被识别，检查 parser 是否按此形状构造。
func isRestElem(t Type) bool {
	nr, ok := t.(*Ref)
	if !ok {
		return false
	}
	return nr.Name == "..." && len(nr.Args) == 1
}

// restTarget unwraps `...T` (or returns t unchanged). When T is an array
// type (the `...rest: unknown[]` tuple idiom), elements match against its
// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：`...T` 的 T 若是数组（如 `...rest: unknown[]`），比较的是元素
// 类型而非数组本身——镜像函数 vararg 语义。候选：直接比数组 vs 拆元素；选拆
// 元素，否则 `[1,2] vs [...number[]]` 会误判。非 rest 原样返回，保持幂等。
// 排查现场：若 rest 匹配总失败，检查是否漏拆 Array 元素。
func restTarget(t Type) Type {
	if nr, ok := t.(*Ref); ok && nr.Name == "..." && len(nr.Args) == 1 {
		if arr, ok := nr.Args[0].(*Array); ok {
			return arr.Elem
		}
		return nr.Args[0]
	}
	return t
}

// matchTupleElems 按位置匹配元组，尾部 `...Rest` 吞掉剩余全部。
//
// 设计者视角：元组是“按座位对号入座”：前段逐位 matchInfer，遇 rest 即把剩余
// 源全喂给 restTarget。若无 rest，长度必须相等（多一个少一个都不行）。
// 障碍是源/模式任一带 rest 的不对称——这里只处理模式带 rest，源带 rest 在
// 上层已展开。排查现场：若定长误配变长，检查 i==len(src) 与尾长校验。
func matchTupleElems(src, pat []Type, bindings map[string]Type, seen map[visit]bool, opt Options) bool {
	i := 0
	for ; i < len(pat); i++ {
		if isRestElem(pat[i]) {
			for ; i < len(src); i++ {
				if !matchInfer(src[i], restTarget(pat[i]), bindings, seen, opt) {
					return false
				}
			}
			return true
		}
		if i >= len(src) {
			return false
		}
		if !matchInfer(src[i], pat[i], bindings, seen, opt) {
			return false
		}
	}
	return i == len(src)
}

// 排查看 Span/诊断，如 `local x=1` 错圈整句。
// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：对象模式是“查户口”：pattern 的每个必填字段都必须在 source 找到
// 且 matchInfer 通过。source 三态分流——Object 查 Fields/Indexer、Class 只看
// public、Map 要求 string/any 键；可选字段缺失直接跳过。障碍是索引器兜底：
// 无名直接命中时要看索引器 Value。排查现场：若可选误拦，检查 pf.Optional
// 短路；若私有成员透过，检查 Class 分支的 Access 过滤。
func matchObjectFields(source Type, fields map[string]Field, bindings map[string]Type, seen map[visit]bool, opt Options) bool {
	for name, pf := range fields {
		var st Type
		switch s := source.(type) {
		case *Object:
			f, ok := s.Fields[name]
			if !ok {
				covered := false
				for _, ix := range s.Indexers {
					if ix.Key.Kind() == KString || ix.Key.Kind() == KAny {
						st = ix.Value
						covered = true
						break
					}
				}
				if !covered {
					if pf.Optional {
						continue
					}
					return false
				}
			} else {
				st = f.Type
			}
		case *Class:
			f, ok := s.Fields[name]
			if !ok || f.Access != Public {
				if pf.Optional {
					continue
				}
				return false
			}
			st = f.Type
		case *Map:
			if s.Key.Kind() != KString && s.Key.Kind() != KAny {
				if pf.Optional {
					continue
				}
				return false
			}
			st = s.Value
		default:
			if pf.Optional {
				continue
			}
			return false
		}
		if !matchInfer(st, pf.Type, bindings, seen, opt) {
			return false
		}
	}
	return true
}

// 排查看 Span/诊断，如 `local x=1` 错圈整句。
// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：类模式是“认祖归宗”：先沿 source.Base 链找同 Def 祖先（防环用
// visited），找到后再把实参逐位 matchInfer，让 infer 从泛型实参里取数。
// 障碍是同名不同声明（Name 撞衫）必须判 Def 指针，不可只比 Name。候选：结构
// 比 vs 名义比；选名义，否则不同类同形状会串味。排查现场：若子类泛型 infer
// 取不到，检查 Args 长度与 Def 对齐分支。
func matchClassPattern(source, pattern *Class, bindings map[string]Type, seen map[visit]bool, opt Options) bool {
	visited := map[*Class]bool{}
	for c := source; c != nil; c = c.Base {
		if visited[c] {
			return false
		}
		visited[c] = true
		same := c.Def != nil && pattern.Def != nil && c.Def == pattern.Def
		if !same && c.Def == nil && pattern.Def == nil && c.Name != "" && c.Name == pattern.Name {
			same = true
		}
		if !same {
			continue
		}
		if len(c.Args) != len(pattern.Args) {
			return len(c.Args) == 0 && len(pattern.Args) == 0
		}
		for i := range c.Args {
			if !matchInfer(c.Args[i], pattern.Args[i], bindings, seen, opt) {
				return false
			}
		}
		return true
	}
	return false
}
