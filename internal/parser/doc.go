// Package parser 把 Token 流组装成 AST（递归下降 + Pratt 表达式 + 错误恢复）。
//
// 语法层是“装配工”：把 `F<A>(x)` 的 Token 拼成调用而非比较，单错（如缺 `end`）同步恢复续拼后文。
//
// # 为什么用递归下降 + Pratt
//
// 递归下降让 `parseIf` 对 `if` 一眼可对；Pratt 让 `2^3^2` 算成 512 而非 64，
// `a..b..c` 右结合一次查表即得，typeof/keyof 切上下文亦只换函数。
//
// # 错误恢复
//
// 试探靠存 pos+诊断长、败则回滚（如 `f<T>(` 无 `(` 即按比较回退）；10 万层括号到 256 层熔断，只报一次 TL2015。
//
// # 软关键字
//
// `type`/`interface`/`macro`/`defer`/`spawn`/`await`/`select` 等在词法层一律
// 标为关键字标签，parser 按位置决定是声明/语句还是普通标识符调用，保持
// 词法无状态。
//
// 明确不做
//   - 不做类型检查或求值；
//   - 不做宏展开（expand 负责，发生在检查之前）；
//   - 不生成代码。
//
// 不变量
//   - ParseFile 对任意输入不 panic（深度超限转为诊断）；
//   - 返回的 AST 节点 Span 单调、可定位；
//   - `return` 只能出现在 Block.Ret，结构上不可错放。
//
// 模块间协作
//   - 依赖：
//     lexer  —— 词法产出 Token 流与词法诊断；ParseFile 先 Lex 再 parse。
//     token  —— Token/Kind/Span/Pos 是 parser 的输入坐标与输出坐标货币。
//     ast    —— parser 的唯一产出：构造所有 Stmt/Expr/TypeNode 节点与 Span。
//     diag   —— 语法诊断的载体与错误码（Parse*）。
//   - 被依赖：
//     compiler —— 调度 ParseFile，聚合诊断，驱动后续检查/发射。
//     checker  —— 只读 AST 做类型检查；parser 不做类型语义。
//     expand   —— 宏展开发生在检查之前，但以 parser 产出的 AST 为输入。
//     lsp      —— 导航/补全/诊断复用 parser 产出与 Span。
//   - 数据流：src ──lexer──► []Token ──parser──► *ast.File + []diag ──checker──► ...
//   - 契约：parser 不修改 Token 流（toks 只读），不回写 AST 形状给上游；
//     试探解析通过 saveCursor/restoreCursor 零副作用。
package parser
