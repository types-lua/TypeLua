package types

import (
	"strconv"
)

// Mapped 是映射类型 `{ [K in Keys]?: V }`。
//
// 为什么“具体域急切展开、泛型域延迟”：具体域可枚举为对象字段，延迟域无法
// 枚举，只能保留 Mapped 形状供后续实例化；这与 TS 的映射类型求值策略一致。
//
// Homomorphic（非 nil）是 `keyof` 基对象：其字段修饰符会被继承，除非被显式
// `?`/`-?`/`readonly`/`-readonly` 覆盖；nil 表示绝对映射（仅标记决定）。
// …（略，详见实现；排查先看 Span/诊断）
//
//	├── RoMinus     bool    // `-readonly`
//	├── Homomorphic Type    // keyof 基对象（nil=绝对）
//	└── Value       Type    // 值类型模板（含 K）
type Mapped struct {
	Param       string
	Constraint  Type
	Optional    bool // `?` or `+?`
	OptMinus    bool // `-?`
	Readonly    bool // `readonly` or `+readonly`
	RoMinus     bool // `-readonly`
	Homomorphic Type
	Value       Type
}

// Kind 返回 Mapped 的类型分类标签。
//
// 设计者视角：Mapped 是独立 struct，分类恒定 KMapped；映射类型形状必须保留
// 直到域具体，不能与 Object 混淆。排查现场：无。
func (t *Mapped) Kind() Kind { return KMapped }

// String 把 Mapped 渲染为规范文本。
//
// 设计者视角：打印 `{ readonly? [K in Domain]?: V }` 还原源码形状，修饰符按
// 优先级输出（-? 优先于 ?）；便于诊断与缓存键。排查现场：若修饰符打印
// 错误，检查 Optional/OptMinus/Readonly/RoMinus 分支。
func (t *Mapped) String() string { return t.stringSeen(map[Type]bool{}) }

// stringSeen 渲染 Mapped，域/值/homomorphic 经 typeString 转发 seen。
//
// 设计者视角：Mapped 的 Constraint/Value/Homomorphic 可含循环对象，必须转发
// seen；Mapped 自身不入 seen。排查现场：若深层类型折叠，检查 seen 传递。
func (t *Mapped) stringSeen(seen map[Type]bool) string {
	q := ""
	if t.OptMinus {
		q = "-?"
	} else if t.Optional {
		q = "?"
	}
	ro := ""
	if t.RoMinus {
		ro = "-readonly "
	} else if t.Readonly {
		ro = "readonly "
	}
	return "{ " + ro + "[" + t.Param + " in " + typeString(t.Constraint, seen) + "]" + q + ": " + typeString(t.Value, seen) + " }"
}

// IndexedAccess 是索引访问类型 `T[K]`，双方都具体时求值。
//
// 为什么保留为类型而非立即求值：泛型上下文里 T/K 未实例化，无法求值；
// 保持形状让 subst/assign 在实例化后再求值，避免过早丢失信息。
//
// 物理布局：
//
//	├── Obj   Type   // 被索引的对象/数组/元组/映射/类/枚举
//	└── Index Type   // 索引键
type IndexedAccess struct {
	Obj   Type
	Index Type
}

// Kind 返回 IndexedAccess 的类型分类标签。
//
// 设计者视角：IndexedAccess 是独立 struct，分类恒定 KIndexedAccess；它必须保留
// 形状直到双方具体，不能提前求值丢失信息。排查现场：无。
func (t *IndexedAccess) Kind() Kind { return KIndexedAccess }

// String 把 IndexedAccess 渲染为规范文本。
//
// 设计者视角：打印 `Obj[Index]` 还原源码形状；Obj/Index 经 typeString 转发
// seen。排查现场：若打印丢失嵌套，检查 seen 传递。
func (t *IndexedAccess) String() string { return t.stringSeen(map[Type]bool{}) }

// stringSeen 渲染 IndexedAccess，双方经 typeString 转发 seen。
//
// 设计者视角：双方可能含循环对象，必须转发 seen。排查现场：同上。
func (t *IndexedAccess) stringSeen(seen map[Type]bool) string {
	return typeString(t.Obj, seen) + "[" + typeString(t.Index, seen) + "]"
}

// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：索引访问/映射求值需要把字面量 `"foo"` / `'bar'` 转为字段名；
// 仅 Literal 可转换，其它类型返回 false。候选：字符串化后再 strip 引号 vs
// 直接检查引号；选后者，避免对非字面量类型误转。排查现场：若字段名含引号
// 未剥离，检查引号判断分支。
func literalKeyName(t Type) (string, bool) {
	lit, ok := t.(*Literal)
	if !ok {
		return "", false
	}
	v := lit.Value
	if len(v) >= 2 && ((v[0] == '"' && v[len(v)-1] == '"') || (v[0] == '\'' && v[len(v)-1] == '\'')) {
		return v[1 : len(v)-1], true
	}
	return v, true
}

// 排查看 Span/诊断，如 `local x=1` 错圈整句。
// 返回，排查先看 Span/诊断。
//
// 设计者视角：映射类型 `{ [K in Domain]: V }` 只有在 Domain 具体时才能展开；
// 域为 string/number/integer 原语时退化为索引签名，为字面量联合时展开为
// 字段。候选：急切展开所有映射 vs 仅具体域；选仅具体域，泛型域保留 Mapped
// 形状供后续实例化。Homomorphic 基对象用于继承修饰符如 `keyof T`，排查看 Span。
// 返回的 Object 字段名来自字面量键；修饰符继承规则见代码。排查现场：若
// 映射展开字段缺失/修饰符错误，检查 base 与 Optional/OptMinus 分支。
func evalMapped(m *Mapped, opt Options) (Type, bool) {
	if !IsConcreteType(m.Constraint) {
		return m, false
	}
	// Index-signature form: `{[K in string]: V}`.
	if prim, ok := m.Constraint.(*Primitive); ok &&
		(prim.K == KString || prim.K == KNumber || prim.K == KInteger) {
		return &Object{Fields: map[string]Field{}, Indexers: []Indexer{{
			Key:   prim,
			Value: Substitute(m.Value, map[string]Type{m.Param: prim}),
		}}}, true
	}
	// 排查看 Span/诊断，如 `local x=1` 错圈整句。
	keys := unionMembers(m.Constraint)
	var lits []Type
	for _, k := range keys {
		if _, ok := k.(*Literal); !ok {
			return m, false
		}
		lits = append(lits, k)
	}
	if len(lits) == 0 {
		return &Object{Fields: map[string]Field{}}, true
	}
	fields := map[string]Field{}
	// Homomorphic base for modifier inheritance (`keyof` object).
	var base *Object
	if m.Homomorphic != nil {
		if o, ok := m.Homomorphic.(*Object); ok {
			base = o
		}
	}
	for _, k := range lits {
		name, _ := literalKeyName(k)
		opt, ro := m.Optional, m.Readonly
		if m.OptMinus {
			opt = false
		} else if !m.Optional && base != nil {
			if f, ok := base.Fields[name]; ok && f.Optional {
				opt = true
			}
		}
		if m.RoMinus {
			ro = false
		} else if !m.Readonly && base != nil {
			if f, ok := base.Fields[name]; ok && f.Readonly {
				ro = true
			}
		}
		fields[name] = Field{
			Type:     Substitute(m.Value, map[string]Type{m.Param: k}),
			Optional: opt,
			Readonly: ro,
		}
	}
	return &Object{Fields: fields}, true
}

// unionMembers 把类型解包为联合成员切片：Union 返回其 Types，否则单元素切片。
//
// 设计者视角：映射域/索引求值常需枚举成员，统一入口避免重复 type switch；
// 非联合类型视为单成员。排查现场：若域枚举遗漏成员，检查此处。
func unionMembers(t Type) []Type {
	if u, ok := t.(*Union); ok {
		return u.Types
	}
	return []Type{t}
}

// evalIndexedAccess resolves `T[K]`; ok=false defers (generic sides) while
// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：索引访问 `T[K]` 的求值必须区分"还没法算"（泛型侧，返回 defer）
// 与"确定性错误"（类型不支持索引，返回错误字符串），让调用方分别处理。
// 候选：返回 (Type,error) vs (Type,bool,string)；选后者，因为 defer 与 error
// 是两种不同结果，不能混用 error 表示 defer（会误报）。联合两侧按成员分发；
// Object 字段可选时并入 nil。排查现场：若索引求值过早报错，检查 IsConcreteType
// 守门；若 defer 丢失，检查 ok=false 分支。
func evalIndexedAccess(obj, index Type, opt Options) (Type, bool, string) {
	if !IsConcreteType(obj) || !IsConcreteType(index) {
		return &IndexedAccess{Obj: obj, Index: index}, false, ""
	}
	if anyLike(obj) {
		return Any, true, ""
	}
	if obj.Kind() == KNever {
		return Never, true, ""
	}
	// 排查看 Span/诊断，如 `local x=1` 错圈整句。
	if u, ok := index.(*Union); ok {
		var rs []Type
		for _, m := range u.Types {
			r, ok, errStr := evalIndexedAccess(obj, m, opt)
			if !ok {
				return &IndexedAccess{Obj: obj, Index: index}, false, ""
			}
			if errStr != "" {
				return nil, true, errStr
			}
			rs = append(rs, r)
		}
		return NewUnion(rs), true, ""
	}
	if u, ok := obj.(*Union); ok {
		var rs []Type
		for _, m := range u.Types {
			r, ok, errStr := evalIndexedAccess(m, index, opt)
			if !ok {
				return &IndexedAccess{Obj: obj, Index: index}, false, ""
			}
			if errStr != "" {
				return nil, true, errStr
			}
			rs = append(rs, r)
		}
		return NewUnion(rs), true, ""
	}
	switch o := obj.(type) {
	case *Object:
		if name, ok := literalKeyName(index); ok {
			if f, ok := o.Fields[name]; ok {
				if f.Optional {
					return NewUnion([]Type{f.Type, Nil}), true, ""
				}
				return f.Type, true, ""
			}
			// 排查看 Span/诊断，如 `local x=1` 错圈整句。
			for _, ix := range o.Indexers {
				if keyAcceptsLiteral(ix.Key, index) {
					return ix.Value, true, ""
				}
			}
			return nil, true, "type '" + obj.String() + "' has no field '" + name + "'"
		}
		// 排查看 Span/诊断，如 `local x=1` 错圈整句。
		if index.Kind() == KString || index.Kind() == KAny {
			var rs []Type
			for _, f := range o.Fields {
				rs = append(rs, f.Type)
			}
			for _, ix := range o.Indexers {
				if ix.Key.Kind() == KString || ix.Key.Kind() == KAny {
					rs = append(rs, ix.Value)
				}
			}
			if len(rs) == 0 {
				return nil, true, "cannot index type '" + obj.String() + "' with '" + index.String() + "'"
			}
			return NewUnion(rs), true, ""
		}
		return nil, true, "cannot index type '" + obj.String() + "' with '" + index.String() + "'"
	case *Tuple:
		if lit, ok := index.(*Literal); ok {
			n, err := tupleIndex(lit.Value)
			if err == nil && n >= 1 && n <= len(o.Elems) {
				return o.Elems[n-1], true, ""
			}
			return nil, true, "tuple index '" + lit.Value + "' out of range"
		}
		if index.Kind() == KNumber || index.Kind() == KInteger {
			if len(o.Elems) == 0 {
				return Never, true, ""
			}
			return NewUnion(o.Elems), true, ""
		}
		return nil, true, "cannot index tuple with '" + index.String() + "'"
	case *Array:
		if _, ok := index.(*Literal); ok {
			return o.Elem, true, ""
		}
		if index.Kind() == KNumber || index.Kind() == KInteger {
			return o.Elem, true, ""
		}
		return nil, true, "cannot index array with '" + index.String() + "'"
	case *Map:
		if IsAssignable(index, o.Key, opt) || IsAssignable(o.Key, index, opt) {
			return o.Value, true, ""
		}
		return nil, true, "cannot index type '" + obj.String() + "' with '" + index.String() + "'"
	case *Class:
		if name, ok := literalKeyName(index); ok {
			if f, ok := o.Fields[name]; ok && f.Access == Public {
				if f.Optional {
					return NewUnion([]Type{f.Type, Nil}), true, ""
				}
				return f.Type, true, ""
			}
			return nil, true, "type '" + obj.String() + "' has no field '" + name + "'"
		}
		return nil, true, "cannot index type '" + obj.String() + "' with '" + index.String() + "'"
	case *Enum:
		if name, ok := literalKeyName(index); ok {
			if m, ok := o.Members[name]; ok {
				return m, true, ""
			}
			return nil, true, "type '" + obj.String() + "' has no member '" + name + "'"
		}
		return nil, true, "cannot index type '" + obj.String() + "' with '" + index.String() + "'"
	}
	return nil, true, "cannot index type '" + obj.String() + "' with '" + index.String() + "'"
}

// keyAcceptsLiteral 判断索引签名的 key 类型是否接受某字面量键。
//
// 设计者视角：对象索引签名 `[K]: V` 的 K 可为 string/number/any 等，字面量
// 键的 base 必须与 K 兼容；此函数集中该兼容性判断，避免在求值主流程散落
// type switch。排查现场：若合法字段访问被拒，检查 key.Kind 与 lit.Base 分支。
func keyAcceptsLiteral(key, lit Type) bool {
	switch key.Kind() {
	case KString, KAny:
		if l, ok := lit.(*Literal); ok {
			return l.Base == String || l.Base == Integer || l.Base == Float || l.Base == Number
		}
		return true
	case KInteger, KNumber:
		if l, ok := lit.(*Literal); ok {
			return l.Base == Integer || l.Base == Float || l.Base == Number
		}
	}
	return false
}

// tupleIndex 把字面量字符串转为 1-based 元组索引。
//
// 设计者视角：元组索引使用 Lua/TS 风格的 1-based 字面量；strconv.Atoi 失败
// 返回 error，调用方据此报"索引越界/非法"。排查现场：若 0-based 索引被接受，
// 检查调用方的 n>=1 判断。
func tupleIndex(v string) (int, error) {
	return strconv.Atoi(v)
}

// tupleIndex如 `f(1)`，排查看 Span。
//
// 设计者视角：any 与 unknown 在索引求值中都视为"任意"，统一为 anyLike 避免
// 两处分支重复；保持与 checker 的 permissive 动态类型一致。排查现场：若
// unknown 索引未退化为 any，检查此函数。
func anyLike(t Type) bool {
	return t.Kind() == KAny || t.Kind() == KUnknown
}

// anyLike如 `f(1)`，排查看 Span。
//
// 设计者视角：TryEval* 是 eval* 的守门包装，供 assign/subst/conditional 调用；
// 本实现直接委托 evalMapped（其内部已做 IsConcreteType 守门），保持接口统一。
// 排查现场：若守门逻辑变更，确保 evalMapped 同步。
func TryEvalMapped(m *Mapped, opt Options) (Type, bool) {
	return evalMapped(m, opt)
}

// TryEvalIndexed evaluates `obj[index]` when both sides are concrete.
// 错误字符串在确定性错误时非空。
//
// 设计者视角：同 TryEvalMapped，直接委托 evalIndexedAccess；错误字符串仅在
// 确定性错误时非空，defer 时为空。排查现场：若调用方把 defer 当错误，检查
// bool 语义。
func TryEvalIndexed(obj, index Type, opt Options) (Type, bool, string) {
	return evalIndexedAccess(obj, index, opt)
}
