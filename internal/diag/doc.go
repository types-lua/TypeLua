// Package diag 定义编译器诊断详见实现，排查看 Span。
//
// 诊断是“整改单”：Code（稳定编号）+ Message + Span + Severity。编码按车间
// 分段：TL1xxx 词法、TL2xxx 语法、TL3xxx 类型（含 TL33xx 类、TL36xx 条件
// 类型、TL37xx 宏、TL38xx 并发）、TL4xxx 工程/发射。看到前缀就知道去哪
// 个车间查。
//
// …（略，详见实现；排查先看 Span/诊断）
// 模块间协作
//   - 依赖：token、types（消息缩写）。
//   - 被依赖：lexer、parser、checker、expand、compiler、lsp 等所有产生诊断的包。
package diag
