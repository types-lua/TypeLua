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
//
// 设计者视角：Literal 是独立 struct，分类恒定 KLiteral；字面量类型必须与
// 基类原语区分，以便 assignability 的"字面量 <: 基类"规则。排查现场：无。
func (t *Literal) Kind() Kind { return KLiteral }

// String 把 Literal 渲染为规范文本。
//
// 设计者视角：字面量打印其源码拼写（含引号/前缀），Value 即身份与缓存键；
// 不走 typeString，因为 Literal 不含循环对象。排查现场：若打印丢失引号，
// 检查 Value 的构造赋值。
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
//
// 设计者视角：Union 是独立 struct，分类恒定 KUnion；调用方靠它把“或”从
// 交集/对象/数组中一眼分出来，再分派到 unionAssignable。字面量 `A|B` 在
// 构造时已范式化，这里只做鉴别、不做范式化校验。排查现场：无。
func (t *Union) Kind() Kind { return KUnion }

// typeString formats t with a visited set: shared *Object nodes reached
// twice (recursive declarations) collapse to their name (or "{...}"),
// so printing a cyclic type terminates. Only objects can recur
// structurally (classes print nominally); containers forward the set.
//
// 设计者视角：打印是“照镜子”，镜子里的人不能再举一面镜子，否则无限反射。
// 递归接口 `type N = { next: N }` 若朴素递归打印，栈会先爆掉。候选有三：
// 全局深度截断（误伤深但无环的泛型链）、String 缓存（循环仍死循环）、
// visited 集合（只拦回头路）。选 visited：只有 *Object 会结构自引用，
// Class 按名打印天然终止，所以 seen 只对 Object 设卡，其余容器只转发。
// 不变量：seen 以指针身份判等，第二次遇到同 *Object 即折叠为 Name 或 "{...}"。
// 排查现场：若本该折叠的类型仍栈溢出，检查是否绕过 typeString 直接调 String()；
// 若误折叠为 "{...}"，检查 seen 是否被复用而未按顶层调用新建。
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
//
// 设计者视角：Union 的文本即身份的一部分：成员已按 String() 排序，去重展平
// 都在 NewUnion 里做完，这里只做 `" | "` 连接。委托 stringSeen 并以空 seen
// 起步，seen 由 typeString 统一转发，保证深层循环对象仍能折叠而非死循环。
// 不变量：输出顺序稳定，`A|B` 与 `B|A` 文本相同。排查现场：若缓存键抖动，
// 先查 NewUnion 的排序，去重后再看这里的 Join。
func (t *Union) String() string { return t.stringSeen(map[Type]bool{}) }

// stringSeen 用 visited 集合渲染 Union，成员经 typeString 转发 seen。
//
// 设计者视角：Union 自身不入 seen（只有 Object 会结构自引用），成员递归时
// 把 seen 原样递给 typeString；若成员含循环对象，折叠发生在 Object 层。
// 排查现场：若深层对象未折叠，检查是否绕过 typeString 直接调成员 String()。
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
//
// 设计者视角：Intersection 是独立 struct，分类恒定 KIntersection；它与 Union
// 对偶——assignability 对它要求“每一路都成立”。恒定分类让 switch 分派一眼
// 可辨，避免与 Union 共用 struct 再靠 flag 区分。排查现场：无。
func (t *Intersection) Kind() Kind { return KIntersection }

// String 把 Intersection 渲染为规范文本。
//
// 设计者视角：交集文本用 `" & "` 连接已范式化成员，顺序同样按 String() 排序
// 以保缓存键稳定。String 委托 stringSeen 并新建空 seen，深层循环仍由 Object
// 层折叠。排查现场：若 `A&B` 与 `B&A` 文本不一致，检查 NewIntersection 排序。
func (t *Intersection) String() string { return t.stringSeen(map[Type]bool{}) }

// stringSeen 用 visited 集合渲染 Intersection，成员经 typeString 转发 seen。
//
// 设计者视角：与 Union 同理，自身不入 seen，只转发；折叠点永远在 Object。
// 排查现场：同 Union。
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
//
// 设计者视角：Array 是独立 struct，分类恒定 KArray；数组与 Tuple/Map 语义
// 分岔（协变元素 vs 定长位置 vs 键值），必须靠 Kind 分流到不同判定分支。
// 若把三者合并为“table 一体”，索引与赋值逻辑会处处 if/else。排查现场：无。
func (t *Array) Kind() Kind { return KArray }

// String 把 Array 渲染为规范文本。
//
// 设计者视角：数组打印为 `Elem[]` 糖形而非 `Array<Elem>`，贴近 TypeLua 源码
// 写法，便于诊断一眼还原。Elem 经 typeString 转发 seen，保证 `({next:T})[]`
// 这类嵌套循环仍可终止。不变量：`T[]` 文本唯一。排查现场：若元素是循环对象
// 却未折叠，检查是否误调 Elem.String() 而非 typeString。
func (t *Array) String() string { return t.stringSeen(map[Type]bool{}) }

// stringSeen 用 visited 集合渲染 Array，元素经 typeString 转发 seen。
//
// 设计者视角：Array 自身不入 seen（无结构自引用），只把 Elem 递给 typeString；
// 循环折叠仍在 Object 层。若 Elem 是循环对象而未折叠，必是 seen 未透传。
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
//
// 设计者视角：Map 是独立 struct，分类恒定 KMap；`map<K,V>` 是 Lua table 的
// 键值视图，与定长 Tuple/单元素 Array 正交。恒定分类让 assignability 能把
// “对象转映射”“数组转映射”等交叉分支收敛到一处。排查现场：无。
func (t *Map) Kind() Kind { return KMap }

// String 把 Map 渲染为规范文本。
//
// 设计者视角：打印 `map<K, V>` 还原声明形状，Key/Value 都经 typeString 转发
// seen；诊断里看到的即源码写法，缓存键亦以此为准。排查现场：若键/值的循环
// 对象未折叠，检查 seen 是否透传。
func (t *Map) String() string { return t.stringSeen(map[Type]bool{}) }

// stringSeen 用 visited 集合渲染 Map，键值经 typeString 转发 seen。
//
// 设计者视角：Map 自身不入 seen，只转发 Key/Value；折叠点仍在 Object。
// 排查现场：若深层循环未折叠，检查 Key/Value 是否走 typeString。
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
//
// 设计者视角：Tuple 是独立 struct，分类恒定 KTuple；定长位置语义（`[A, B]`
// 长度即类型的一部分）与变长 Array 截然不同，混用会导致展开与索引越界检查
// 失准。排查现场：无。
func (t *Tuple) Kind() Kind { return KTuple }

// String 把 Tuple 渲染为规范文本。
//
// 设计者视角：元组按位置打印 `[A, B, ...Rest]`，位置顺序即语义，不排序；
// 每个 Elems 经 typeString 转发 seen，rest 元素（`...T`）同样走 typeString。
// 不变量：长度与顺序稳定，文本可逆。排查现场：若 rest 打印异常，检查 Elems
// 里 Ref{"..."} 的构造。
func (t *Tuple) String() string { return t.stringSeen(map[Type]bool{}) }

// stringSeen 用 visited 集合渲染 Tuple，位置元素经 typeString 转发 seen。
//
// 设计者视角：Tuple 自身不入 seen，顺序即语义不排序；rest 元素同样走
// typeString，保证嵌套循环可终止。排查现场：同 Array。
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
//
// 设计者视角：Chan 是独立 struct，分类恒定 KChan；通道不变（invariant）——
// `chan<A>` 与 `chan<B>` 仅双向可赋值时兼容，协变会引入数据竞争的 unsound。
// 独立 Kind 让这条“双向检查”分支不被数组/映射的协变分支淹没。排查现场：无。
func (t *Chan) Kind() Kind { return KChan }

// String 把 Chan 渲染为规范文本。
//
// 设计者视角：打印 `chan<Elem>` 还原声明形状，Elem 经 typeString 转发 seen；
// Elem 不变性不在打印层体现，只在 assignability 层双向检查。排查现场：
// 若嵌套 chan 的循环对象未折叠，检查 seen 透传。
func (t *Chan) String() string { return t.stringSeen(map[Type]bool{}) }

// stringSeen 用 visited 集合渲染 Chan，元素经 typeString 转发 seen。
//
// 设计者视角：Chan 自身不入 seen，只转发 Elem；不变性不在此体现。
// 排查现场：若嵌套循环未折叠，检查 seen 透传。
func (t *Chan) stringSeen(seen map[Type]bool) string {
	return fmt.Sprintf("chan<%s>", typeString(t.Elem, seen))
}
