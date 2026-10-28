// Package ast 定义 TypeLua 编译器的抽象语法树（AST）节点。
//
// AST 是词法、语法、类型三类诊断挂靠的同一套坐标（token.Span）的唯一事实来源：
// 词法/语法只负责把它拼出来，检查器只读它并把类型写进符号表，发射器只读它
// 并擦除类型。没有任何阶段回头改写 AST 的形状（宏展开与 tree-shaking 是
// 显式的、发生在检查/发射之前的原地改写，仍以 AST 为唯一事实来源）。
//
// # 为什么 AST 只存语法形状，不存语义
//
// 把“语法形状”与“语义类型”分家，是整条流水线能保持单向流动的前提：
// 检查器遇到不确定的类型会填 `any` 继续，但绝不动 AST；发射器看不到类型，
// 就不可能把类型错误带进最终 Lua。如果 AST 节点里直接挂类型，发射器就会
// 与检查器耦合，类型擦除的结构性保证也就不复存在。施工图不是楼房：
// AST 是图纸，types.Type 才是建成的实物。
//
// # 物理布局与层次
//
// 所有节点共享一个最小契约：Node 只要求 Span() token.Span。Go 用“空方法标记
// 接口”把节点分进三个家族，运行时零开销、编译期类型安全：
//
//	Node (Span() token.Span)
//	├── Stmt  (stmtNode())   语句家族
//	│   ├── 赋值：AssignStmt / LocalAssign
//	│   ├── 定义：LocalFuncDef / FuncDefStmt / TypeAliasDecl / InterfaceDecl /
//	│   │         EnumDecl / ClassDecl / MacroDef
//	│   ├── 流程：IfStmt / WhileStmt / RepeatStmt / NumericFor / GenericFor /
//	│   │         DoBlock / SelectStmt
//	│   └── 跳转与其它：ReturnStmt / BreakStmt / GotoStmt / LabelStmt /
//	│                   DeferStmt / SpawnStmt / ImportStmt / ExprStmt / EmptyStmt
//	├── Expr  (exprNode())   表达式家族
//	│   ├── 字面量：Ident / NumberLit / StringLit / BoolLit / NilLit / VarArg
//	│   ├── 复合：TableLit / FuncLit / ParenExpr
//	│   ├── 后缀：IndexExpr / FieldExpr / CallExpr / MethodCallExpr
//	│   ├── 运算：BinOpExpr / UnOpExpr
//	│   └── 断言与标记：CastExpr / NonNullExpr / AwaitExpr
//	├── TypeNode (typeNode()) 类型语法家族
//	│   └── NamedType / LiteralType / ArrayType / UnionType / IntersectionType /
//	│       OptionalType / ParenType / TupleType / TableType / FuncType /
//	│       TypeofType / KeyofType / MappedType / IndexedAccessType /
//	│       ConditionalType / InferType
//	└── 容器（不属于三家族，但实现 Node）
//	    ├── File{Name, Block, EOF}   一个源文件；EOF 为文件尾 Span，空文件靠它报错
//	    └── Block{Stmts, Ret, S}     语句序列 + 可选 return
//
// 每个节点自带一个 S token.Span（或 NameSpan 等辅坐标），而不是指回词法 token：
// 节点拥有自己的坐标，语法层可以为合成节点制造 Span，后续诊断永远有门牌号。
// 容器节点把“return 必须在块末”这条 Lua 语法规则直接编码进数据结构——
// Ret 独立于 Stmts 存放，想把 return 放错位置都没地方放。
//
// # 为什么用空方法标记接口分家族
//
// Go 没有代数数据类型；用未导出方法（stmtNode/exprNode/typeNode）做标记，
// 既让编译器在类型层面阻止“把表达式塞进语句槽”，又避免接口方法表的运行时
// 开销。新增节点家族时必须显式实现标记方法，忘掉就会在编译期报错——
// 这是“忘加分支”的防火墙。
//
// # 为什么节点是扁平 struct 而非带父指针
//
// 节点只存子节点切片，不存父指针：遍历方向唯一（自顶向下），避免父子
// 双向引用导致的 GC 压力与循环引用；需要父上下文时由遍历器（checker/
// emitter）自己维护栈。切片而非链表，是为了 cache 友好与 range 遍历。
//
// 明确不做
//   - 不存类型信息、符号引用、作用域（这些由 checker 的符号表承载）；
//   - 不存父指针与源文本（坐标靠 Span，文本靠 token.Literal/源文件）；
//   - 不做不可变保证（宏展开/tree-shaking 会原地改写，但仅在明确阶段）。
//
// 不变量
//   - 任何非 nil 节点的 Span() 返回非零坐标（文件名可能为空的合成节点除外）；
//   - Block.Stmts 不含 ReturnStmt；ReturnStmt 永远在 Block.Ret；
//   - 节点家族由未导出标记方法唯一确定，互不相交；
//   - TypeNode 仅表示语法，不等于 types.Type 语义值。
//
// 模块间协作
//   - 依赖：token（Span/Pos）。
//   - 被依赖：parser（构造 AST）、checker（只读 + 符号表）、emitter（只读）、
//     expand（宏展开原地改写）、shake（tree-shaking 原地裁剪）、lsp（导航/引用）、
//     compiler（调度与诊断聚合）。
package ast
