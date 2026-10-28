// Package types 定义 TypeLua 的语义类型值（Type 接口及其 ~30 种实现）。
//
// Type 是“建成实物”（如 `Box<Cat>` 实例），TypeNode 是“图纸”（`Box<T>` 写法）；Kind 分派、String 供报错与缓存键。
//
// # 为什么类型是接口 + 多实现
//
// Go 的接口天然表达“多种类型的一种”，让子类型判定、字符串化、缓存都能按
// 具体类型分派；新增类型只需实现 Kind/String，代价是穷举 switch 处必须
// 覆盖新类型（编译器不会强制，靠测试与审查兜底）。
//
// # 为什么 AST 类型与语义类型分家
//
// AST 类型节点是语法形状（可能含未解析的名字、泛型参数），语义类型是
// 检查后的值（已解析、已实例化、已范式化）。分家让宏展开、泛型实例化、
// 条件求值都能在语义层自由变换，而不污染语法树。
//
// # 核心结构
//
//   - Primitive/Union/Intersection/Array/Map/Tuple/Func/Object/Literal/Enum/
//     Class/ClassValue/Conditional/InferParam/GenericParam/Ref/Mapped/
//     IndexedAccess 等；
//   - Field{Type, Optional, Readonly, Access, Owner}：用同一结构表达普通对象
//     与类成员，靠约定区分；
//   - ClassDef：仅含 Name 的身份令牌，指针相等即同一声明。
//
// # 为什么 Union 已范式化
//
// `string|never|string` 展平成 `string`：同一类型一种写法，`A|B <: A|B` 判定与缓存 key 才稳（构造多一次规范化换热路径快）。
//
// # 为什么 ClassDef 只有一个 Name
//
// 它是身份证号而非照片：`Dog` 与结构相同的 `{name: string}` 长得像也不是一家人，指针相等才是同声明。
//
// 明确不做
//   - 不做类型检查或作用域（checker 负责）；
//   - 不做语法解析（parser 负责）；
//   - 不做并发安全的缓存（调用方串行化）。
//
// 不变量
//   - Union/Intersection 已范式化（展平、去 never、去重、排序）；
//   - ClassDef 指针相等即同一声明；
//   - Type 接口仅要求 Kind() 与 String()。
//
// 模块间协作
//   - 依赖：无内部包（仅标准库）。
//   - 被依赖：checker（构造/查询/替换/实例化/条件求值）、diag（类型字符串化）、
//     compiler（增量/require）、lsp（悬停/补全）。
//   - 与 parser 的关系：parser 只产出 ast.TypeNode（语法图纸），由 checker
//     的类型声明处理降级为 types.Type（语义实物）；types 包不反向依赖 parser。
//   - 与 emitter 的关系：emitter 不接触 types（类型擦除）；它只读 ast 与符号
//     表的运行时信息，类型信息在检查阶段全部消费完毕。
//   - 与 expand/shake 的关系：宏展开与 tree-shaking 通过 ast 与 checker 协作，
//     types 仅作为检查期的不可变值参与，不被它们改写。
package types
