package ast

import (
	"github.com/typelua/typelua/internal/token"
)

// Declaration nodes (types, interfaces, enums, macros).

// Type declarations.

// TypeAliasDecl 表示 `type Name = T` 类型别名声明；S 为源码 Span，用于诊断与定位。
//
// 设计者视角：别名是“类型的快捷方式”而非新类型——Name 挂名、Type 存本体、Generics 管参数化。
// 障碍是递归别名 `type N = { next: N }`：AST 只存引用不展开，递归合法性由 checker 的
// visit 假设集判定。Declared 为 true 即 `declare type` ambient，只供检查不发射。
// 不变量：Type 永非 nil。排查现场：若递归别名栈溢出，检查 checker 是否绕过 assignable
// 而直接展开 Type。
type TypeAliasDecl struct {
	Name     *Ident
	Generics []*GenericParam
	Type     TypeNode
	Exported bool
	Declared bool
	S        token.Span
}

func (*TypeAliasDecl) stmtNode() {}

// Span returns the source span of this node.
//
// 设计者视角：别名 Span 覆盖 `type Name<...> = T` 全句，循环别名报错圈整句，T 写错圈
// Type 子段。排查现场：若泛型别名高亮只剩名字，检查 Generics 区间是否纳入 S。
func (n *TypeAliasDecl) Span() token.Span { return n.S }

// InterfaceDecl 表示 `interface Name { ... }` 接口声明；S 为源码 Span，用于诊断与定位。
//
// 设计者视角：接口是“结构类型的草稿纸”——Fields 只记签名（Type/Optional/Readonly），
// Extends 记多继承源（可空）。与 ClassDecl 的名义身份不同，接口只看形状，
// class 实例能否赋给接口由 classObjectView 快照复用判定。Declared 同为 ambient 开关。
// 不变量：Fields 可空（空接口即 `any` 除外皆可）。排查现场：若接口继承缺成员，检查
// Extends 逐个展开而非仅看自身 Fields。
type InterfaceDecl struct {
	Name     *Ident
	Generics []*GenericParam
	Extends  []TypeNode
	Fields   []*InterfaceField
	Exported bool
	Declared bool
	S        token.Span
}

func (*InterfaceDecl) stmtNode() {}

// Span returns the source span of this node.
//
// 设计者视角：接口 Span 罩住 `interface Name ... { ... }` 全体，字段缺失圈缺失处，
// 继承冲突圈 Extends 子段。排查现场：若字段报错圈到接口名，检查是否误用本 S 而非
// InterfaceField.S。
func (n *InterfaceDecl) Span() token.Span { return n.S }

// InterfaceField 表示接口中的一个字段/方法签名；S 为源码 Span，用于诊断与定位。
//
// 设计者视角：接口字段是“最小契约行”——Name 认键，Type 报价，Optional（`?`）放宽必填，
// Readonly 禁写。不与 TableTypeField 合并：接口活在声明层、table 类型活在表达式层，
// 分家让解析入口各走各路。不变量：Name/Type 永非 nil。排查现场：若可选字段仍报缺失，
// 检查 Optional 是否在 parser 置位。
type InterfaceField struct {
	Name     *Ident
	Type     TypeNode
	Optional bool
	Readonly bool
	S        token.Span
}

// Span returns the source span of this node.
//
// 设计者视角：字段 Span 覆盖 `name?: T` / `readonly name: T` 全段，`?` 漏写圈问号位。
// 排查现场：若 readonly 误报，检查 Readonly 标志与 Span 修饰符区间是否一致。
func (n *InterfaceField) Span() token.Span { return n.S }

// EnumDecl 表示 `enum Name { ... }` 枚举声明；S 为源码 Span，用于诊断与定位。
//
// 设计者视角：枚举是“命名常量表”——Members 保序，值可显式（Value 非 nil）可自增（nil 即 auto）。
// checker 按“首值 0/上值+1、字符串枚举须全显式”规则填 auto，emitter 降为 Lua 表。
// 候选：AST 期填 auto vs checker 期填；选后者，AST 只存“是否手写”，自增值语义留给检查。
// 排查现场：若自增值跳号，检查 checker 计数而非此处顺序。
type EnumDecl struct {
	Name     *Ident
	Members  []*EnumMember
	Exported bool
	Declared bool
	S        token.Span
}

func (*EnumDecl) stmtNode() {}

// Span returns the source span of this node.
//
// 设计者视角：枚举 Span 罩住 `enum Name { ... }` 全体，成员重复圈成员，值类型错圈 Value。
// 排查现场：若成员报错圈整枚举，检查是否误用本 S 而非 EnumMember.S。
func (n *EnumDecl) Span() token.Span { return n.S }

// EnumMember 表示枚举中的一个成员（可带显式值）；S 为源码 Span，用于诊断与定位。
//
// 设计者视角：成员是“名 + 可选值”二元组——Value 为 nil 即 auto 待填，非 nil 即手写值
// （数字/字符串字面量）。AST 不判“混用自增与手写是否合法”，留给 checker 按枚举种类
// 一次验明。不变量：Name 永非 nil。排查现场：若 auto 值与手写混排错，检查 Value nil
// 分支而非名字。
type EnumMember struct {
	Name  *Ident
	Value Expr // nil => auto
	S     token.Span
}

// Span returns the source span of this node.
//
// 设计者视角：成员 Span 只圈 `Name = Value` 本行，值非法圈 Value，名冲突圈 Name。
// 排查现场：若自增枚举仍要求显式值，检查 checker 的字符串枚举分支。
func (n *EnumMember) Span() token.Span { return n.S }

// MacroDef is a compile-time macro: `macro name(params) = expr` for
// expressions, `macro name(params) do stmts end` for statements.
// 尾部的 `...`（HasVarArg）收集额外调用实参；函数体内裸 `...` 仅在
// 调用实参位置展开它们.
// Definitions are expanded (and erased) before type checking.
//
// 设计者视角：宏是“检查前蒸发的糖”——IsExpr 分两道：Body（表达式宏 `= expr`）与
// Block（语句宏 `do stmts end`），Params 为形参名表，HasVarArg 吞多余实参，体内裸
// `...` 只在实参位置展开。障碍是卫生性：展开在检查前原地改写 AST，宏内裸 Ident 在
// parser 期特许为语句占位（见 ExprStmt），待代入后复验。不变量：IsExpr 为真则 Body 非
// nil，否则 Block 非 nil。排查现场：若宏展开后行号飞回定义处，检查展开是否重 Stamp
// 调用点 Span；若 `...` 未展开，检查是否不在实参位置。
type MacroDef struct {
	Name      *Ident
	Params    []*Ident
	HasVarArg bool
	Body      Expr   // expression macro (IsExpr)
	Block     *Block // statement macro (!IsExpr)
	IsExpr    bool
	S         token.Span
}

func (*MacroDef) stmtNode() {}

// Span returns the source span of this node.
//
// 设计者视角：宏定义 Span 覆盖整段 `macro ...`，调用点错误圈调用处而非定义处；
// 定义体内错误圈 Body/Block。排查现场：若宏错误全指回定义，检查展开是否丢失调用点
// Span 回填。
func (n *MacroDef) Span() token.Span { return n.S }
