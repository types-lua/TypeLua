package ast

import (
	"github.com/typelua/typelua/internal/token"
)

// 类声明节点：`class C {…}`，成员 Span 各管一段。

// ---------- Classes ----------

// Visibility 表示类成员的可见性修饰详见实现，排查看 Span。
type Visibility int

const (
	Public Visibility = iota
	Private
	Protected
)

// ClassDecl 表示 `class` 声明语句节点；S 为源码 Span，用于诊断与定位。
//
// 设计者视角：ClassDecl 是名义类型在语法层的投胎，好比楼盘预售证——Name/Generics/
// Extends/Implements 只记“申报形状”，真正户型（字段布局、方法表）由 checker 的
// ClassValue 建成。障碍是单继承与多实现的混排：Extends 只允许一个（nil 即无基类），
// Implements 另起切片，避免把“是一种”与“能干什么”混进同一槽。候选：独立 ClassDecl
// vs 复用 TableType；选独立，因为可见性/静态/抽象/本地性等修饰在结构类型里无处安放。
// 不变量：Declared 为 true 时 Body 皆空（仅声明不建楼）；Abstract 类不直接实例化由
// checker 拦截。排查现场：若继承链诊断指错位置，先看 Extends/Implements 各自的 Span
// 是否独立，再看 S 是否覆盖整个 class...end。
type ClassDecl struct {
	Name       *Ident
	Generics   []*GenericParam
	Extends    TypeNode // nil if none
	Implements []TypeNode
	Members    []*ClassMember
	Exported   bool
	Declared   bool
	Abstract   bool
	IsLocal    bool // `local class` (or class in non-global scope intent)
	S          token.Span
}

func (*ClassDecl) stmtNode() {}

// Span 取源码区间，如 `x=1` 圈整句供诊断画线。
//
// 设计者视角：ClassDecl 的 Span 覆盖整个 `class ... end`，是折叠、跳转、报错的
// 统一门牌号；成员各自另有小门牌详见实现，排查看 Span。
// 就圈整类”。排查现场：若类报错只圈到类名，检查 parser 是否忘了把 end 纳入 S。
func (n *ClassDecl) Span() token.Span { return n.S }

// ClassMemberKind 区分类成员的种类（字段/方法/构造器），决定后续字段的解释方式。
type ClassMemberKind int

const (
	ClassField ClassMemberKind = iota
	ClassMethod
	ClassConstructor
)

// ClassMember 表示类体中的一个成员（字段/方法/构造器）；S 为源码 Span，用于诊断与定位。
//
// 设计者视角：三类成员挤进一个 struct 是刻意为之，好比多功能插座——Kind 决定哪组
// 插孔通电：ClassField 只看 FieldType/Init，ClassMethod/ClassConstructor 只看
// Params/Body/ReturnType。障碍是字段与方法名字空间重叠、构造器无名：用 Name 统一
// 承载（构造器固定填 "constructor"），Kind 做唯一判据，避免调用方猜字符串。
// 候选：拆三 struct + 接口 vs 单 struct + Kind；选后者，让 parser 按“冒号/括号/等号”
// 一次分流填槽，checker/emitter 按 Kind 一次 switch，分支遗漏在编译期暴露。
// 不变量：abstract/declare 成员 Body 为 nil（只有签名没有肉身）；IsStatic 与
// Visibility 正交。排查现场：若构造器被当普通方法检查，先看 Kind 是否误标为
// ClassMethod，再看 Name 回填是否缺失。
type ClassMember struct {
	Kind       ClassMemberKind
	Name       *Ident // method/field name; "constructor" for constructors
	Visibility Visibility
	IsStatic   bool
	IsReadonly bool
	IsAbstract bool
	// 字段区：`name: T = init`，无 type 无 init 即补 any。
	FieldType TypeNode // nil => infer from Init
	Init      Expr     // nil if none
	// 方法/构造区：`m(x): R`，declare/abstract 无体。
	Generics   []*GenericParam
	Params     []*Param
	HasVarArg  bool
	VarArgName *Ident
	VarArgType TypeNode
	ReturnType TypeNode // nil for constructors or unannotated
	Body       *Block   // nil for abstract/declare (no body)
	IsAsync    bool     // `async` method: may await; erases to plain method
	S          token.Span
}

// Span 取源码区间，如 `x=1` 圈整句供诊断画线。
//
// 设计者视角：成员 Span 只圈自身声明（含修饰符与签名），不含前后逗号与换行，
// 让“私有字段泄漏”“抽象方法有体”等诊断能精准下划线。Body 的块 Span 另计，
// 外层 S 与内层 Block.S 形成大小套娃。排查现场：若成员报错圈到整类，检查是否
// 误用了 ClassDecl.S 而非 ClassMember.S。
func (n *ClassMember) Span() token.Span { return n.S }

// IsConstructor 报告该成员是否为构造器，供发射器生成 __init 调用约定.
//
// 设计者视角：构造器判定只认 Kind==ClassConstructor、不认 Name 字符串，好比只认
// 印章不认手写签名——Name 只是展示用的 "constructor"，真正的身份是 parser 按语法
// 位置盖的 Kind 章，避免用户写个同名普通方法被误当构造器。障碍是 Lua 侧无构造器
// 概念：emitter 靠此分支把 `new` 翻译为表分配 + __init 调用。排查现场：若 new 调用
// 未触发初始化，检查成员 Kind 是否在解析期被标错，而非 Name 拼写。
func (n *ClassMember) IsConstructor() bool { return n.Kind == ClassConstructor }

// GenericParam 表示泛型参数声明（名称 + 约束 + 默认值）；S 为源码 Span，用于诊断与定位。
//
// 设计者视角：泛型参数是“类型的形参”，长相酷似函数 Param 但活在类型层：Constraint
// 是上限详见实现，排查看 Span。
// 约束与默认值的前后依赖（Default 必须满足 Constraint），但 AST 只存原文不验证，
// 把“是否合法”留给 checker 的实例化阶段，保持语法层无类型计算。不变量：Name 永非
// nil；Constraint/Default 为 nil 即无约束/无默认。排查现场：若泛型报错无位置，检查
// 此处 S 是否覆盖 `T extends K = D` 全段，而非仅 Name。
type GenericParam struct {
	Name       *Ident
	Constraint TypeNode
	Default    TypeNode
	S          token.Span
}

// Span 取源码区间，如 `x=1` 圈整句供诊断画线。
//
// 设计者视角：泛型参数的 Span 覆盖 `Name extends C = D` 全段而非仅 Name，让
// “约束不满足默认值”“循环默认”等诊断能圈出整段因果链。parser 预拼 S，checker
// 直接复用，无需回溯三个子节点重算。排查现场：若高亮只剩名字，检查 parser 拼
// S 时是否漏了 Constraint/Default 的 End。
func (n *GenericParam) Span() token.Span { return n.S }
