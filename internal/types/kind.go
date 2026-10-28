package types

// Kind 是 Type 的分类条码：只答“哪一种”（如 `local x: string` 的 string 即 KString），不存数据。
// 用 int 而非反射：switch 穷举快、缓存键稳；代价是新增类型须手补全部分 switch，靠测试兜底。
// 布局：Type 接口（Kind()+String()）→ 30 余实现（*Primitive/*Union/*Object/*Class/*Func 等，见下）。
// 取值按家族排（数值冻结供缓存）：原语 KAny..KFunction → 容器 KLiteral..KFunc
// → 名义 KObject..KClassValue → 高阶 KConditional..KChan。
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

// Type 是语义值的最小契约：Kind 分派 + String 渲染（如报错 `expected string, got integer` 与缓存 key 同源）。
type Type interface {
	Kind() Kind
	String() string
}

// Access 是类成员门禁（普通表恒为 public）：三态须枚举而非 bool，如 `private x: integer` 误从外部读即拦。
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

// Field 通吃对象与类成员：`{name?: string}` 的 Optional 与 `readonly x`、`private` 皆存于此；
// 普通表 Owner=nil 且 Access=Public，类成员带真 Owner（如 Dog 的 name 归 Dog）。
type Field struct {
	Type     Type
	Optional bool
	Readonly bool
	Access   Access
	Owner    *ClassDef // defining class for class members; nil otherwise
}

// Indexer 是动态键签名 `[K]: V`（如 `{[string]: integer}`），与具名字段 Field 分家，一对象可挂多个。
type Indexer struct {
	Key   Type
	Value Type
}

// Primitive 是基础单例（如全局唯一的 string/number/any）：K 供分派，Name 供打印与缓存键，指针身份恒稳。
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
