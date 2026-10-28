package types

import (
	"strings"
)

// ClassField 是类成员（字段或方法）的描述。
//
// 为什么与 Field 分离：类成员多出 IsMethod/Abstract/Owner 语义，且方法不可
// 选；复用 Field 会把“方法可选”这类非法状态暴露出来。Owner 记录声明该
// 成员的类身份，供可见性与继承判定。
//
// 物理布局：
//
//	ClassField
//	├── Type     Type        // 成员类型（方法为函数类型）
//	├── Optional bool        // 字段可选（方法恒 false）
//	├── Readonly bool
//	├── Access   Access      // 可见性
//	├── IsMethod bool
//	├── Abstract bool
//	└── Owner    *ClassDef   // 声明类身份
type ClassField struct {
	Type     Type
	Optional bool // fields only; methods are never optional
	Readonly bool
	Access   Access
	IsMethod bool
	Abstract bool
	Owner    *ClassDef // class that declared this member (for visibility)
}

// ClassDef 是类声明的身份令牌（仅 Name）。
//
// 为什么只存 Name：身份比较靠指针相等，Name 仅用于打印/诊断；不存结构形状，
// 因为名义子类型以声明身份为准，结构相同的不同类也不可互换。像身份证号：
// 照片会撞脸，证号不会。
//
// 物理布局：
//
//	ClassDef
//	└── Name string   // 声明名（指针身份即身份）
type ClassDef struct {
	Name string
}

// Class 是名义实例类型。两个类类型仅沿继承链可赋值；与 plain object/
// interface 比较时按 public 成员结构化检查。
//
// 为什么名义性靠 Def 指针：泛型类的所有实例共享一个 Def，实例差异由 Args
// 承载；assignability 先比 Def 身份再比继承链，避免结构巧合导致的误判。
//
// 物理布局：
//
//	Class
//	├── Def        *ClassDef            // 声明身份（指针相等）
//	├── Name       string              // 打印名
//	├── Args       []Type              // 泛型实参
//	├── Params     []string            // 泛型形参名（冗余，便于打印）
//	├── Fields     map[string]ClassField // 实例成员（字段+方法）
//	├── Base       *Class              // 父类（nil=根）
//	├── IsAbstract bool
//	└── TypeParams []*GenericParam     // 泛型声明（约束/默认）
type Class struct {
	Def        *ClassDef
	Name       string
	Args       []Type // instantiation arguments for generic classes
	Params     []string
	Fields     map[string]ClassField // instance members (fields + methods)
	Base       *Class
	IsAbstract bool
	// TypeParams holds resolved generic declarations (constraints and
	// defaults intact) for explicit-argument checking on `new`.
	TypeParams []*GenericParam
}

// Kind 返回 Class 的类型分类标签。
//
// 设计者视角：Class 是独立 struct，分类恒定 KClass；名义类型必须与 Object
// 区分，以便 assignability 先比 Def 身份。候选：与 Object 共用 struct 加 flag
// vs 独立 struct；选独立，让名义/结构两条判定永不串线。排查现场：无。
func (t *Class) Kind() Kind { return KClass }

// String 把 Class 渲染为规范文本。
//
// 设计者视角：泛型类打印 `Name<Args>`，无实参时只打印 Name；Args 经 typeString
// 转发 seen。为什么不打字段——类是名义身份，字段是实现细节，诊断只认名字加
// 实参，详情看定义处。不变量：同声明同实参文本相同。排查现场：若泛型实参丢失，
// 检查 Args 赋值与 seen 传递。
func (t *Class) String() string { return t.stringSeen(map[Type]bool{}) }

// stringSeen 渲染 Class，实参经 typeString 转发 seen。
//
// 设计者视角：Class 可自引用（字段类型含自身），但 Class 不入 seen——因为
// 类型打印按 Name 恒等，递归仅通过 Args/Fields；seen 由 typeString 在 Object
// 层终止。排查现场：若深层层类型折叠，检查 seen 传递链。
func (t *Class) stringSeen(seen map[Type]bool) string {
	if len(t.Args) == 0 {
		return t.Name
	}
	ps := make([]string, len(t.Args))
	for i, a := range t.Args {
		ps[i] = typeString(a, seen)
	}
	return t.Name + "<" + strings.Join(ps, ", ") + ">"
}

// ObjectView exposes public instance members structurally (for
// interface conformance checks).
// ObjectView 把类实例的 public 成员暴露为结构化 Object，供接口一致性检查。
//
// 设计者视角：接口一致性检查需要把名义类实例转为结构化 Object，仅暴露 public
// 成员（private/protected 不参与接口）。候选：在 assignability 里特殊处理
// Class vs Object vs 提供 ObjectView；选 ObjectView，让 objectAssignable 复用
// 同一结构化逻辑，避免两套成员遍历。排查现场：若私有成员误参与接口检查，
// 检查 classObjectView 的 Access 过滤。
func (t *Class) ObjectView() *Object { return classObjectView(t) }

// ClassValue 是运行时的类表：构造器 + 静态成员；调用它（或其 `new`）
// 产生 Class 实例。
//
// 为什么与 Class 分离：Class 是实例形状（字段/继承），ClassValue 是“类本身”
// 作为值的类型（typeof Foo）；把二者混在一起会让 `Foo` 与 `Foo.new()` 的
// 类型无法区分，且静态成员无处安放。
//
// 物理布局：
//
//	ClassValue
//	├── Class       *Class                 // 产生的实例类型（泛型定义态）
//	├── Constructor *Func                  // 构造器（declare stub 为 nil）
//	├── CtorAccess  Access                 // 构造器可见性
//	├── Statics     map[string]ClassField  // 静态成员
//	└── Abstract    bool
type ClassValue struct {
	Class       *Class // instance type produced (generic definition form)
	Constructor *Func  // nil only for declare stubs (treated as any)
	CtorAccess  Access // visibility of the constructor
	Statics     map[string]ClassField
	Abstract    bool
}

// Kind 返回 ClassValue 的类型分类标签。
//
// 设计者视角：ClassValue 是"类本身作为值"的类型，与 Class（实例）分离；分类
// 恒定 KClassValue。为什么分离——实例是数据形状，类值是构造器加静态表；混用
// 会让 `Foo`（值）与 `Foo`（实例）无法区分。排查现场：无。
func (t *ClassValue) Kind() Kind { return KClassValue }

// String 把 ClassValue 渲染为规范文本。
//
// 设计者视角：打印 `typeof Name` 以区分实例类型与类值类型；Class 为 nil 时
// 退化为 "class"（declare stub）。为什么加 typeof 前缀——裸名会与实例混淆，
// 前缀让诊断一眼看出这是“类本身”。排查现场：若 typeof 打印丢失类名，检查
// Class 字段赋值。
func (t *ClassValue) String() string {
	if t.Class != nil {
		return "typeof " + t.Class.Name
	}
	return "class"
}
