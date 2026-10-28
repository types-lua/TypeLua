package types

// Kind 是 types.Type 的分类标签（discriminant），只回答“这是哪一种类型”，
// 不携带任何数据；数据与行为都在具体 struct（*Primitive/*Union/...）里。
//
// 为什么用 int 枚举而非类型断言/接口：Kind 让穷举 switch 与缓存键稳定，
// 且避免在热路径上做反射；代价是新增类型时编译器不会强制覆盖所有 switch，
// 靠测试与审查兜底。
//
// 物理布局（Type 与 Kind 的关系）：
//
//	Type (interface)                Kind (int 标签)
//	┌──────────────────┐            ┌──────────┐
//	│ Kind() Kind      │──returns──►│ KObject  │
//	│ String() string  │            └──────────┘
//	└────────┬─────────┘
//	         │ 动态分派到具体 struct
//	         ▼
//	*Primitive{K Kind; Name string}
//	*Union{Types []Type}            *Intersection{Types []Type}
//	*Array{Elem Type}               *Map{Key,Value Type}
//	*Tuple{Elems []Type}            *Func{Params;Returns;...}
//	*Object{Fields;Indexers}        *Class{Def;Args;Fields;Base}
//	*ClassValue{Class;Constructor;Statics}
//	*Literal{Value;Base}            *Enum{Name;Underlying;Members}
//	*GenericParam{Name;Constraint;Default}
//	*Ref{Name;Args}                 *InferParam{Name}
//	*Conditional{Check;Extends;True;False;Distributive}
//	*Mapped{Param;Constraint;...}   *IndexedAccess{Obj;Index}
//	*Chan{Elem}
//
// 枚举取值按家族分组（数值稳定，供缓存键与诊断）：
//
//	基础原语 KAny..KCtype, KFunction
//	复合/容器 KLiteral, KUnion, KIntersection, KArray, KMap, KTuple, KFunc
//	对象/名义 KObject, KEnum, KGenericParam, KRef, KClass, KClassValue
//	高阶类型 KConditional, KInferParam, KMapped, KIndexedAccess, KChan
type Kind int

const (
	// KAny 是 any 类型的 kind 标签。
	KAny Kind = iota
	// KUnknown 是 unknown 类型的 kind 标签。
	KUnknown
	// KNever 是 never 类型的 kind 标签。
	KNever
	// KVoid 是 void 类型的 kind 标签。
	KVoid
	// KNil 是 nil 类型的 kind 标签。
	KNil
	// KBoolean 是 boolean 类型的 kind 标签。
	KBoolean
	// KNumber 是 number 类型的 kind 标签。
	KNumber
	// KInteger 是 integer 类型的 kind 标签。
	KInteger
	// KFloat 是 float 类型的 kind 标签。
	KFloat
	// KString 是 string 类型的 kind 标签。
	KString
	// KTable 是 table 类型的 kind 标签。
	KTable
	// KThread 是 thread 类型的 kind 标签。
	KThread
	// KUserdata 是 userdata 类型的 kind 标签。
	KUserdata
	// KCdata 是 cdata 类型的 kind 标签。
	KCdata
	// KCtype 是 ctype 类型的 kind 标签。
	KCtype
	// KFunction 是 function 原语的 kind 标签。
	KFunction
	// KLiteral 是字面量类型的 kind 标签。
	KLiteral
	// KUnion 是并集类型的 kind 标签。
	KUnion
	// KIntersection 是交集类型的 kind 标签。
	KIntersection
	// KArray 是数组类型的 kind 标签。
	KArray
	// KMap 是映射类型的 kind 标签。
	KMap
	// KTuple 是元组类型的 kind 标签。
	KTuple
	// KFunc 是函数类型的 kind 标签。
	KFunc
	// KObject 是对象/接口类型的 kind 标签。
	KObject
	// KEnum 是枚举类型的 kind 标签。
	KEnum
	// KGenericParam 是泛型参数的 kind 标签。
	KGenericParam
	// KRef 是未解析命名类型的 kind 标签。
	KRef
	// KClass 是类实例类型的 kind 标签。
	KClass
	// KClassValue 是类值（typeof）类型的 kind 标签。
	KClassValue
	// KConditional 是条件类型的 kind 标签。
	KConditional
	// KInferParam 是 infer 占位符的 kind 标签。
	KInferParam
	// KMapped 是映射类型的 kind 标签。
	KMapped
	// KIndexedAccess 是索引访问类型的 kind 标签。
	KIndexedAccess
	// KChan 是通道类型的 kind 标签。
	KChan
)

// Type 是所有语义类型共用的最小契约：分类标签 + 稳定字符串渲染。
// 字符串渲染用于诊断消息与缓存键，Kind 用于分派；所有聪明逻辑在
// 使用它们的函数里（子类型、替换、实例化等）。
type Type interface {
	Kind() Kind
	String() string
}

// Access 是类成员的可见性（plain object 永远是 public，不使用此字段）。
//
// 为什么单独一个类型而非 bool：三态（public/private/protected）需要可扩展的
// 枚举；用 int 让 switch 穷举与 String() 集中处理。
//
// 物理布局：单个 int，0=Public,1=Private,2=Protected。
type Access int

const (
	// Public 表示公开可见性。
	Public Access = iota
	// Private 表示私有可见性。
	Private
	// Protected 表示受保护可见性。
	Protected
)

// String 把 Access 渲染为可见性关键字拼写。
//
// 设计者视角：可见性关键字固定三态，switch 穷举即可；默认分支返回 "public"
// 而非报错，是为了让未知/零值 Access 退化为最宽松的 public，避免构造期
// 遗漏 Access 时把成员误判为私有导致误报。排查现场：若诊断里出现 "public"
// 而源码写的是 protected，检查 Access 常量赋值与 iota 顺序。
func (a Access) String() string {
	switch a {
	case Private:
		return "private"
	case Protected:
		return "protected"
	}
	return "public"
}

// Field 是对象/接口成员的统一描述：类型、可选、只读、可见性与定义类。
//
// 为什么一个 struct 覆盖对象与类：减少类型种类与转换；靠 Owner 与 Access
// 约定区分——plain object 的 Owner=nil 且 Access=Public，类成员带 Owner
// 与真实 Access。代价是“谁是类成员”由约定而非类型系统保证。
//
// 物理布局：
//
//	Field
//	├── Type     Type        // 成员类型
//	├── Optional bool        // `?`
//	├── Readonly bool        // `readonly`
//	├── Access   Access      // 仅类有意义
//	└── Owner    *ClassDef   // 定义该成员的类身份；nil=普通对象
type Field struct {
	Type     Type
	Optional bool
	Readonly bool
	Access   Access
	Owner    *ClassDef // defining class for class members; nil otherwise
}

// Indexer 是索引签名 `[K]: V`（对象的动态键集合）。
//
// 为什么独立于 Field：索引签名没有名字、没有可选/只读语义（由 mapped
// 标记单独决定），且一个对象可同时有多个不同键类型的签名。
//
// 物理布局：Indexer{Key Type; Value Type}
type Indexer struct {
	Key   Type
	Value Type
}

// Primitive 是无状态/少状态的基础类型：any/unknown/never/void/nil/boolean/
// number/integer/float/string/table/thread/userdata/cdata/ctype/function 等。
//
// 为什么 K 与 Name 分离：K 供分派与缓存，Name 供打印与缓存键；单例变量
// （Any/Unknown/...）保证指针身份稳定，让 map/set 与 memo key 可用指针。
//
// 物理布局：
//
//	Primitive
//	├── K    Kind   // 分类标签
//	└── Name string // 源码拼写（"number"、"any" 等）
type Primitive struct {
	K    Kind
	Name string
}

// Kind 返回 Primitive 的类型分类标签。
//
// 设计者视角：Primitive 的分类直接由 K 字段承载（与其它类型"Kind() 返回固定
// 常量"不同），因为原语家族共享一个 struct、靠 K 区分；这样 any/number/...
// 都是 *Primitive 单例，指针身份稳定。排查现场：若 Kind() 返回异常，检查
// 单例初始化时 K 是否与 Name 对齐。
func (t *Primitive) Kind() Kind { return t.K }

// String 把 Primitive 渲染为规范文本（用于消息与缓存键）。
//
// 设计者视角：原语打印名即其身份（"number"、"any"），Name 在单例初始化时
// 固定，避免每次渲染重新映射 Kind->字符串；缓存键依赖该字符串稳定。
// 排查现场：若缓存命中失败，检查单例 Name 是否被意外覆盖。
func (t *Primitive) String() string { return t.Name }
