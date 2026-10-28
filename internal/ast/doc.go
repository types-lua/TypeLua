// Package ast 定义 TypeLua 编译器的抽象语法树（AST）节点。
//
// AST 是词法、语法、类型三类诊断挂靠的同一套坐标详见实现，排查看 Span。
// 词法/语法只负责把它拼出来，检查器只读它并把类型写进符号表，发射器只读它
// 并擦除类型。没有任何阶段回头改写 AST 的形状（宏展开与 tree-shaking 是
// 显式的、发生在检查/发射之前的原地改写，仍以 AST 为唯一事实来源）。
//
// …（略，详见实现；排查先看 Span/诊断）
//   - 被依赖：parser（构造 AST）、checker（只读 + 符号表）、emitter（只读）、
//     expand（宏展开原地改写）、shake（tree-shaking 原地裁剪）、lsp（导航/引用）、
//     compiler（调度与诊断聚合）。
package ast
