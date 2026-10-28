package types

import (
	"fmt"
	"strings"
)

// 基础原语类型单例。指针身份即身份：同一原语永远复用同一指针，
// 让缓存键、map key 与快速比较都退化为指针相等。
var (
	// Any 是any 类型单例。
	Any = &Primitive{KAny, "any"}
	// Unknown 是unknown 类型单例。
	Unknown = &Primitive{KUnknown, "unknown"}
	// Never 是never 类型单例。
	Never = &Primitive{KNever, "never"}
	// Void 是void 类型单例。
	Void = &Primitive{KVoid, "void"}
	// Nil 是nil 类型单例。
	Nil = &Primitive{KNil, "nil"}
	// Boolean 是boolean 类型单例。
	Boolean = &Primitive{KBoolean, "boolean"}
	// Number 是number 类型单例。
	Number = &Primitive{KNumber, "number"}
	// Integer 是integer 类型单例。
	Integer = &Primitive{KInteger, "integer"}
	// Float 是float 类型单例。
	Float = &Primitive{KFloat, "float"}
	// String 是string 类型单例。
	String = &Primitive{KString, "string"}
	// Table 是table 类型单例。
	Table = &Primitive{KTable, "table"}
	// Thread 是thread 类型单例。
	Thread = &Primitive{KThread, "thread"}
	// Userdata 是userdata 类型单例。
	Userdata = &Primitive{KUserdata, "userdata"}
	// Cdata 是cdata 类型单例。
	Cdata = &Primitive{KCdata, "cdata"}
	// Ctype 是ctype 类型单例。
	Ctype = &Primitive{KCtype, "ctype"}
	// Function 是function 原语单例。
	Function = &Primitive{KFunction, "function"}
)

// Literal 是字符串/数字/布尔/nil 字面量类型（如 `"red"`、`42`、`true`）。
//
// 为什么 Value 存源码拼写而 Base 存基类：Value 保证打印与缓存键稳定（保留
// 原始拼写），Base 供子类型判定（字面量 <: 其基类）。两者分离避免打印
// 时丢失引号/前缀。
//
// 物理布局：
//
//	Literal
//	├── Value string   // 源码拼写，含引号/前缀
//	└── Base  Type     // String/Number/Boolean/Nil 原语
type Literal struct {
	Value string // source spelling, e.g. `"red"`, `42`, `true`
	Base  Type   // String/Number/Boolean/Nil
}

// Kind 返回 Literal 的类型分类标签。
func (t *Literal) Kind() Kind { return KLiteral }

// String 把 Literal 渲染为规范文本。
func (t *Literal) String() string { return t.Value }

// Union 是已范式化的并集类型（A | B | ...）。
//
// 为什么必须范式化：展平嵌套、去掉 never、去重、排序，让子类型判定与缓存键
// 稳定——同一类型只有一种文本形式，避免“Structurally same but textually
// different”导致的缓存失效。代价是构造时多一次规范化（NewUnion），换来
// 热路径 O(1) 比较与 map key 稳定。
//
// 物理布局：
//
//	Union
//	└── Types []Type   // 已展平、去 never、去重、按 String() 排序
type Union struct {
	Types []Type
}

// Kind 返回 Union 的类型分类标签。
func (t *Union) Kind() Kind { return KUnion }

// typeString formats t with a visited set: shared *Object nodes reached
// twice (recursive declarations) collapse to their name (or "{...}"),
// so printing a cyclic type terminates. Only objects can recur
// structurally (classes print nominally); containers forward the set.
func typeString(t Type, seen map[Type]bool) string {
	switch x := t.(type) {
	case *Object:
		if seen[x] {
			if x.Name != "" {
				return x.Name
			}
			return "{...}"
		}
		seen[x] = true
		return x.stringSeen(seen)
	case *Union:
		return x.stringSeen(seen)
	case *Intersection:
		return x.stringSeen(seen)
	case *Array:
		return x.stringSeen(seen)
	case *Map:
		return x.stringSeen(seen)
	case *Tuple:
		return x.stringSeen(seen)
	case *Func:
		return x.stringSeen(seen)
	case *Class:
		return x.stringSeen(seen)
	case *GenericParam:
		return x.stringSeen(seen)
	case *Ref:
		return x.stringSeen(seen)
	case *Conditional:
		return x.stringSeen(seen)
	case *Mapped:
		return x.stringSeen(seen)
	case *IndexedAccess:
		return x.stringSeen(seen)
	case *Chan:
		return x.stringSeen(seen)
	default:
		return t.String()
	}
}

// String 把 Union 渲染为规范文本。
func (t *Union) String() string { return t.stringSeen(map[Type]bool{}) }
func (t *Union) stringSeen(seen map[Type]bool) string {
	parts := make([]string, len(t.Types))
	for i, x := range t.Types {
		parts[i] = typeString(x, seen)
	}
	return strings.Join(parts, " | ")
}

// Intersection 是已范式化的交集类型（A & B & ...）。
//
// 与 Union 对偶：展平、去 any（any 是交集的单位元）、never 吸收、去重、
// 排序。范式化同样是为了缓存键与判定稳定。
//
// 物理布局：
//
//	Intersection
//	└── Types []Type   // 已展平、去 any、never 吸收、去重、排序
type Intersection struct {
	Types []Type
}

// Kind 返回 Intersection 的类型分类标签。
func (t *Intersection) Kind() Kind { return KIntersection }

// String 把 Intersection 渲染为规范文本。
func (t *Intersection) String() string { return t.stringSeen(map[Type]bool{}) }
func (t *Intersection) stringSeen(seen map[Type]bool) string {
	parts := make([]string, len(t.Types))
	for i, x := range t.Types {
		parts[i] = typeString(x, seen)
	}
	return strings.Join(parts, " & ")
}

// Array 是 T[]（Lua table 作数组/序列的类型视图）。
//
// 为什么单独于 Tuple：数组长度不定、元素类型单一，子类型协变；元组长度
// 固定、位置类型可异。混在一起会让索引访问与展开逻辑分叉。
//
// 物理布局：
//
//	Array
//	└── Elem Type   // 元素类型
type Array struct {
	Elem Type
}

// Kind 返回 Array 的类型分类标签。
func (t *Array) Kind() Kind { return KArray }

// String 把 Array 渲染为规范文本。
func (t *Array) String() string { return t.stringSeen(map[Type]bool{}) }
func (t *Array) stringSeen(seen map[Type]bool) string {
	return typeString(t.Elem, seen) + "[]"
}

// Map 是 map<K, V>（Lua table 作映射的类型视图）。
//
// 为什么 Key/Value 都是 Type 而非仅 Value：Lua table 的键可以是任意可哈希
// 值，类型系统必须同时约束键与值；any 键退化为“所有键”。
//
// 物理布局：
//
//	Map
//	├── Key   Type   // 键类型
//	└── Value Type   // 值类型
type Map struct {
	Key   Type
	Value Type
}

// Kind 返回 Map 的类型分类标签。
func (t *Map) Kind() Kind { return KMap }

// String 把 Map 渲染为规范文本。
func (t *Map) String() string { return t.stringSeen(map[Type]bool{}) }
func (t *Map) stringSeen(seen map[Type]bool) string {
	return fmt.Sprintf("map<%s, %s>", typeString(t.Key, seen), typeString(t.Value, seen))
}

// Tuple 是固定长度、位置类型可异的元组 `[A, B, ...]`。
//
// 与 Array 的区别见 Array 注释；尾部 `...Rest` 在类型语法层展开为 Ref{"..."}，
// 语义层仍落在 Tuple.Elems 里，由 matchInfer/restTarget 处理。
//
// 物理布局：
//
//	Tuple
//	└── Elems []Type   // 位置类型；可含 `...T` rest 元素
type Tuple struct {
	Elems []Type
}

// Kind 返回 Tuple 的类型分类标签。
func (t *Tuple) Kind() Kind { return KTuple }

// String 把 Tuple 渲染为规范文本。
func (t *Tuple) String() string { return t.stringSeen(map[Type]bool{}) }
func (t *Tuple) stringSeen(seen map[Type]bool) string {
	parts := make([]string, len(t.Elems))
	for i, e := range t.Elems {
		parts[i] = typeString(e, seen)
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

// Chan 是 `chan<T>`：承载 Elem 类型值的 CSP 通道。
//
// 为什么类型系统里有 channel：TypeLua 提供 coroutine/channel 并发原语，
// 通道必须可类型化才能做 send/recv 的类型检查。T 不变（invariant）：chan<A>
// 与 chan<B> 仅在 A、B 互相可赋值时兼容，避免协变导致的数据竞争 unsoundness。
//
// 物理布局：
//
//	Chan
//	└── Elem Type   // 通道承载的值类型
type Chan struct {
	Elem Type
}

// Kind 返回 Chan 的类型分类标签。
func (t *Chan) Kind() Kind { return KChan }

// String 把 Chan 渲染为规范文本。
func (t *Chan) String() string { return t.stringSeen(map[Type]bool{}) }
func (t *Chan) stringSeen(seen map[Type]bool) string {
	return fmt.Sprintf("chan<%s>", typeString(t.Elem, seen))
}
