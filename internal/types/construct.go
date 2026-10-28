package types

import (
	"sort"
)

// ---------- constructors ----------

// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：Union 是类型系统的"或"，但若每次构造都保留嵌套与重复，子类型
// 判定与缓存键会因同一类型多种文本形式而失效。候选：惰性范式化（每次判定时
// 展平）vs 构造时范式化。选构造时：热路径判定只需 O(1) 指针/字符串比较，
// 代价是每次 NewUnion 多一次 O(n log n) 排序；any/unknown 在此吸收，never 在此
// 去除，空集退化为 never。不变量：输出 Types 已展平、去 never、去重、按
// String() 排序；单成员退化为该成员，空输入为 never。排查现场：若缓存键
// 不稳定或 `A|B` 与 `B|A` 不相等，检查此处的排序与去重，而不是 String()。
func NewUnion(types []Type) Type {
	var flat []Type
	for _, t := range types {
		if t == nil {
			continue
		}
		if u, ok := t.(*Union); ok {
			flat = append(flat, u.Types...)
			continue
		}
		if t.Kind() == KNever {
			continue
		}
		flat = append(flat, t)
	}
	// 排查看 Span/诊断，如 `local x=1` 错圈整句。
	for _, t := range flat {
		if t.Kind() == KAny {
			return Any
		}
	}
	for _, t := range flat {
		if t.Kind() == KUnknown {
			return Unknown
		}
	}
	// 排查看 Span/诊断，如 `local x=1` 错圈整句。
	seen := map[string]bool{}
	var out []Type
	for _, t := range flat {
		s := t.String()
		if !seen[s] {
			seen[s] = true
			out = append(out, t)
		}
	}
	if len(out) == 0 {
		return Never
	}
	if len(out) == 1 {
		return out[0]
	}
	// 排查看 Span/诊断，如 `local x=1` 错圈整句。
	sort.Slice(out, func(i, j int) bool { return out[i].String() < out[j].String() })
	return &Union{Types: out}
}

// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：交集与并集对偶——any 是交集的单位元（any & X = X），never 是
// 吸收元详见实现，排查看 Span。
// 与 Union 的差异：Union 的 any 吸收全部（any | X = any），Intersection 的 any
// 是单位元。候选：保留 any 作成员 vs 去除；选去除，避免 `any & Foo` 打印
// 冗余且破坏范式化。不变量：空输入退化为 unknown（交集的零元），单成员
// 退化为该成员。排查现场：若 `Foo & any` 没有化简为 `Foo`，检查 any 分支。
func NewIntersection(types []Type) Type {
	var flat []Type
	for _, t := range types {
		if t == nil {
			continue
		}
		if u, ok := t.(*Intersection); ok {
			flat = append(flat, u.Types...)
			continue
		}
		if t.Kind() == KAny {
			continue
		}
		if t.Kind() == KNever {
			return Never
		}
		flat = append(flat, t)
	}
	if len(flat) == 0 {
		return Unknown
	}
	seen := map[string]bool{}
	var out []Type
	for _, t := range flat {
		s := t.String()
		if !seen[s] {
			seen[s] = true
			out = append(out, t)
		}
	}
	if len(out) == 1 {
		return out[0]
	}
	sort.Slice(out, func(i, j int) bool { return out[i].String() < out[j].String() })
	return &Intersection{Types: out}
}

// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：nil 的联合成员在类型窄化/函数返回分析中常需剥离（如把
// `Foo | nil` 拆成 `Foo` + "可能为 nil" 标志）。候选：原地修改 Union vs
// 构造新 Union；选构造新 Union详见实现，排查看 Span。
// 单类型非联合：是 nil 则退化为 never（空类型），否则原样返回。返回 bool
// 让调用方知道是否剥离过 nil，避免重复处理。排查现场：若剥离后类型意外
// 变 never，检查输入是否仅含 nil。
func RemoveNil(t Type) (Type, bool) {
	u, ok := t.(*Union)
	if !ok {
		if t.Kind() == KNil {
			return Never, true
		}
		return t, false
	}
	var rest []Type
	found := false
	for _, m := range u.Types {
		if m.Kind() == KNil {
			found = true
			continue
		}
		rest = append(rest, m)
	}
	if !found {
		return t, false
	}
	return NewUnion(rest), true
}

// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：nil 检测必须同时覆盖裸 nil 与联合成员中的 nil，因为 TypeLua
// 把可空类型表示为 `T | nil`。候选：递归遍历 vs 仅查 Union 成员；选后者，
// 因为 nil 只能作为顶层联合成员或裸类型出现（构造器保证范式化，nil 不会
// 嵌套在 Array/Map 内被当作"可空"）。排查现场：若 `Foo | nil` 误报 false，
// 检查 Union 范式化是否把 nil 合并或去重掉了。
func ContainsNil(t Type) bool {
	if t.Kind() == KNil {
		return true
	}
	if u, ok := t.(*Union); ok {
		for _, m := range u.Types {
			if m.Kind() == KNil {
				return true
			}
		}
	}
	return false
}
